package namespace

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/utils"
	"github.com/vishvananda/netns"
)

func TestNamespaceNodeNetnsPath(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")

	wantName := "ntmabc.ns1"
	if node.nsName != wantName {
		t.Fatalf("nsName = %q, want %q", node.nsName, wantName)
	}
	wantPath := filepath.Join(netnsMountPath, wantName)
	if node.netnsPath() != wantPath {
		t.Fatalf("netnsPath() = %q, want %q", node.netnsPath(), wantPath)
	}
}

func TestNamespaceNodeCloseIsIdempotent(t *testing.T) {
	node := NewNamespaceNode(utils.RandString(6), utils.RandString(6), "n1")
	node.Running = true
	if err := node.AttachInterface("eth0", 0, false); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}

	if err := node.Close(); err != nil {
		t.Fatalf("first Close returned an error: %v", err)
	}
	if err := node.Close(); err != nil {
		t.Fatalf("second Close returned an error: %v", err)
	}

	if node.IsRunning() {
		t.Fatal("node is running after Close")
	}
	if states := node.GetInterfacesState(); len(states) != 0 {
		t.Fatalf("interfaces after Close = %v, want none", states)
	}
	if node.initialized {
		t.Fatal("node is still initialized after Close")
	}
}

func TestNamespaceNodeStopAndKillOnlyChangeRunningState(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true

	if err := node.Stop(); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is running after Stop")
	}

	node.Running = true
	if err := node.Kill(); err != nil {
		t.Fatalf("Kill returned an error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is running after Kill")
	}
}

func TestNamespaceNodeStartWhenAlreadyRunningSkipsNetnsCreation(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true

	if err := node.Start(); err != nil {
		t.Fatalf("Start on a running node returned an error: %v", err)
	}
	if node.initialized {
		t.Fatal("Start on a running node created a network namespace")
	}
}

func TestNamespaceNodeConfigureInterfacesIsNoop(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	if err := node.ConfigureInterfaces(); err != nil {
		t.Fatalf("ConfigureInterfaces returned an error: %v", err)
	}
}

func TestNamespaceNodeInterfaceStateIsACopy(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	if err := node.AttachInterface("eth0", 0, false); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}

	states := node.GetInterfacesState()
	states["eth0"] = link.IFSTATE_DOWN
	delete(states, "eth0")

	if got := node.GetInterfacesState()["eth0"]; got != link.IFSTATE_UP {
		t.Fatalf("node state was modified through the snapshot: %v", got)
	}
}

func TestNamespaceNodeAttachMgntInterfaceRemovesEntryOnError(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	ns, err := netns.Get()
	if err != nil {
		t.Fatalf("unable to open the current network namespace: %v", err)
	}
	defer ns.Close()

	ifName := "missing-" + utils.RandString(8)
	if err := node.AttachMgntInterface(ifName, ns, "10.0.0.1/24"); err == nil {
		t.Fatal("AttachMgntInterface accepted a missing interface")
	}
	if _, found := node.GetInterfacesState()[ifName]; found {
		t.Fatal("management interface stayed in bookkeeping after a failure")
	}
}

func TestNamespaceNodeInterfaceMapConcurrentAccess(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = node.AttachInterface("eth0", 0, false)
		}()
		go func() {
			defer wg.Done()
			_ = node.GetInterfacesState()
		}()
		go func() {
			defer wg.Done()
			_ = node.DetachInterface("eth0")
		}()
	}
	wg.Wait()
}
