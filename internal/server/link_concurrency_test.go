package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vishvananda/netns"
)

type fakeLinkNode struct {
	fakeStartNode
	mu       sync.Mutex
	attached map[string]bool
	detached []string
}

func newFakeLinkNode(name string) *fakeLinkNode {
	return &fakeLinkNode{
		fakeStartNode: fakeStartNode{name: name},
		attached:      make(map[string]bool),
	}
}

func (n *fakeLinkNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("eth%d", ifIndex)
}

func (n *fakeLinkNode) GetNetns() (netns.NsHandle, error) {
	return netns.Get()
}

func (n *fakeLinkNode) AttachInterface(ifName string, ifIndex int, configure bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attached[ifName] = true
	return nil
}

func (n *fakeLinkNode) DetachInterface(ifName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.attached, ifName)
	n.detached = append(n.detached, ifName)
	return nil
}

func newLinkTestManager(t *testing.T, nodes ...*fakeLinkNode) *NetemTopologyManager {
	t.Helper()

	manager := &NetemTopologyManager{
		path:    t.TempDir(),
		nodes:   make([]NetemNode, 0, len(nodes)),
		links:   make([]*NetemLink, 0),
		bridges: make([]*NetemBridge, 0),
	}
	for _, node := range nodes {
		manager.nodes = append(manager.nodes, NetemNode{
			Instance:        node,
			LaunchAtStartup: true,
			Config:          NodeConfig{Type: "docker.host", Launch: true},
		})
	}
	return manager
}

func addTestLink(t *testing.T, manager *NetemTopologyManager, peer1, peer2 string) {
	t.Helper()
	if err := manager.LinkAdd(LinkConfig{Peer1: peer1, Peer2: peer2}, false); err != nil {
		t.Fatalf("unable to add test link %s-%s: %v", peer1, peer2, err)
	}
}

func TestLinkAddRejectsConcurrentDuplicate(t *testing.T) {
	nodeA := newFakeLinkNode("A")
	nodeB := newFakeLinkNode("B")
	manager := newLinkTestManager(t, nodeA, nodeB)

	const workers = 32
	var (
		wg       sync.WaitGroup
		success  int32
		failures int32
		start    = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := manager.LinkAdd(LinkConfig{Peer1: "A.0", Peer2: "B.0"}, false)
			if err == nil {
				atomic.AddInt32(&success, 1)
			} else {
				atomic.AddInt32(&failures, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("successful concurrent adds = %d, want 1", success)
	}
	if failures != workers-1 {
		t.Fatalf("failed concurrent adds = %d, want %d", failures, workers-1)
	}
	if len(manager.links) != 1 {
		t.Fatalf("topology contains %d links, want 1", len(manager.links))
	}
}

func TestLinkDelConcurrentRemovesCorrectLinks(t *testing.T) {
	nodeA := newFakeLinkNode("A")
	nodeB := newFakeLinkNode("B")
	nodeC := newFakeLinkNode("C")
	nodeD := newFakeLinkNode("D")
	nodeE := newFakeLinkNode("E")
	nodeF := newFakeLinkNode("F")
	manager := newLinkTestManager(t, nodeA, nodeB, nodeC, nodeD, nodeE, nodeF)

	addTestLink(t, manager, "A.0", "B.0")
	addTestLink(t, manager, "C.0", "D.0")
	addTestLink(t, manager, "E.0", "F.0")

	var wg sync.WaitGroup
	for _, peers := range [][2]string{{"A.0", "B.0"}, {"E.0", "F.0"}} {
		peers := peers
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.LinkDel(LinkConfig{Peer1: peers[0], Peer2: peers[1]}, false); err != nil {
				t.Errorf("concurrent LinkDel %s-%s failed: %v", peers[0], peers[1], err)
			}
		}()
	}
	wg.Wait()

	if len(manager.links) != 1 {
		t.Fatalf("topology contains %d links, want 1", len(manager.links))
	}
	if _, _, err := manager.GetLink("C.0", "D.0"); err != nil {
		t.Fatalf("remaining link is not C-D: %v", err)
	}
	if _, _, err := manager.GetLink("A.0", "B.0"); err == nil {
		t.Fatal("deleted link A-B is still present")
	}
	if _, _, err := manager.GetLink("E.0", "F.0"); err == nil {
		t.Fatal("deleted link E-F is still present")
	}
}

func TestLinkDelRemovesEveryLinkWithoutIndexPanic(t *testing.T) {
	nodes := []*fakeLinkNode{
		newFakeLinkNode("A"), newFakeLinkNode("B"), newFakeLinkNode("C"),
	}
	manager := newLinkTestManager(t, nodes...)
	addTestLink(t, manager, "A.0", "B.0")
	addTestLink(t, manager, "B.0", "C.0")
	addTestLink(t, manager, "A.0", "C.0")

	for _, peers := range [][2]string{{"A.0", "B.0"}, {"B.0", "C.0"}, {"A.0", "C.0"}} {
		if err := manager.LinkDel(LinkConfig{Peer1: peers[0], Peer2: peers[1]}, false); err != nil {
			t.Fatalf("LinkDel %s-%s failed: %v", peers[0], peers[1], err)
		}
	}

	if len(manager.links) != 0 {
		t.Fatalf("remaining links = %d, want 0", len(manager.links))
	}
	for _, peers := range [][2]string{{"A.0", "B.0"}, {"B.0", "C.0"}, {"A.0", "C.0"}} {
		if _, _, err := manager.GetLink(peers[0], peers[1]); err == nil {
			t.Fatalf("link %s-%s is still present", peers[0], peers[1])
		}
	}
}

func TestSynchroniseTopologyConcurrentWithLinkChanges(t *testing.T) {
	manager := newLinkTestManager(t,
		newFakeLinkNode("A"), newFakeLinkNode("B"),
		newFakeLinkNode("C"), newFakeLinkNode("D"),
	)
	if err := manager.LinkAdd(LinkConfig{Peer1: "A.0", Peer2: "B.0"}, false); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := manager.SynchroniseTopology(); err != nil {
					t.Errorf("SynchroniseTopology failed: %v", err)
					return
				}
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = manager.LinkUpdate(LinkConfig{
					Peer1: "A.0", Peer2: "B.0",
					Peer1QoS: QoSConfig{Delay: i + j},
				}, false)
			}
		}(i)
	}
	wg.Wait()
}
