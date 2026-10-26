package ovs

import (
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/utils"
)

func TestNewOvsNodeWithoutInstance(t *testing.T) {
	node, err := NewOvsNode(utils.RandString(6), "sw1", "sw1")
	if err != nil {
		t.Fatalf("NewOvsNode returned an error: %v", err)
	}
	if node.OvsInstance != nil {
		t.Fatal("new lazy OVS node unexpectedly has an instance")
	}
	if err := node.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
}

func TestOvsNode_DetachInterface(t *testing.T) {
	node := &OvsNode{
		Interfaces: map[string]link.IfState{"sw1.0": link.IFSTATE_UP},
		Running:    false,
	}

	if err := node.DetachInterface("sw1.0"); err != nil {
		t.Fatalf("DetachInterface returned an error: %v", err)
	}
	if _, ok := node.Interfaces["sw1.0"]; ok {
		t.Fatal("interface was not removed from bookkeeping")
	}
}

func TestOvsNodeGetInterfacesStateKeepsFullName(t *testing.T) {
	node := &OvsNode{Interfaces: map[string]link.IfState{"sw1.0": link.IFSTATE_UP}}

	states := node.GetInterfacesState()
	state, ok := states["sw1.0"]
	if !ok {
		t.Fatalf("full interface name missing from state map: %#v", states)
	}
	if state != link.IFSTATE_UP {
		t.Fatalf("unexpected state: %v", state)
	}
	if _, ok := states["0"]; ok {
		t.Fatalf("stripped interface name leaked into state map: %#v", states)
	}
}

func TestOvsNode_InterfaceMapConcurrentAccess(t *testing.T) {
	node := &OvsNode{Interfaces: make(map[string]link.IfState)}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = node.AttachInterface("sw1.0", 0, false)
		}()
		go func() {
			defer wg.Done()
			_ = node.GetInterfacesState()
			_ = node.IsRunning()
		}()
	}
	wg.Wait()
}

func TestOvsNode_StartStop(t *testing.T) {
	options.InitServerConfig()
	prjID := utils.RandString(3)
	name := utils.RandString(4)

	instance, err := NewOvsInstance(prjID)
	if err != nil {
		t.Errorf("Unable to create ovs instance: %v", err)
		return
	}
	defer instance.Close()

	if err := instance.Start(); err != nil {
		t.Errorf("Unable to start ovs instance: %v", err)
		return
	}

	node, err := NewOvsNode(prjID, name, name)
	if err != nil {
		t.Errorf("Unable to create ovs node: %v", err)
		return
	}
	defer node.Close()

	ports := []string{"p1", "p2"}
	for _, port := range ports {
		if err := instance.Exec([]string{"ip", "link", "add", port, "type", "dummy"}); err != nil {
			t.Fatalf("Unable to create dummy interface %s: %v", port, err)
		}
		if err := node.AttachInterface(port, 0, false); err != nil {
			t.Fatalf("Unable to attach interface %s: %v", port, err)
		}
	}

	// Start the node
	if err := node.Start(); err != nil {
		t.Errorf("Unable to start ovs node: %v", err)
	}
	for _, port := range ports {
		if err := instance.Exec([]string{"ovs-vsctl", "port-to-br", port}); err != nil {
			t.Errorf("Port %s was not added to the bridge: %v", port, err)
		}
	}

	// Stop the node
	if err := node.Stop(); err != nil {
		t.Errorf("Unable to stop ovs node: %v", err)
	}
}

func TestOvsNode_AttachLink(t *testing.T) {
	options.InitServerConfig()
	prjID := utils.RandString(3)

	instance, err := NewOvsInstance(prjID)
	if err != nil {
		t.Errorf("Unable to create ovs instance: %v", err)
		return
	}
	defer instance.Close()

	if err := instance.Start(); err != nil {
		t.Errorf("Unable to start ovs instance: %v", err)
		return
	}

	node1, err := NewOvsNode(prjID, utils.RandString(4), utils.RandString(4))
	if err != nil {
		t.Errorf("Unable to create ovs node: %v", err)
		return
	}
	defer node1.Close()

	node2, err := NewOvsNode(prjID, utils.RandString(4), utils.RandString(4))
	if err != nil {
		t.Errorf("Unable to create ovs node: %v", err)
		return
	}
	defer node2.Close()

	for _, node := range []*OvsNode{node1, node2} {
		if err := node.Start(); err != nil {
			t.Fatalf("Unable to start ovs node %s: %v", node.GetName(), err)
		}
	}

	// create link
	node1Netns, err := node1.GetNetns()
	if err != nil {
		t.Fatalf("Unable to get netns for node 1: %v", err)
	}
	defer node1Netns.Close()

	node2Netns, err := node2.GetNetns()
	if err != nil {
		t.Fatalf("Unable to get netns for node 2: %v", err)
	}
	defer node2Netns.Close()

	_, err = link.CreateVethLink(
		node1.GetInterfaceName(0), node1Netns,
		node2.GetInterfaceName(0), node2Netns,
	)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
}
