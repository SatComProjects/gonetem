package namespace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

const netnsMountPath = "/run/netns"

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
			_ = ns.Close()
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
