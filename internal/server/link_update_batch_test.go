package server

import (
	"context"
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/proto"
	"github.com/vishvananda/netns"
)

type fakeQoSNode struct {
	fakeStartNode
}

func (n *fakeQoSNode) GetNetns() (netns.NsHandle, error) {
	return netns.Get()
}

func (n *fakeQoSNode) GetInterfaceName(int) string {
	return "eth0"
}

func newQoSTestLink(peer1, peer2 *fakeQoSNode) *NetemLink {
	return &NetemLink{
		Peer1: NetemLinkPeer{Node: peer1, IfIndex: 0},
		Peer2: NetemLinkPeer{Node: peer2, IfIndex: 0},
	}
}

func TestLinkUpdateManyGroupsQoSByNode(t *testing.T) {
	nodeA := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "A"}}
	nodeB := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "B"}}
	nodeC := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "C"}}
	linkAB := newQoSTestLink(nodeA, nodeB)
	linkAC := newQoSTestLink(nodeA, nodeC)

	manager := &NetemTopologyManager{
		links:   []*NetemLink{linkAB, linkAC},
		running: true,
	}

	var (
		mu       sync.Mutex
		calls    int
		opsTotal int
	)
	manager.qosBatchApplier = func(_ netns.NsHandle, ops []link.QoSOperation) ([]link.QoSOperationResult, error) {
		mu.Lock()
		calls++
		opsTotal += len(ops)
		mu.Unlock()

		results := make([]link.QoSOperationResult, len(ops))
		for idx := range results {
			results[idx].HasTbf = true
			results[idx].HasNetem = true
		}
		return results, nil
	}

	outcomes, err := manager.LinkUpdateMany([]LinkConfig{
		{Peer1: "A.0", Peer2: "B.0", Peer1QoS: QoSConfig{Delay: 10}, Peer2QoS: QoSConfig{Delay: 10}},
		{Peer1: "A.0", Peer2: "C.0", Peer1QoS: QoSConfig{Delay: 20}, Peer2QoS: QoSConfig{Delay: 20}},
	}, false)
	if err != nil {
		t.Fatalf("LinkUpdateMany returned an error: %v", err)
	}
	if len(outcomes) != 2 || outcomes[0].Err != nil || outcomes[1].Err != nil {
		t.Fatalf("unexpected outcomes: %#v", outcomes)
	}
	if calls != 3 {
		t.Fatalf("QoS batch called %d times, want one per node (3)", calls)
	}
	if opsTotal != 4 {
		t.Fatalf("QoS operations = %d, want 4", opsTotal)
	}
	if !linkAB.HasPeer1Tbf || !linkAB.HasPeer1Netem || !linkAB.HasPeer2Tbf || !linkAB.HasPeer2Netem {
		t.Fatalf("link AB qdisc state not updated: %#v", linkAB)
	}
	if !linkAC.HasPeer1Tbf || !linkAC.HasPeer2Netem {
		t.Fatalf("link AC qdisc state not updated: %#v", linkAC)
	}
}

func TestLinkUpdateManyStoppedTopologyUpdatesConfig(t *testing.T) {
	nodeA := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "A"}}
	nodeB := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "B"}}
	linkAB := newQoSTestLink(nodeA, nodeB)
	manager := &NetemTopologyManager{links: []*NetemLink{linkAB}}

	outcomes, err := manager.LinkUpdateMany([]LinkConfig{
		{Peer1: "A.0", Peer2: "B.0", Peer1QoS: QoSConfig{Delay: 10}, Peer2QoS: QoSConfig{Delay: 10}},
	}, false)
	if err != nil {
		t.Fatalf("LinkUpdateMany returned an error: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Err != nil {
		t.Fatalf("unexpected outcomes: %#v", outcomes)
	}
	if got := linkAB.Config.GetPeer1QoS().Delay; got != 10 {
		t.Fatalf("config delay = %d, want 10", got)
	}
}

func TestLinkUpdateManyIsolatesPerLinkErrors(t *testing.T) {
	nodeA := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "A"}}
	nodeB := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "B"}}
	linkAB := newQoSTestLink(nodeA, nodeB)
	manager := &NetemTopologyManager{links: []*NetemLink{linkAB}}

	outcomes, err := manager.LinkUpdateMany([]LinkConfig{
		{Peer1: "A.0", Peer2: "B.0", Peer1QoS: QoSConfig{Delay: 10}, Peer2QoS: QoSConfig{Delay: 10}},
		{Peer1: "A.0", Peer2: "B.0", Peer1QoS: QoSConfig{Delay: 20}, Peer2QoS: QoSConfig{Delay: 20}},
		{Peer1: "X.0", Peer2: "Y.0"},
		{Peer1: "A.0", Peer2: "B.0", Peer1QoS: QoSConfig{Delay: -1}},
	}, false)
	if err != nil {
		t.Fatalf("LinkUpdateMany returned an error: %v", err)
	}
	if len(outcomes) != 4 {
		t.Fatalf("outcomes = %d, want 4", len(outcomes))
	}
	if outcomes[0].Err != nil {
		t.Fatalf("valid link failed: %v", outcomes[0].Err)
	}
	if outcomes[1].Err == nil {
		t.Fatal("duplicate link was accepted")
	}
	if outcomes[2].Err == nil {
		t.Fatal("missing link was accepted")
	}
	if outcomes[3].Err == nil {
		t.Fatal("invalid QoS was accepted")
	}
}

func TestLinkUpdateManyHandlerReportsPerLinkError(t *testing.T) {
	nodeA := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "A"}}
	nodeB := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "B"}}
	manager := &NetemTopologyManager{links: []*NetemLink{newQoSTestLink(nodeA, nodeB)}}

	prjID := "linkupdatetesterr"
	projectsMu.Lock()
	openProjects[prjID] = &NetemProject{Id: prjID, Name: prjID, Topology: manager}
	projectsMu.Unlock()
	t.Cleanup(func() {
		projectsMu.Lock()
		delete(openProjects, prjID)
		projectsMu.Unlock()
	})

	response, err := (&netemServer{}).LinkUpdateMany(context.Background(), &proto.LinkUpdateManyRequest{
		PrjId: prjID,
		Links: []*proto.LinkConfig{{
			Peer1: "X.0",
			Peer2: "Y.0",
		}},
	})
	if err != nil {
		t.Fatalf("LinkUpdateMany returned an error: %v", err)
	}
	if response.GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatal("top-level batch status should be OK")
	}
	if response.GetUpdated() != 0 {
		t.Fatalf("updated = %d, want 0", response.GetUpdated())
	}
	if len(response.GetResults()) != 1 || response.GetResults()[0].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatalf("unexpected results: %#v", response.GetResults())
	}
}

func TestLinkUpdateManyHandlerReportsUpdatedCount(t *testing.T) {
	nodeA := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "A"}}
	nodeB := &fakeQoSNode{fakeStartNode: fakeStartNode{name: "B"}}
	manager := &NetemTopologyManager{links: []*NetemLink{newQoSTestLink(nodeA, nodeB)}}

	prjID := "linkupdatetest"
	projectsMu.Lock()
	openProjects[prjID] = &NetemProject{Id: prjID, Name: prjID, Topology: manager}
	projectsMu.Unlock()
	t.Cleanup(func() {
		projectsMu.Lock()
		delete(openProjects, prjID)
		projectsMu.Unlock()
	})

	response, err := (&netemServer{}).LinkUpdateMany(context.Background(), &proto.LinkUpdateManyRequest{
		PrjId: prjID,
		Links: []*proto.LinkConfig{{
			Peer1: "A.0",
			Peer2: "B.0",
			Peer1Qos: &proto.LinkConfig_QoSConfig{
				Delay: 10,
			},
			Peer2Qos: &proto.LinkConfig_QoSConfig{
				Delay: 10,
			},
		}},
	})
	if err != nil {
		t.Fatalf("LinkUpdateMany returned an error: %v", err)
	}
	if response.GetUpdated() != 1 {
		t.Fatalf("updated = %d, want 1", response.GetUpdated())
	}
	if len(response.GetResults()) != 1 || response.GetResults()[0].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("unexpected results: %#v", response.GetResults())
	}
}
