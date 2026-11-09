package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/proto"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

func TestLinkSyncManyRunsDeletesAddsUpdatesAndPreservesOrder(t *testing.T) {
	nodes := []*fakeLinkNode{
		newFakeLinkNode("A"), newFakeLinkNode("B"),
		newFakeLinkNode("C"), newFakeLinkNode("D"),
	}
	manager := newLinkTestManager(t, nodes...)
	addTestLink(t, manager, "A.0", "B.0")
	addTestLink(t, manager, "C.0", "D.0")

	operations := []LinkSyncOperation{
		{Action: LinkSyncAdd, Config: LinkConfig{Peer1: "A.0", Peer2: "C.0"}},
		{Action: LinkSyncDel, Config: LinkConfig{Peer1: "A.0", Peer2: "B.0"}},
		{Action: LinkSyncUpdate, Config: LinkConfig{
			Peer1: "C.0", Peer2: "D.0", Delay: 10, Peer2QoS: QoSConfig{Delay: 10},
		}},
		{Action: LinkSyncDel, Config: LinkConfig{Peer1: "X.0", Peer2: "Y.0"}},
	}

	outcomes, err := manager.LinkSyncMany(operations, true)
	if err != nil {
		t.Fatalf("LinkSyncMany returned an error: %v", err)
	}
	if len(outcomes) != len(operations) {
		t.Fatalf("outcomes = %d, want %d", len(outcomes), len(operations))
	}
	for idx, outcome := range outcomes {
		if outcome.Action != operations[idx].Action {
			t.Errorf("outcome %d action = %v, want %v", idx, outcome.Action, operations[idx].Action)
		}
		if outcome.Peer1 != operations[idx].Config.Peer1 || outcome.Peer2 != operations[idx].Config.Peer2 {
			t.Errorf("outcome %d peers = %s-%s, want %s-%s", idx,
				outcome.Peer1, outcome.Peer2,
				operations[idx].Config.Peer1, operations[idx].Config.Peer2)
		}
	}
	if outcomes[0].Err != nil {
		t.Fatalf("add failed: %v", outcomes[0].Err)
	}
	if outcomes[1].Err != nil {
		t.Fatalf("delete failed: %v", outcomes[1].Err)
	}
	if outcomes[2].Err != nil {
		t.Fatalf("update failed: %v", outcomes[2].Err)
	}
	if outcomes[3].Err == nil {
		t.Fatal("missing-link delete unexpectedly succeeded")
	}

	if _, _, err := manager.GetLink("A.0", "C.0"); err != nil {
		t.Fatalf("added link is missing: %v", err)
	}
	if _, _, err := manager.GetLink("A.0", "B.0"); err == nil {
		t.Fatal("deleted link is still present")
	}
	updated, _, err := manager.GetLink("C.0", "D.0")
	if err != nil {
		t.Fatalf("updated link is missing: %v", err)
	}
	if got := updated.Config.GetPeer1QoS().Delay; got != 10 {
		t.Fatalf("updated delay = %d, want 10", got)
	}

	data, err := os.ReadFile(filepath.Join(manager.path, networkFilename))
	if err != nil {
		t.Fatalf("topology file was not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("topology file is empty")
	}
}

func TestLinkSyncManyWithoutSyncDoesNotWriteTopology(t *testing.T) {
	manager := newLinkTestManager(t, newFakeLinkNode("A"), newFakeLinkNode("B"))

	_, err := manager.LinkSyncMany([]LinkSyncOperation{
		{Action: LinkSyncAdd, Config: LinkConfig{Peer1: "A.0", Peer2: "B.0"}},
	}, false)
	if err != nil {
		t.Fatalf("LinkSyncMany returned an error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(manager.path, networkFilename)); !os.IsNotExist(err) {
		t.Fatalf("topology file was written without sync: %v", err)
	}
}

func TestLinkSyncManyRejectsDuplicateOperation(t *testing.T) {
	manager := newLinkTestManager(t, newFakeLinkNode("A"), newFakeLinkNode("B"))

	outcomes, err := manager.LinkSyncMany([]LinkSyncOperation{
		{Action: LinkSyncUpdate, Config: LinkConfig{Peer1: "A.0", Peer2: "B.0", Delay: 10}},
		{Action: LinkSyncUpdate, Config: LinkConfig{Peer1: "A.0", Peer2: "B.0", Delay: 20}},
	}, false)
	if err != nil {
		t.Fatalf("LinkSyncMany returned an error: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	if outcomes[0].Err == nil {
		t.Fatal("first update unexpectedly succeeded on a missing link")
	}
	if outcomes[1].Err == nil {
		t.Fatal("duplicate update unexpectedly succeeded")
	}
}

type fakeKernelNode struct {
	*fakeLinkNode
	namespace netns.NsHandle
}

func (n *fakeKernelNode) GetNetns() (netns.NsHandle, error) {
	fd, err := unix.Dup(int(n.namespace))
	if err != nil {
		return netns.None(), err
	}
	return netns.NsHandle(fd), nil
}

func newKernelTestManager(t *testing.T) (*NetemTopologyManager, netns.NsHandle, netns.NsHandle) {
	t.Helper()

	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}

	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}

	nsA, err := netns.New()
	if err != nil {
		orig.Close()
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	nsB, err := netns.New()
	if err != nil {
		nsA.Close()
		orig.Close()
		runtime.UnlockOSThread()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = netns.Set(orig)
		orig.Close()
		nsA.Close()
		nsB.Close()
		runtime.UnlockOSThread()
	})

	nodeA := &fakeKernelNode{fakeLinkNode: newFakeLinkNode("A"), namespace: nsA}
	nodeB := &fakeKernelNode{fakeLinkNode: newFakeLinkNode("B"), namespace: nsB}
	manager := newLinkTestManager(t, nodeA.fakeLinkNode, nodeB.fakeLinkNode)
	manager.nodes[0].Instance = nodeA
	manager.nodes[1].Instance = nodeB
	manager.running = true
	return manager, nsA, nsB
}

func TestLinkSyncManyConcurrentKernelLinks(t *testing.T) {
	manager, nsA, nsB := newKernelTestManager(t)

	const linkCount = 12
	var wg sync.WaitGroup
	errs := make(chan error, linkCount)
	for i := 0; i < linkCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.LinkAdd(LinkConfig{
				Peer1: fmt.Sprintf("A.%d", i),
				Peer2: fmt.Sprintf("B.%d", i),
			}, false); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent LinkAdd failed: %v", err)
	}
	if len(manager.links) != linkCount {
		t.Fatalf("links = %d, want %d", len(manager.links), linkCount)
	}
	for i := 0; i < linkCount; i++ {
		if !link.IsLinkExist(fmt.Sprintf("eth%d", i), nsA) {
			t.Fatalf("eth%d is missing from node A", i)
		}
		if !link.IsLinkExist(fmt.Sprintf("eth%d", i), nsB) {
			t.Fatalf("eth%d is missing from node B", i)
		}
	}

	for i := 0; i < linkCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.LinkDel(LinkConfig{
				Peer1: fmt.Sprintf("A.%d", i),
				Peer2: fmt.Sprintf("B.%d", i),
			}, false); err != nil {
				t.Errorf("concurrent LinkDel %d failed: %v", i, err)
			}
		}()
	}
	wg.Wait()

	if len(manager.links) != 0 {
		t.Fatalf("links after delete = %d, want 0", len(manager.links))
	}
	for i := 0; i < linkCount; i++ {
		if link.IsLinkExist(fmt.Sprintf("eth%d", i), nsA) {
			t.Fatalf("eth%d still exists on node A", i)
		}
	}
}

func TestLinkSyncManyKernelBatch(t *testing.T) {
	manager, nsA, nsB := newKernelTestManager(t)

	operations := make([]LinkSyncOperation, 0, 8)
	for i := 0; i < 4; i++ {
		operations = append(operations, LinkSyncOperation{
			Action: LinkSyncAdd,
			Config: LinkConfig{Peer1: fmt.Sprintf("A.%d", i), Peer2: fmt.Sprintf("B.%d", i)},
		})
	}
	outcomes, err := manager.LinkSyncMany(operations, false)
	if err != nil {
		t.Fatalf("LinkSyncMany add batch failed: %v", err)
	}
	for idx, outcome := range outcomes {
		if outcome.Err != nil {
			t.Fatalf("add %d failed: %v", idx, outcome.Err)
		}
	}

	deletes := make([]LinkSyncOperation, 0, 4)
	for i := 0; i < 4; i++ {
		deletes = append(deletes, LinkSyncOperation{
			Action: LinkSyncDel,
			Config: LinkConfig{Peer1: fmt.Sprintf("A.%d", i), Peer2: fmt.Sprintf("B.%d", i)},
		})
	}
	outcomes, err = manager.LinkSyncMany(deletes, false)
	if err != nil {
		t.Fatalf("LinkSyncMany delete batch failed: %v", err)
	}
	for idx, outcome := range outcomes {
		if outcome.Err != nil {
			t.Fatalf("delete %d failed: %v", idx, outcome.Err)
		}
	}

	if len(manager.links) != 0 {
		t.Fatalf("links after batch delete = %d, want 0", len(manager.links))
	}
	if link.IsLinkExist("eth0", nsA) || link.IsLinkExist("eth0", nsB) {
		t.Fatal("eth0 still exists after batch delete")
	}
}

func TestLinkSyncManyHandlerReportsCountsAndOrder(t *testing.T) {
	manager := newLinkTestManager(t,
		newFakeLinkNode("A"), newFakeLinkNode("B"), newFakeLinkNode("C"),
	)
	addTestLink(t, manager, "A.0", "B.0")

	prjID := "linksyncmanyhandler"
	projectsMu.Lock()
	openProjects[prjID] = &NetemProject{Id: prjID, Name: prjID, Topology: manager}
	projectsMu.Unlock()
	t.Cleanup(func() {
		projectsMu.Lock()
		delete(openProjects, prjID)
		projectsMu.Unlock()
	})

	response, err := (&netemServer{}).LinkSyncMany(context.Background(), &proto.LinkSyncManyRequest{
		PrjId: prjID,
		Operations: []*proto.LinkSyncOperation{
			{Action: proto.LinkSyncAction_LINK_SYNC_DEL, Link: &proto.LinkConfig{Peer1: "A.0", Peer2: "B.0"}},
			{Action: proto.LinkSyncAction_LINK_SYNC_ADD, Link: &proto.LinkConfig{Peer1: "A.0", Peer2: "C.0"}},
			{Action: proto.LinkSyncAction_LINK_SYNC_UPDATE, Link: &proto.LinkConfig{
				Peer1: "A.0", Peer2: "C.0", Peer1Qos: &proto.LinkConfig_QoSConfig{Delay: 10},
			}},
			{Action: proto.LinkSyncAction_LINK_SYNC_DEL, Link: &proto.LinkConfig{Peer1: "X.0", Peer2: "Y.0"}},
		},
	})
	if err != nil {
		t.Fatalf("LinkSyncMany returned an error: %v", err)
	}
	if response.GetAdded() != 1 || response.GetDeleted() != 1 || response.GetUpdated() != 1 {
		t.Fatalf("counts = added %d, deleted %d, updated %d; want 1/1/1",
			response.GetAdded(), response.GetDeleted(), response.GetUpdated())
	}
	results := response.GetResults()
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4", len(results))
	}
	wantActions := []proto.LinkSyncAction{
		proto.LinkSyncAction_LINK_SYNC_DEL,
		proto.LinkSyncAction_LINK_SYNC_ADD,
		proto.LinkSyncAction_LINK_SYNC_UPDATE,
		proto.LinkSyncAction_LINK_SYNC_DEL,
	}
	for idx, result := range results {
		if result.GetAction() != wantActions[idx] {
			t.Errorf("result %d action = %v, want %v", idx, result.GetAction(), wantActions[idx])
		}
	}
	if results[0].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Errorf("delete failed: %s", results[0].GetStatus().GetError())
	}
	if results[1].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Errorf("add failed: %s", results[1].GetStatus().GetError())
	}
	if results[2].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Errorf("update failed: %s", results[2].GetStatus().GetError())
	}
	if results[3].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Error("missing-link delete should report an error")
	}
}
