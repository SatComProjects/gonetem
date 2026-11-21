package server

import (
	"errors"
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

func TestApplyPeerQoSUsesInjectedApplier(t *testing.T) {
	l := newTestLink()
	l.Config = LinkConfig{
		Peer1:    "A.0",
		Peer2:    "B.0",
		Peer1QoS: QoSConfig{Delay: 10, Rate: 1000},
		Peer2QoS: QoSConfig{Delay: 20, Loss: 1},
	}

	calls := 0
	applier := func(_ string, _ netns.NsHandle, q link.QoS, _, _, _ bool) (bool, bool, error) {
		calls++
		return q.Rate > 0, q.Delay > 0 || q.Loss > 0, nil
	}

	if err := l.ApplyPeer1QoS(applier, "eth0", 0, true); err != nil {
		t.Fatalf("ApplyPeer1QoS returned an error: %v", err)
	}
	if !l.HasPeer1Tbf || !l.HasPeer1Netem {
		t.Fatalf("peer1 flags were not updated: %#v", l)
	}
	if err := l.ApplyPeer2QoS(applier, "eth1", 0, false); err != nil {
		t.Fatalf("ApplyPeer2QoS returned an error: %v", err)
	}
	if l.HasPeer2Tbf || !l.HasPeer2Netem {
		t.Fatalf("peer2 flags were not updated: %#v", l)
	}
	if calls != 2 {
		t.Fatalf("applier calls = %d, want 2", calls)
	}
}

func TestApplyPeerQoSDiffPropagatesErrorAndKeepsFlags(t *testing.T) {
	hasTbf, hasNetem := false, false
	applier := func(_ string, _ netns.NsHandle, _ link.QoS, _, _, _ bool) (bool, bool, error) {
		return true, true, errors.New("synthetic qdisc failure")
	}

	err := applyPeerQoSDiff(applier, "eth0", 0, QoSConfig{}, QoSConfig{Delay: 10}, &hasTbf, &hasNetem)
	if err == nil {
		t.Fatal("applyPeerQoSDiff accepted an applier failure")
	}
	if hasTbf || hasNetem {
		t.Fatalf("flags changed on error: %v/%v", hasTbf, hasNetem)
	}
}
