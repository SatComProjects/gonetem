package server

import (
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

func TestLinkUpdateUsesInjectedQoSApplier(t *testing.T) {
	nodeA := newFakeLinkNode("A")
	nodeB := newFakeLinkNode("B")
	manager := newLinkTestManager(t, nodeA, nodeB)
	addTestLink(t, manager, "A.0", "B.0")
	manager.running = true

	calls := 0
	manager.qosApplier = func(_ string, _ netns.NsHandle, q link.QoS, _, _, _ bool) (bool, bool, error) {
		calls++
		return q.Rate > 0, q.Delay > 0 || q.Jitter > 0 || q.Loss > 0, nil
	}

	cfg := LinkConfig{
		Peer1: "A.0",
		Peer2: "B.0",
		Delay: 10,
		Rate:  1000,
	}
	if err := manager.LinkUpdate(cfg, false); err != nil {
		t.Fatalf("LinkUpdate returned an error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("qos applier called %d times, want 2 (one per direction)", calls)
	}

	updated, _, err := manager.GetLink("A.0", "B.0")
	if err != nil {
		t.Fatalf("GetLink returned an error: %v", err)
	}
	if !updated.HasPeer1Tbf || !updated.HasPeer1Netem || !updated.HasPeer2Tbf || !updated.HasPeer2Netem {
		t.Fatalf("qdisc flags were not updated from the applier: %#v", updated)
	}
}
