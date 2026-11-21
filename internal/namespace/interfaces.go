package namespace

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

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

func (n *NamespaceNode) AttachMgntInterface(ifName string, ns netns.NsHandle, ipAddress string) error {
	n.mu.Lock()
	n.Interfaces[ifName] = link.IFSTATE_UP
	n.mu.Unlock()

	if err := link.IpAddressAdd(ifName, ns, ipAddress); err != nil {
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
