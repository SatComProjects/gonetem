package server

import (
	"context"
	"testing"

	"github.com/mroy31/gonetem/internal/proto"
)

func TestSplitPeer(t *testing.T) {
	node, idx, err := splitPeer("R1.12")
	if err != nil {
		t.Fatalf("splitPeer returned an error: %v", err)
	}
	if node != "R1" || idx != 12 {
		t.Fatalf("unexpected result: %q %d", node, idx)
	}

	for _, peer := range []string{"", "R1", "R1.x", "R1.-1", ".0", "R1."} {
		if _, _, err := splitPeer(peer); err == nil {
			t.Errorf("splitPeer(%q) unexpectedly succeeded", peer)
		}
	}
}

func TestGetLinkRejectsMalformedPeer(t *testing.T) {
	manager := &NetemTopologyManager{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("GetLink panicked: %v", r)
		}
	}()

	if _, _, err := manager.GetLink("no-dot", "R2.0"); err == nil {
		t.Fatal("GetLink unexpectedly accepted a peer without an interface suffix")
	}
}

func TestLinkAddUnknownNodeReturnsError(t *testing.T) {
	manager := &NetemTopologyManager{
		nodes: make([]NetemNode, 0),
		links: make([]*NetemLink, 0),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LinkAdd panicked: %v", r)
		}
	}()

	err := manager.LinkAdd(LinkConfig{
		Peer1: "ghost.0",
		Peer2: "ghost2.0",
	}, false)
	if err == nil {
		t.Fatal("LinkAdd unexpectedly accepted unknown nodes")
	}
}

func TestLinkDelMissingLinkReturnsError(t *testing.T) {
	prjID := "tst"
	openProjects[prjID] = &NetemProject{
		Id:       prjID,
		Topology: &NetemTopologyManager{links: make([]*NetemLink, 0)},
	}
	defer delete(openProjects, prjID)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LinkDel panicked: %v", r)
		}
	}()

	_, err := (&netemServer{}).LinkDel(context.Background(), &proto.LinkRequest{
		PrjId: prjID,
	})
	if err == nil {
		t.Fatal("LinkDel unexpectedly accepted a request without a link")
	}
}
