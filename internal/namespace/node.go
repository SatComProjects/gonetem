package namespace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
)

const netnsMountPath = "/run/netns"

// NamespaceNode is a node that only provides a Linux network namespace. It
// shares the host filesystem and process namespace, so it is intended for
// lightweight L2/L3 topologies that do not need a container.
type NamespaceNode struct {
	PrjID      string
	Name       string
	ShortName  string
	Interfaces map[string]link.IfState
	Running    bool

	nsName      string
	initialized bool
	mu          sync.RWMutex
	nsMu        sync.Mutex
	Logger      *logrus.Entry
}

func NewNamespaceNode(prjID, name, shortName string) *NamespaceNode {
	return &NamespaceNode{
		PrjID:      prjID,
		Name:       name,
		ShortName:  shortName,
		Interfaces: make(map[string]link.IfState),
		nsName:     fmt.Sprintf("%s%s.%s", options.NETEM_ID, prjID, name),
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    name,
		}),
	}
}

func (n *NamespaceNode) GetName() string {
	return n.Name
}

func (n *NamespaceNode) GetShortName() string {
	if n.ShortName == "" {
		return n.Name
	}
	return n.ShortName
}

func (n *NamespaceNode) GetType() string {
	return "namespace"
}

func (n *NamespaceNode) GetFullType() string {
	return "namespace"
}

func (n *NamespaceNode) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Running
}

func (n *NamespaceNode) netnsPath() string {
	return filepath.Join(netnsMountPath, n.nsName)
}

// ensureNetns creates the named network namespace on first use. On later
// calls it reuses the namespace as long as it still exists; Stop keeps the
// namespace alive so links and interface state survive a stop/start cycle.
func (n *NamespaceNode) ensureNetns() error {
	n.nsMu.Lock()
	defer n.nsMu.Unlock()

	if n.initialized {
		if ns, err := netns.GetFromName(n.nsName); err == nil {
			ns.Close()
			return nil
		}
	}

	// A namespace with the same name may be left over from a previous run.
	// Remove it before creating a fresh one.
	if _, err := os.Stat(n.netnsPath()); err == nil {
		if err := link.DeleteNetns(n.nsName); err != nil {
			return fmt.Errorf("unable to remove stale network namespace %s: %w", n.nsName, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("unable to inspect network namespace %s: %w", n.nsName, err)
	}

	ns, err := link.CreateNetns(n.nsName)
	if err != nil {
		return fmt.Errorf("unable to create network namespace %s: %w", n.nsName, err)
	}
	defer ns.Close()

	if err := link.SetInterfaceState("lo", ns, link.IFSTATE_UP); err != nil {
		_ = link.DeleteNetns(n.nsName)
		return fmt.Errorf("unable to set loopback up in network namespace %s: %w", n.nsName, err)
	}

	n.initialized = true
	return nil
}

func (n *NamespaceNode) GetNetns() (netns.NsHandle, error) {
	if err := n.ensureNetns(); err != nil {
		return netns.None(), err
	}

	ns, err := netns.GetFromName(n.nsName)
	if err != nil {
		return netns.None(), fmt.Errorf("unable to get network namespace %s: %w", n.nsName, err)
	}
	return ns, nil
}

func (n *NamespaceNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("eth%d", ifIndex)
}

func (n *NamespaceNode) AttachInterface(ifName string, ifIndex int, configure bool) error {
	n.mu.Lock()
	n.Interfaces[ifName] = link.IFSTATE_UP
	n.mu.Unlock()
	return nil
}

func (n *NamespaceNode) DetachInterface(ifName string) error {
	n.mu.Lock()
	delete(n.Interfaces, ifName)
	n.mu.Unlock()
	return nil
}

func (n *NamespaceNode) ConfigureInterfaces() error {
	return nil
}

func (n *NamespaceNode) AttachMgntInterface(ifName string, ns netns.NsHandle, IPAddress string) error {
	n.mu.Lock()
	n.Interfaces[ifName] = link.IFSTATE_UP
	n.mu.Unlock()

	if err := link.IpAddressAdd(ifName, ns, IPAddress); err != nil {
		n.mu.Lock()
		delete(n.Interfaces, ifName)
		n.mu.Unlock()
		return err
	}
	return nil
}

func (n *NamespaceNode) GetInterfacesState() map[string]link.IfState {
	n.mu.RLock()
	defer n.mu.RUnlock()

	states := make(map[string]link.IfState, len(n.Interfaces))
	for ifName, state := range n.Interfaces {
		states[ifName] = state
	}
	return states
}

func (n *NamespaceNode) SetInterfaceState(ifIndex int, state link.IfState) error {
	ifName := n.GetInterfaceName(ifIndex)

	n.mu.RLock()
	current, found := n.Interfaces[ifName]
	n.mu.RUnlock()
	if !found {
		return fmt.Errorf("interface %s.%d not found", n.GetName(), ifIndex)
	}
	if current == state {
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

func (n *NamespaceNode) Start() error {
	n.mu.RLock()
	running := n.Running
	n.mu.RUnlock()
	if running {
		return nil
	}

	if err := n.ensureNetns(); err != nil {
		return err
	}

	n.mu.Lock()
	n.Running = true
	n.mu.Unlock()
	n.Logger.Debug("namespace node started")
	return nil
}

// Stop only marks the node as stopped. The namespace and its interfaces are
// kept so that they can be reused when the node is started again.
func (n *NamespaceNode) Stop() error {
	n.mu.Lock()
	n.Running = false
	n.mu.Unlock()
	n.Logger.Debug("namespace node stopped")
	return nil
}

func (n *NamespaceNode) Kill() error {
	return n.Stop()
}

func (n *NamespaceNode) Close() error {
	n.nsMu.Lock()
	defer n.nsMu.Unlock()

	n.mu.Lock()
	n.Running = false
	n.Interfaces = make(map[string]link.IfState)
	n.mu.Unlock()

	if _, err := os.Stat(n.netnsPath()); err != nil {
		if os.IsNotExist(err) {
			n.initialized = false
			return nil
		}
		return fmt.Errorf("unable to inspect network namespace %s: %w", n.nsName, err)
	}

	if err := link.DeleteNetns(n.nsName); err != nil {
		return fmt.Errorf("unable to delete network namespace %s: %w", n.nsName, err)
	}
	n.initialized = false
	return nil
}

func (n *NamespaceNode) nsenterArgs(cmd []string) []string {
	args := []string{"--net=" + n.netnsPath(), "--"}
	return append(args, cmd...)
}

func (n *NamespaceNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !n.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}

	nsenterCmd := exec.Command("nsenter", n.nsenterArgs(cmd)...)
	var stdout, stderr bytes.Buffer
	nsenterCmd.Stdout = &stdout
	nsenterCmd.Stderr = &stderr

	err := nsenterCmd.Run()
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

func (n *NamespaceNode) ExecCommand(
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

	nsenterCmd := exec.CommandContext(ctx, "nsenter", n.nsenterArgs(cmd)...)
	if !tty {
		if in != nil {
			nsenterCmd.Stdin = in
		}
		nsenterCmd.Stdout = out
		nsenterCmd.Stderr = out
		return nsenterCmd.Run()
	}

	ptmx, err := pty.Start(nsenterCmd)
	if err != nil {
		return fmt.Errorf("unable to start command in namespace: %w", err)
	}
	defer ptmx.Close()

	if ttyHeight > 0 && ttyWidth > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{
			Rows: uint16(ttyHeight),
			Cols: uint16(ttyWidth),
		})
	}

	if in != nil {
		go func() {
			_, _ = io.Copy(ptmx, in)
		}()
	}

	if resizeCh != nil {
		go func() {
			for size := range resizeCh {
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: size.Height,
					Cols: size.Width,
				})
			}
		}()
	}

	_, copyErr := io.Copy(out, ptmx)
	if errors.Is(copyErr, syscall.EIO) {
		// Linux returns EIO on the master side once the child closes the PTY.
		copyErr = nil
	}
	waitErr := nsenterCmd.Wait()
	if waitErr != nil {
		return waitErr
	}
	return copyErr
}

func (n *NamespaceNode) GetConsoleCmd(shell bool) ([]string, error) {
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		shellPath = "/bin/sh"
	}
	return []string{shellPath}, nil
}

func (n *NamespaceNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	ifName := n.GetInterfaceName(ifIndex)
	captureCmd := exec.CommandContext(
		ctx, "nsenter",
		n.nsenterArgs([]string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", ifName})...,
	)
	captureCmd.Stdout = out
	var stderr bytes.Buffer
	captureCmd.Stderr = &stderr
	if err := captureCmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("%v: %s", err, message)
		}
		return err
	}
	return nil
}

func (n *NamespaceNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	return nil, nil
}

func (n *NamespaceNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	return make(map[string][]byte), nil
}

func (n *NamespaceNode) Save(dstPath string, timeout int) error {
	return nil
}

func (n *NamespaceNode) CopyFrom(srcPath, destPath string) error {
	return copyFile(srcPath, destPath)
}

func (n *NamespaceNode) CopyTo(srcPath, destPath string) error {
	return copyFile(srcPath, destPath)
}

func copyFile(srcPath, destPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("unable to stat %s: %w", srcPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srcPath)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("unable to open %s: %w", srcPath, err)
	}
	defer src.Close()

	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("unable to create %s: %w", destPath, err)
	}

	_, copyErr := io.Copy(dest, src)
	closeErr := dest.Close()
	if copyErr != nil {
		return fmt.Errorf("unable to copy %s to %s: %w", srcPath, destPath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("unable to close %s: %w", destPath, closeErr)
	}
	return nil
}
