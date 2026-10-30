package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/shlex"
	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
)

type VrrpOptions struct {
	Interface int
	Group     int
	Address   string
}

type DockerNodeOptions struct {
	Name       string
	ShortName  string
	Ipv6       bool
	Mpls       bool
	Vrfs       []string
	Vrrps      []VrrpOptions
	Volumes    []string
	Image      string
	LazyCreate bool
}

func vrrpInterfaceName(ifIndex, group int) string {
	return fmt.Sprintf("vrrp-%d-%d", ifIndex, group)
}

type DockerNodeStatus struct {
	Running bool
}

type DockerInterface struct {
	Configured bool
	State      link.IfState
}

type DockerNode struct {
	PrjID          string
	ID             string
	Name           string
	ShortName      string
	Config         *options.DockerNodeConfig
	Interfaces     map[string]*DockerInterface
	mu             sync.RWMutex
	nsMu           sync.Mutex
	configMu       sync.Mutex
	LocalNetnsName string
	Running        bool
	pid            int // cached container PID; valid only while Running
	ConfigLoaded   bool
	ImageName      string
	IPv6           bool
	LazyCreate     bool
	Mpls           bool
	Vrfs           []string
	Vrrps          []VrrpOptions
	Volumes        []string
	Logger         *logrus.Entry
}

func (n *DockerNode) GetName() string {
	return n.Name
}

func (n *DockerNode) GetShortName() string {
	if n.ShortName == "" {
		return n.Name
	}
	return n.ShortName
}

func (n *DockerNode) GetType() string {
	return "docker"
}

func (n *DockerNode) GetFullType() string {
	return fmt.Sprintf("docker.%s", n.Config.Type)
}

func (n *DockerNode) GetStatus() (DockerNodeStatus, error) {
	if n.ID == "" {
		return DockerNodeStatus{Running: false}, nil
	}

	client, err := NewDockerClient()
	if err != nil {
		return DockerNodeStatus{}, err
	}
	defer client.Close()

	state, err := client.GetState(context.Background(), n.ID)
	if err != nil {
		return DockerNodeStatus{}, err
	}

	return DockerNodeStatus{
		Running: state == "running",
	}, nil
}

func (n *DockerNode) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Running
}

func (n *DockerNode) Create(imgName string, ipv6 bool) error {
	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()

	present, err := client.IsImagePresent(ctx, imgName)
	if err != nil {
		return err
	} else if !present {
		return fmt.Errorf("docker image %s not present", imgName)
	}

	containerName := fmt.Sprintf("%s%s.%s", options.NETEM_ID, n.PrjID, n.Name)
	volumes := slices.Concat(n.Config.Volumes, n.Volumes)

	if n.ID, err = client.Create(ctx, imgName, containerName, n.Name, volumes, ipv6, n.Mpls); err != nil {
		return err
	}

	return nil
}

func (n *DockerNode) GetNetns() (netns.NsHandle, error) {
	if n.IsRunning() {
		return n.GetRunningNetns()
	}

	return n.GetLocalNetns()
}

// resolvePid returns the cached container PID, invoking resolve only when no
// PID is cached. The PID is stable for the lifetime of a running container.
func (n *DockerNode) resolvePid(resolve func() (int, error)) (int, error) {
	n.mu.RLock()
	pid := n.pid
	n.mu.RUnlock()
	if pid > 0 {
		return pid, nil
	}

	pid, err := resolve()
	if err != nil {
		return -1, err
	}

	n.mu.Lock()
	if n.pid == 0 {
		n.pid = pid
	}
	n.mu.Unlock()
	return pid, nil
}

func (n *DockerNode) runningPid(client *DockerClient) (int, error) {
	return n.resolvePid(func() (int, error) {
		return client.Pid(context.Background(), n.ID)
	})
}

func (n *DockerNode) invalidatePid() {
	n.mu.Lock()
	n.pid = 0
	n.mu.Unlock()
}

func (n *DockerNode) GetRunningNetns() (netns.NsHandle, error) {
	// Docker is consulted only on the first call after Start(); Stop(), Kill()
	// and Close() invalidate the cached PID.
	client, err := NewDockerClient()
	if err != nil {
		return netns.NsHandle(0), err
	}
	defer client.Close()

	pid, err := n.runningPid(client)
	if err != nil {
		return netns.NsHandle(0), err
	}
	return netns.GetFromPid(pid)
}

func (n *DockerNode) GetLocalNetns() (netns.NsHandle, error) {
	n.nsMu.Lock()
	defer n.nsMu.Unlock()

	if n.LocalNetnsName == "" {
		// first created local named ns to detach interface without delete it
		name := fmt.Sprintf("%s%s", n.PrjID, n.Name)
		ns, err := link.CreateNetns(name)
		if err != nil {
			return netns.NsHandle(0),
				fmt.Errorf("error when creating node netns '%s': %v", name, err)
		}
		n.LocalNetnsName = name

		return ns, nil
	}

	localNS, err := netns.GetFromName(n.LocalNetnsName)
	if err != nil {
		return netns.NsHandle(0), fmt.Errorf("unable to get netns associated to node %s: %v", n.Name, err)
	}

	return localNS, nil
}

func (n *DockerNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("eth%d", ifIndex)
}

func interfacePreparationScript(ifNames []string, disableTso, enableMpls bool) (string, bool) {
	lines := []string{"rc=0"}
	hasWork := false

	for _, ifName := range ifNames {
		if !strings.HasPrefix(ifName, "eth") {
			continue // not applicable to management interface
		}
		if disableTso {
			hasWork = true
			lines = append(lines, fmt.Sprintf(
				"ethtool -K %s tx off || { echo 'ethtool failed on %s'; rc=1; }",
				ifName, ifName,
			))
		}
		if enableMpls {
			hasWork = true
			lines = append(lines, fmt.Sprintf(
				"sysctl -w net.mpls.conf.%s.input=1 || { echo 'sysctl failed on %s'; rc=1; }",
				ifName, ifName,
			))
		}
	}

	if !hasWork {
		return "", false
	}
	lines = append(lines, "exit $rc")
	return strings.Join(lines, "\n"), true
}

func (n *DockerNode) prepareInterfaces(client *DockerClient, ifNames []string) {
	script, ok := interfacePreparationScript(ifNames, !n.Config.Options.Tso, n.Mpls)
	if !ok {
		return
	}

	if _, err := client.Exec(context.Background(), n.ID, []string{"sh", "-c", script}); err != nil {
		n.Logger.Warnf("Unable to prepare interfaces %v: %v", ifNames, err)
	}
}

func (n *DockerNode) AttachInterface(ifName string, ifIndex int, configure bool) error {
	iface := &DockerInterface{
		Configured: false,
		State:      link.IFSTATE_UP,
	}
	n.mu.Lock()
	n.Interfaces[ifName] = iface
	n.mu.Unlock()

	if configure && n.IsRunning() {
		client, err := NewDockerClient()
		if err != nil {
			return err
		}
		defer client.Close()

		n.prepareInterfaces(client, []string{ifName})

		n.mu.Lock()
		if st := n.Interfaces[ifName]; st != nil {
			st.Configured = true
		}
		n.mu.Unlock()
	}

	return nil
}

func (n *DockerNode) DetachInterface(ifName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.Interfaces, ifName)
	return nil
}

func (n *DockerNode) ConfigureInterfaces() error {
	if !n.IsRunning() {
		return nil
	}

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	n.mu.Lock()
	pending := make([]string, 0)
	for ifName, st := range n.Interfaces {
		if !st.Configured {
			pending = append(pending, ifName)
		}
	}
	n.mu.Unlock()

	if len(pending) > 0 {
		n.prepareInterfaces(client, pending)

		n.mu.Lock()
		for _, ifName := range pending {
			if st := n.Interfaces[ifName]; st != nil {
				st.Configured = true
			}
		}
		n.mu.Unlock()
	}

	return nil
}

func (n *DockerNode) AttachMgntInterface(ifName string, ns netns.NsHandle, IPAddress string) error {
	n.mu.Lock()
	n.Interfaces[ifName] = &DockerInterface{
		Configured: true,
		State:      link.IFSTATE_UP,
	}
	n.mu.Unlock()

	// Set IP address
	if err := link.IpAddressAdd(ifName, ns, IPAddress); err != nil {
		return err
	}

	return nil
}

func (n *DockerNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	ifName := n.GetInterfaceName(ifIndex)
	cmd := []string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", ifName}
	return client.ExecOutStream(ctx, n.ID, cmd, out)
}

func (n *DockerNode) ExecCommand(
	ctx context.Context,
	cmd []string,
	in io.ReadCloser,
	out io.Writer,
	tty bool,
	ttyHeight uint,
	ttyWidth uint,
	resizeCh chan term.Winsize) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return client.ExecTty(
		ctx,
		n.ID,
		cmd, in, out,
		tty, ttyHeight, ttyWidth,
		resizeCh)
}

func (n *DockerNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !n.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}

	client, err := NewDockerClient()
	if err != nil {
		return nil, nil, -1, err
	}
	defer client.Close()

	pid, err := n.runningPid(client)
	if err != nil {
		return nil, nil, -1, err
	}

	nsenterCmd := exec.Command("nsenter", append([]string{"-t", strconv.Itoa(pid), "-a", "--"}, cmd...)...)
	var stdout, stderr bytes.Buffer
	nsenterCmd.Stdout = &stdout
	nsenterCmd.Stderr = &stderr
	err = nsenterCmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, nil, -1, err
		}
	}

	return stdout.Bytes(), stderr.Bytes(), exitCode, nil
}

func (n *DockerNode) GetConsoleCmd(shell bool) ([]string, error) {
	cmd := n.Config.Commands.Console
	if shell {
		cmd = n.Config.Commands.Shell
	}

	cmds, err := shlex.Split(cmd)
	if err != nil {
		return []string{}, fmt.Errorf("unable to parse console cmd: %v", err)
	}

	return cmds, nil
}

func (n *DockerNode) Start() error {
	if n.IsRunning() {
		return nil
	}

	// A stopped container may have left a stale PID behind after an external
	// kill or a previous failed start.
	n.invalidatePid()

	n.Logger.Debug("Start Node")
	startAll := time.Now()

	if n.ID == "" {
		if err := n.Create(n.ImageName, n.IPv6); err != nil {
			return fmt.Errorf("unable to create container for node %s: %w", n.Name, err)
		}
	}

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	if err := client.Start(ctx, n.ID); err != nil {
		return err
	}
	if _, err := n.runningPid(client); err != nil {
		return fmt.Errorf("unable to get container pid for node %s: %w", n.Name, err)
	}
	n.Logger.Debugf("Start: docker start took %s", time.Since(startAll))

	started := false
	var currentNS, targetNS netns.NsHandle
	var ifStates map[string]link.IfState

	defer func() {
		if !started {
			if targetNS != 0 && currentNS != 0 {
				if err := link.MoveInterfacesNetns(ifStates, targetNS, currentNS); err != nil {
					n.Logger.Warnf("unable to roll back interface move after start failure: %v", err)
				}
			}
			if err := client.Stop(ctx, n.ID); err != nil {
				n.Logger.Warnf("unable to stop container after start failure: %v", err)
			}
			n.mu.Lock()
			n.Running = false
			n.pid = 0
			n.mu.Unlock()
		}

		if targetNS != 0 {
			targetNS.Close()
		}
		if currentNS != 0 {
			currentNS.Close()
		}
	}()

	ifStates = n.GetInterfacesState()
	allIfNames := make([]string, 0, len(ifStates))
	n.mu.RLock()
	pendingIfNames := make([]string, 0, len(ifStates))
	for ifName := range ifStates {
		allIfNames = append(allIfNames, ifName)
		if st := n.Interfaces[ifName]; st == nil || !st.Configured {
			pendingIfNames = append(pendingIfNames, ifName)
		}
	}
	n.mu.RUnlock()

	if len(allIfNames) > 0 {
		// Attach existing interfaces. Isolated nodes do not need a local
		// namespace or any namespace switching during start/stop.
		currentNS, err = n.GetLocalNetns()
		if err != nil {
			return err
		}

		targetNS, err = n.GetRunningNetns()
		if err != nil {
			return err
		}

		moveStart := time.Now()
		if err := link.MoveInterfacesNetns(ifStates, currentNS, targetNS); err != nil {
			return fmt.Errorf("unable to attach interfaces: %v", err)
		}
		n.Logger.Debugf("Start: interface move took %s", time.Since(moveStart))

		if len(pendingIfNames) > 0 {
			prepareStart := time.Now()
			n.prepareInterfaces(client, pendingIfNames)
			n.Logger.Debugf("Start: interface prepare took %s", time.Since(prepareStart))
		}
	}

	n.mu.Lock()
	for _, ifName := range pendingIfNames {
		if st := n.Interfaces[ifName]; st != nil {
			st.Configured = true
		}
	}
	n.Running = true
	n.ConfigLoaded = false
	n.mu.Unlock()
	started = true
	n.Logger.Debugf("Start: total took %s", time.Since(startAll))

	return nil
}

func (n *DockerNode) Stop() error {
	if !n.IsRunning() {
		return nil
	}

	n.Logger.Debug("Stop Node")

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	// Detach interfaces when the node has any.
	ifStates := n.GetInterfacesState()
	var currentNS, targetNS netns.NsHandle
	if len(ifStates) > 0 {
		currentNS, err = n.GetRunningNetns()
		if err != nil {
			return err
		}
		defer currentNS.Close()

		targetNS, err = n.GetLocalNetns()
		if err != nil {
			return err
		}
		defer targetNS.Close()

		if err := link.MoveInterfacesNetns(ifStates, currentNS, targetNS); err != nil {
			return fmt.Errorf("unable to attach interfaces: %v", err)
		}
	}

	ctx := context.Background()
	if err := client.Stop(ctx, n.ID); err != nil {
		if currentNS != 0 && targetNS != 0 {
			if rollbackErr := link.MoveInterfacesNetns(ifStates, targetNS, currentNS); rollbackErr != nil {
				n.Logger.Errorf("unable to roll back interface move after stop failure: %v", rollbackErr)
			}
		}
		return err
	}

	n.mu.Lock()
	n.Running = false
	n.pid = 0
	n.ConfigLoaded = false
	n.mu.Unlock()

	return nil
}

func (n *DockerNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	n.configMu.Lock()
	defer n.configMu.Unlock()

	var messages []string

	if !n.IsRunning() {
		n.Logger.Info("LoadConfig: node not running")
		return messages, nil
	}

	n.mu.RLock()
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	if !configLoaded {
		ns, err := n.GetRunningNetns()
		if err != nil {
			return messages, fmt.Errorf("unable to get netns of node %s: %w", n.Name, err)
		}
		defer ns.Close()

		// create vrfs
		for idx, vrf := range n.Vrfs {
			if _, err := link.CreateVrf(vrf, ns, 10+idx); err != nil {
				return messages, err
			}
		}

		client, err := NewDockerClient()
		if err != nil {
			return messages, err
		}
		defer client.Close()

		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc

			ctx, cancel = context.WithTimeoutCause(
				ctx,
				time.Duration(timeout)*time.Second,
				fmt.Errorf("node %s: loadconfig op timeout", n.Name))
			defer cancel()
		}

		// create vrrps interfaces
		for _, vrrpGroup := range n.Vrrps {
			name := vrrpInterfaceName(vrrpGroup.Interface, vrrpGroup.Group)
			if _, err := link.CreateVRRPMacVlan(
				name, fmt.Sprintf("eth%d", vrrpGroup.Interface),
				vrrpGroup.Group, ns); err != nil {
				return messages, err
			}
			if err := link.SetInterfaceState(name, ns, link.IFSTATE_UP); err != nil {
				return messages, fmt.Errorf("unable to set VRRP interface %s up: %w", name, err)
			}

			// set ip address
			if _, err := client.Exec(ctx, n.ID, []string{"ip", "addr", "add", vrrpGroup.Address, "dev", name}); err != nil {
				return messages, err
			}

			// modify kernel settings to disable routes when interface
			// is in linkdown state
			_, err := client.Exec(ctx, n.ID, []string{"sysctl", "-w", fmt.Sprintf("net.ipv4.conf.%s.ignore_routes_with_linkdown=1", name)})
			if err != nil {
				return messages, err
			}
		}

		// Load confifuration files/folders
		if _, err := os.Stat(confPath); err == nil {
			for _, confFileOpts := range n.Config.ConfigurationFiles {
				confFile := path.Join(confPath, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
				if _, err := os.Stat(confFile); os.IsNotExist(err) {
					continue
				}

				if err := client.CopyTo(ctx, n.ID, confFile, confFileOpts.Source); err != nil {
					return messages, fmt.Errorf("unable to load config file %s:\n\tdest: %s\n\t%w", confFile, confFileOpts.Source, err)
				}
			}

			for _, confFolderOpts := range n.Config.ConfigurationFolders {
				filename := fmt.Sprintf("%s.%s", n.Name, confFolderOpts.DestSuffix)
				confFolderArchive := path.Join(confPath, filename)
				if _, err := os.Stat(confFolderArchive); os.IsNotExist(err) {
					continue
				}

				if err := client.CopyTo(ctx, n.ID, confFolderArchive, filepath.Join("/tmp", filename)); err != nil {
					return messages, fmt.Errorf("unable to copy archile file %s:\n\t%w", filename, err)
				}

				if _, err := client.ExecWithWorkingDir(ctx, n.ID, []string{"tar", "xzf", filepath.Join("/tmp", filename)}, "/"); err != nil {
					return messages, fmt.Errorf("unable to extract archile file %s in the node:\n\t%w", filename, err)
				}
			}
		}

		// Execute load config commands
		for _, loadConfigCmd := range n.Config.Commands.LoadConfig {
			canExec := true
			for _, filepath := range loadConfigCmd.CheckFiles {
				if !client.IsFileExist(context.Background(), n.ID, filepath) {
					canExec = false
				}
			}
			if !canExec {
				continue
			}

			cmd, err := shlex.Split(loadConfigCmd.Command)
			if err != nil {
				return messages, fmt.Errorf("unable to parse loadConfig command %s: %v", loadConfigCmd.Command, err)
			}

			output, err := client.Exec(ctx, n.ID, cmd)
			output = strings.Trim(output, " ")
			if err != nil {
				return messages, fmt.Errorf("node %s - unable to exec load config cmd %s - %v", n.Name, loadConfigCmd.Command, err)
			} else if output != "" && output != "\n" && n.Config.Options.Log {
				messages = append(messages, output)
			}
		}

		n.mu.Lock()
		n.ConfigLoaded = true
		n.mu.Unlock()
	}

	return messages, nil
}

func (n *DockerNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	configFilesData := make(map[string][]byte)

	client, err := NewDockerClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	n.mu.RLock()
	running := n.Running
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	filesDir := confDir
	if running || configLoaded {
		// create temp directory for the project
		dir, err := os.MkdirTemp(options.ServerConfig.Workdir, "gonetem-config-node"+n.Name+"-")
		if err != nil {
			return nil, fmt.Errorf("unable to create temp folder to save node config: %w", err)
		}
		defer os.RemoveAll(dir)

		if err := n.Save(dir, timeout); err != nil {
			return nil, fmt.Errorf("unable to save node configs in temp folder %s: %w", dir, err)
		}
		filesDir = dir
	}

	for _, confFileOpts := range n.Config.ConfigurationFiles {
		filepath := path.Join(filesDir, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
		if _, err := os.Stat(filepath); os.IsNotExist(err) {
			configFilesData[confFileOpts.Label] = []byte{}
			continue
		}

		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("unable to read config file '%s':\n\t%w", filepath, err)
		}
		configFilesData[confFileOpts.Label] = data
	}

	return configFilesData, nil
}

func (n *DockerNode) Save(dstPath string, timeout int) error {
	n.mu.RLock()
	running := n.Running
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	if !running || !configLoaded {
		n.Logger.Info("Save: node not running")
		return nil
	}

	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(
			ctx,
			time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// Execute save config commands
	for _, saveConfigCmd := range n.Config.Commands.SaveConfig {
		canExec := true
		for _, filepath := range saveConfigCmd.CheckFiles {
			if !client.IsFileExist(context.Background(), n.ID, filepath) {
				canExec = false
			}
		}
		if !canExec {
			continue
		}

		cmd, err := shlex.Split(saveConfigCmd.Command)
		if err != nil {
			return fmt.Errorf("unable to parse save config command %s: %v", saveConfigCmd.Command, err)
		}

		if _, err := client.Exec(ctx, n.ID, cmd); err != nil {
			return fmt.Errorf("node %s - unable to exec save config cmd %s - %v", n.Name, saveConfigCmd.Command, err)
		}
	}

	for _, confFileOpts := range n.Config.ConfigurationFiles {
		if !client.IsFileExist(ctx, n.ID, confFileOpts.Source) {
			continue
		}

		confFile := path.Join(dstPath, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
		if err := client.CopyFrom(ctx, n.ID, confFileOpts.Source, confFile); err != nil {
			return fmt.Errorf("unable to save config file %s:\n\t%w", confFile, err)
		}
	}

	for _, confFolderOpts := range n.Config.ConfigurationFolders {
		if !client.IsFolderExist(ctx, n.ID, confFolderOpts.Source) {
			continue
		}

		filename := fmt.Sprintf("%s.%s", n.Name, confFolderOpts.DestSuffix)
		confFolderArchive := path.Join(dstPath, filename)

		if _, err := client.ExecWithWorkingDir(ctx, n.ID, []string{"tar", "czf", "/tmp/" + filename, confFolderOpts.Source}, "/"); err != nil {
			return err
		}

		if err := client.CopyFrom(ctx, n.ID, "/tmp/"+filename, confFolderArchive); err != nil {
			return fmt.Errorf("node %s - unable to save archive file %s:\n\t%v", n.Name, filename, err)
		}
	}

	return nil
}

func (n *DockerNode) CopyFrom(source, dest string) error {
	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return client.CopyFrom(context.Background(), n.ID, source, dest)
}

func (n *DockerNode) CopyTo(source, dest string) error {
	client, err := NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return client.CopyTo(context.Background(), n.ID, source, dest)
}

func (n *DockerNode) GetInterfacesState() map[string]link.IfState {
	n.mu.RLock()
	defer n.mu.RUnlock()

	ifStates := make(map[string]link.IfState, len(n.Interfaces))
	for ifName, st := range n.Interfaces {
		ifStates[ifName] = st.State
	}

	return ifStates
}

func (n *DockerNode) SetInterfaceState(ifIndex int, state link.IfState) error {
	ifName := n.GetInterfaceName(ifIndex)

	n.mu.RLock()
	st, found := n.Interfaces[ifName]
	n.mu.RUnlock()
	if !found {
		return fmt.Errorf("interface %s.%d not found", n.GetName(), ifIndex)
	}
	if state == st.State {
		return nil
	}

	ns, err := n.GetNetns()
	if err != nil {
		return err
	}
	defer ns.Close()

	if err := link.SetInterfaceState(ifName, ns, state); err != nil {
		return err
	}

	n.mu.Lock()
	if current := n.Interfaces[ifName]; current != nil {
		current.State = state
	}
	n.mu.Unlock()

	return nil
}

func (n *DockerNode) Kill() error {
	if n.ID != "" && n.IsRunning() {
		n.Logger.Info("Kill: starting")
		start := time.Now()

		client, err := NewDockerClient()
		if err != nil {
			return err
		}
		defer client.Close()

		ctx := context.Background()
		if err := client.Stop(ctx, n.ID); err != nil {
			return err
		}
		n.mu.Lock()
		n.Running = false
		n.pid = 0
		n.mu.Unlock()
		n.Logger.Infof("Kill: done in %s", time.Since(start))
	}
	return nil
}

func (n *DockerNode) Close() error {
	n.Logger.Info("Close: starting")
	start := time.Now()

	if n.ID != "" {
		client, err := NewDockerClient()
		if err != nil {
			return err
		}
		defer client.Close()

		// Force removal also stops a running container. Deleting the container
		// destroys its netns, so there is no need for a separate kill phase.
		if err := client.Rm(context.Background(), n.ID); err != nil {
			return err
		}
	}

	n.mu.Lock()
	n.Running = false
	n.pid = 0
	n.ConfigLoaded = false
	n.Interfaces = make(map[string]*DockerInterface)
	n.mu.Unlock()

	n.nsMu.Lock()
	if n.LocalNetnsName != "" {
		link.DeleteNetns(n.LocalNetnsName)
		n.LocalNetnsName = ""
	}
	n.nsMu.Unlock()

	n.Logger.Infof("Close: total done in %s", time.Since(start))
	return nil
}

func getDockerConfigFromType(nType string) (*options.DockerNodeConfig, error) {
	switch nType {
	case "router":
		return &options.ServerConfig.Docker.Nodes.Router, nil
	case "host":
		return &options.ServerConfig.Docker.Nodes.Host, nil
	case "server":
		return &options.ServerConfig.Docker.Nodes.Server, nil
	case "p4sw":
		return &options.ServerConfig.Docker.Nodes.P4sw, nil
	default:
		for _, nConfig := range options.ServerConfig.Docker.ExtraNodes {
			if nConfig.Type == nType {
				return &nConfig, nil
			}
		}
		return nil, fmt.Errorf("docker node of type %s does not exist in the configuraiton", nType)
	}
}

func NewDockerNode(prjID string, nType string, dockerOpts DockerNodeOptions) (*DockerNode, error) {
	nConfig, err := getDockerConfigFromType(nType)
	if err != nil {
		return nil, err
	}

	node := &DockerNode{
		PrjID:      prjID,
		ID:         "",
		Name:       dockerOpts.Name,
		ShortName:  dockerOpts.ShortName,
		Config:     nConfig,
		Mpls:       dockerOpts.Mpls,
		Vrfs:       dockerOpts.Vrfs,
		Vrrps:      dockerOpts.Vrrps,
		Volumes:    dockerOpts.Volumes,
		Interfaces: make(map[string]*DockerInterface),
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    dockerOpts.Name,
		}),
	}

	imgName := options.GetDockerImageId(nConfig.Image)
	if dockerOpts.Image != "" {
		imgName = options.GetDockerImageId(dockerOpts.Image)
	}
	node.ImageName = imgName
	node.IPv6 = dockerOpts.Ipv6
	node.LazyCreate = dockerOpts.LazyCreate

	if !node.LazyCreate {
		if err := node.Create(imgName, dockerOpts.Ipv6); err != nil {
			return node, err
		}
	}
	return node, nil
}
