package ovs

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
)

func newRunningInstance(client *fakeContainerAPI) *OvsProjectInstance {
	instance := newTestInstance(client)
	instance.state = started
	return instance
}

func TestOvsNode_StartStopMaintainsBridgeAndRunningState(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.OvsInstance = newRunningInstance(client)
	node.Interfaces["sw1.0"] = link.IFSTATE_UP
	node.Interfaces["sw1.1"] = link.IFSTATE_UP

	if err := node.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	if !node.IsRunning() {
		t.Fatal("node is not running after Start")
	}
	if instance := node.OvsInstance; instance.findBr("sw1") != 0 {
		t.Fatal("Start did not record the bridge on the instance")
	}

	calls := client.execCalls()
	if len(calls) != 1 {
		t.Fatalf("Start issued %d Docker calls, want 1", len(calls))
	}
	command := strings.Join(calls[0], " ")
	for _, want := range []string{
		"ovs-vsctl --may-exist add-br sw1",
		"add-port sw1 sw1.0",
		"add-port sw1 sw1.1",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("Start command %q does not contain %q", command, want)
		}
	}

	if err := node.Start(); err != nil {
		t.Fatalf("second Start returned an error: %v", err)
	}
	if calls := client.execCalls(); len(calls) != 1 {
		t.Fatalf("second Start issued Docker calls: %#v", calls)
	}

	if err := node.Stop(); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is running after Stop")
	}
	if instance := node.OvsInstance; instance.findBr("sw1") != -1 {
		t.Fatal("Stop did not remove the bridge from the instance")
	}
	last := client.execCalls()[1]
	if !reflect.DeepEqual(last, []string{"ovs-vsctl", "del-br", "sw1"}) {
		t.Fatalf("unexpected del-br command: %#v", last)
	}

	if err := node.Stop(); err != nil {
		t.Fatalf("second Stop returned an error: %v", err)
	}
	if calls := client.execCalls(); len(calls) != 2 {
		t.Fatalf("second Stop issued Docker calls: %#v", calls)
	}
}

func TestOvsNode_StartFailureLeavesNodeStopped(t *testing.T) {
	execErr := errors.New("add-br failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	node := newTestNode()
	node.OvsInstance = newRunningInstance(client)
	node.Interfaces["sw1.0"] = link.IFSTATE_UP

	if err := node.Start(); !errors.Is(err, execErr) {
		t.Fatalf("Start error = %v, want %v", err, execErr)
	}
	if node.IsRunning() {
		t.Fatal("node is running after a failed Start")
	}
	if instance := node.OvsInstance; instance.findBr("sw1") != -1 {
		t.Fatal("failed Start recorded the bridge")
	}
}

func TestOvsNode_AttachDetachInterfaceUsesInstance(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newRunningInstance(client)

	if err := node.AttachInterface("sw1.0", 0, false); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}
	if state, ok := node.GetInterfacesState()["sw1.0"]; !ok || state != link.IFSTATE_UP {
		t.Fatalf("interface not recorded as up: %#v", node.GetInterfacesState())
	}
	wantAdd := []string{"ovs-vsctl", "add-port", "sw1", "sw1.0"}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{wantAdd}) {
		t.Fatalf("unexpected add-port calls: %#v", calls)
	}

	if err := node.DetachInterface("sw1.0"); err != nil {
		t.Fatalf("DetachInterface returned an error: %v", err)
	}
	if _, ok := node.GetInterfacesState()["sw1.0"]; ok {
		t.Fatal("interface was not removed from bookkeeping")
	}
	wantDel := []string{"ovs-vsctl", "del-port", "sw1", "sw1.0"}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{wantAdd, wantDel}) {
		t.Fatalf("unexpected del-port calls: %#v", calls)
	}
}

func TestOvsNode_DetachInterfaceKeepsBookkeepingOnPortError(t *testing.T) {
	client := newFakeContainerAPI()
	client.execErr = errors.New("port missing")
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newRunningInstance(client)
	node.Interfaces["sw1.0"] = link.IFSTATE_UP

	if err := node.DetachInterface("sw1.0"); err != nil {
		t.Fatalf("DetachInterface returned an error: %v", err)
	}
	if _, ok := node.GetInterfacesState()["sw1.0"]; ok {
		t.Fatal("interface was not removed from bookkeeping")
	}
}

func TestOvsNode_SetInterfaceStateUnknownInterface(t *testing.T) {
	node := newTestNode()

	if err := node.SetInterfaceState(0, link.IFSTATE_UP); err == nil {
		t.Fatal("SetInterfaceState did not reject an unknown interface")
	}
}

func TestOvsNode_SetInterfaceStateUnchanged(t *testing.T) {
	node := newTestNode()
	node.Interfaces["sw1.0"] = link.IFSTATE_UP

	if err := node.SetInterfaceState(0, link.IFSTATE_UP); err != nil {
		t.Fatalf("SetInterfaceState returned an error: %v", err)
	}
}

func TestOvsNode_ConcurrentStartStop(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.OvsInstance = newRunningInstance(client)
	node.Interfaces["sw1.0"] = link.IFSTATE_UP

	const count = 20
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := node.Start(); err != nil {
				t.Errorf("Start returned an error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := node.Stop(); err != nil {
				t.Errorf("Stop returned an error: %v", err)
			}
		}()
	}
	wg.Wait()

	if states := node.GetInterfacesState(); len(states) != 1 {
		t.Fatalf("node lost interface bookkeeping while toggling: %#v", states)
	}
}

func TestOvsNode_CloseDeletesBridgeWhenInstancePresent(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.OvsInstance = newRunningInstance(client)
	node.OvsInstance.bridges = []string{"sw1"}

	if err := node.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if got := node.OvsInstance.findBr("sw1"); got != -1 {
		t.Fatalf("Close left bridge recorded at index %d", got)
	}
}
