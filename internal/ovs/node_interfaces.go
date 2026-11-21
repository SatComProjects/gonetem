package ovs

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/link"
)

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
