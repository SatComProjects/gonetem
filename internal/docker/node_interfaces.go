package docker

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

func (n *DockerNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("eth%d", ifIndex)
}

// prepareInterfaces applies interface-level settings directly in the node
// network namespace. It avoids one docker exec per attached interface.
func (n *DockerNode) prepareInterfaces(ifNames []string, namespace netns.NsHandle) {
	if err := link.PrepareInterfaces(ifNames, namespace, !n.Config.Options.Tso, n.Mpls); err != nil {
		n.Logger.Warnf("Unable to prepare interfaces %v: %v", ifNames, err)
	}
}

func (n *DockerNode) AttachInterface(ifName string, ifIndex int, configure bool) error {
	n.interfaces.attach(ifName, &DockerInterface{
		Configured: false,
		State:      link.IFSTATE_UP,
	})

	if configure && n.IsRunning() {
		namespace, err := n.GetNetns()
		if err != nil {
			return err
		}
		defer namespace.Close()

		n.prepareInterfaces([]string{ifName}, namespace)

		n.interfaces.markConfigured(ifName)
	}

	return nil
}

func (n *DockerNode) DetachInterface(ifName string) error {
	n.interfaces.detach(ifName)
	return nil
}

func (n *DockerNode) ConfigureInterfaces() error {
	if !n.IsRunning() {
		return nil
	}

	pending := n.interfaces.pending()
	if len(pending) > 0 {
		namespace, err := n.GetNetns()
		if err != nil {
			return err
		}
		defer namespace.Close()

		n.prepareInterfaces(pending, namespace)

		n.interfaces.markConfiguredAll(pending)
	}

	return nil
}

func (n *DockerNode) AttachMgntInterface(ifName string, ns netns.NsHandle, ipAddress string) error {
	n.interfaces.attach(ifName, &DockerInterface{
		Configured: true,
		State:      link.IFSTATE_UP,
	})

	// Set IP address
	if err := link.IpAddressAdd(ifName, ns, ipAddress); err != nil {
		return err
	}

	return nil
}

func (n *DockerNode) GetInterfacesState() map[string]link.IfState {
	return n.interfaces.states()
}

func (n *DockerNode) SetInterfaceState(ifIndex int, state link.IfState) error {
	ifName := n.GetInterfaceName(ifIndex)

	current, found := n.interfaces.state(ifName)
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

	n.interfaces.setState(ifName, state)

	return nil
}
