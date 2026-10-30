package ovs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"sync"

	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/docker"
	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
)

type OvsNode struct {
	PrjID       string
	Name        string
	ShortName   string
	Running     bool
	OvsInstance *OvsProjectInstance
	Interfaces  map[string]link.IfState
	mu          sync.RWMutex
	instanceMu  sync.Mutex
	Logger      *logrus.Entry
}

func (o *OvsNode) ensureInstance() error {
	o.instanceMu.Lock()
	defer o.instanceMu.Unlock()

	if o.OvsInstance != nil {
		return nil
	}

	instance := GetOvsInstance(o.PrjID)
	if instance == nil {
		return fmt.Errorf("ovswitch instance for project %s is not initialized", o.PrjID)
	}
	o.OvsInstance = instance
	return nil
}

func (s *OvsNode) GetName() string {
	return s.Name
}

func (s *OvsNode) GetShortName() string {
	if s.ShortName == "" {
		return s.Name
	}
	return s.ShortName
}

func (s *OvsNode) GetType() string {
	return "ovs"
}

func (s *OvsNode) GetFullType() string {
	return "ovs"
}

func (o *OvsNode) IsRunning() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.Running
}

func (o *OvsNode) ExecCommand(
	ctx context.Context,
	cmd []string,
	in io.ReadCloser,
	out io.Writer,
	tty bool,
	ttyHeight uint,
	ttyWidth uint,
	resizeCh chan term.Winsize) error {
	if !o.IsRunning() {
		return errors.New("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	client, err := docker.NewDockerClient()
	if err != nil {
		return err
	}
	defer client.Close()

	return client.ExecTty(
		ctx,
		o.OvsInstance.containerId,
		cmd, in, out,
		tty, ttyHeight, ttyWidth,
		resizeCh)
}

func (o *OvsNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !o.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return nil, nil, -1, err
	}

	client, err := docker.NewDockerClient()
	if err != nil {
		return nil, nil, -1, err
	}
	defer client.Close()

	pid, err := client.Pid(context.Background(), o.OvsInstance.containerId)
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

func (o *OvsNode) GetConsoleCmd(shell bool) ([]string, error) {
	cmd := []string{"/bin/bash"}
	if !shell {
		cmd = []string{"/usr/bin/ovs-console.py", o.GetBridgeName()}
	}

	return cmd, nil
}

func (o *OvsNode) CopyFrom(srcPath, destPath string) error {
	return fmt.Errorf("CopyFrom action not supported for ovswitch node")
}

func (o *OvsNode) CopyTo(srcPath, destPath string) error {
	return fmt.Errorf("CopyTo action not supported for ovswitch node")
}

func (o *OvsNode) GetNetns() (netns.NsHandle, error) {
	if err := o.ensureInstance(); err != nil {
		return netns.None(), err
	}
	return o.OvsInstance.GetNetns()
}

func (o *OvsNode) GetBridgeName() string {
	return o.Name
}

func (o *OvsNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("%s.%d", o.GetBridgeName(), ifIndex)
}

func (o *OvsNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !o.IsRunning() {
		return fmt.Errorf("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	return o.OvsInstance.Capture(ctx, o.GetInterfaceName(ifIndex), out)
}

func (o *OvsNode) Start() error {
	if o.IsRunning() {
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	o.mu.RLock()
	interfaces := make([]string, 0, len(o.Interfaces))
	for ifName := range o.Interfaces {
		interfaces = append(interfaces, ifName)
	}
	o.mu.RUnlock()

	if err := o.OvsInstance.AddBridgeWithPorts(o.GetBridgeName(), interfaces); err != nil {
		_ = o.OvsInstance.DelBr(o.GetBridgeName())
		return err
	}

	o.mu.Lock()
	o.Running = true
	o.mu.Unlock()

	return nil
}

func (o *OvsNode) Stop() error {
	if !o.IsRunning() {
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	// Deleting the bridge removes all of its ports from the OVS database.
	// Keep the interface map so Start can re-add the ports on restart.
	if err := o.OvsInstance.DelBr(o.GetBridgeName()); err != nil {
		return err
	}

	o.mu.Lock()
	o.Running = false
	o.mu.Unlock()

	return nil
}

func (o *OvsNode) AttachInterface(ifName string, ifIndex int, configure bool) error {
	if o.IsRunning() {
		if err := o.ensureInstance(); err != nil {
			return err
		}
		if err := o.OvsInstance.AddPort(o.GetBridgeName(), ifName); err != nil {
			return err
		}
	}

	o.mu.Lock()
	o.Interfaces[ifName] = link.IFSTATE_UP
	o.mu.Unlock()

	return nil
}

func (o *OvsNode) DetachInterface(ifName string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.Running {
		if err := o.ensureInstance(); err != nil {
			return err
		}
		// The kernel veth is normally deleted first, so a missing OVS port is
		// expected. Keep the bookkeeping removal unconditional.
		if err := o.OvsInstance.DelPort(o.GetBridgeName(), ifName); err != nil {
			o.Logger.Warnf("unable to delete OVS port %s: %v", ifName, err)
		}
	}

	delete(o.Interfaces, ifName)
	return nil
}

func (n *OvsNode) ConfigureInterfaces() error {
	return nil
}

func (o *OvsNode) AttachMgntInterface(ifName string, ns netns.NsHandle, IPAddress string) error {
	return fmt.Errorf("unable to attach ovs switch to management network")
}

func (o *OvsNode) GetInterfacesState() map[string]link.IfState {
	o.mu.RLock()
	defer o.mu.RUnlock()

	ifStates := make(map[string]link.IfState, len(o.Interfaces))
	for ifName, state := range o.Interfaces {
		ifStates[ifName] = state
	}

	return ifStates
}

func (n *OvsNode) SetInterfaceState(ifIndex int, state link.IfState) error {
	ifName := n.GetInterfaceName(ifIndex)

	n.mu.RLock()
	current, found := n.Interfaces[ifName]
	n.mu.RUnlock()
	if !found {
		return fmt.Errorf("interface %s.%d not found", n.GetName(), ifIndex)
	}
	if state == current {
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
	n.Interfaces[ifName] = state
	n.mu.Unlock()

	return nil
}

func (o *OvsNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	configFilesData := make(map[string][]byte)

	filesDir := confDir
	if o.IsRunning() {
		if err := o.ensureInstance(); err != nil {
			return nil, err
		}
		// create temp directory for the project
		dir, err := os.MkdirTemp(options.ServerConfig.Workdir, "gonetem-config-sw"+o.Name+"-")
		if err != nil {
			return nil, fmt.Errorf("unable to create temp folder to save ovs config: %w", err)
		}
		defer os.RemoveAll(dir)

		err = o.OvsInstance.SaveConfig(o.Name, o.GetBridgeName(), dir, timeout)
		if err != nil {
			return nil, fmt.Errorf("unable to save ovs config in temp folder %s: %w", dir, err)
		}
		filesDir = dir
	}

	confpath := path.Join(filesDir, o.Name+".conf")
	if _, err := os.Stat(confpath); os.IsNotExist(err) {
		configFilesData["OVS"] = []byte{}
		return configFilesData, nil
	}

	data, err := os.ReadFile(confpath)
	if err != nil {
		return nil, fmt.Errorf("unable to read ovs config file '%s':\n\t%w", confpath, err)
	}

	configFilesData["OVS"] = data
	return configFilesData, nil
}

func (o *OvsNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	if !o.IsRunning() {
		o.Logger.Warn("LoadConfig: node not running")
		return []string{}, nil
	}
	if err := o.ensureInstance(); err != nil {
		return []string{}, err
	}

	return o.OvsInstance.LoadConfig(o.Name, o.GetBridgeName(), confPath, timeout)
}

func (o *OvsNode) Save(dstPath string, timeout int) error {
	if !o.IsRunning() {
		o.Logger.Warn("Save: node not running")
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	return o.OvsInstance.SaveConfig(o.Name, o.GetBridgeName(), dstPath, timeout)
}

func (o *OvsNode) Kill() error {
	return nil
}

func (o *OvsNode) Close() error {
	if o.OvsInstance != nil {
		return o.OvsInstance.DelBr(o.GetBridgeName())
	}
	return nil
}

func NewOvsNode(prjID, name, shortName string) (*OvsNode, error) {
	node := &OvsNode{
		PrjID:      prjID,
		Name:       name,
		ShortName:  shortName,
		Interfaces: make(map[string]link.IfState),
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    "ovs-" + name,
		}),
	}

	node.OvsInstance = GetOvsInstance(prjID)
	return node, nil
}
