package server

import "testing"

// TestLinkAddDelCharacterization documents the state invariants of the link
// operations that later phases extract from the topology manager. Fake nodes
// keep it hermetic: the manager is not running, so no kernel work is needed.
func TestLinkAddDelCharacterization(t *testing.T) {
	manager := newLinkTestManager(t, newFakeLinkNode("A"), newFakeLinkNode("B"))

	if err := manager.LinkAdd(LinkConfig{
		Peer1: "A.00",
		Peer2: "B.0",
		Delay: 10,
		Rate:  1000,
	}, false); err != nil {
		t.Fatalf("LinkAdd returned an error: %v", err)
	}

	link, _, err := manager.GetLink("B.0", "A.0")
	if err != nil {
		t.Fatalf("GetLink returned an error: %v", err)
	}
	if link.Config.Delay != 10 {
		t.Errorf("delay = %d, want 10", link.Config.Delay)
	}
	if link.Config.Buffer != 1.0 {
		t.Errorf("buffer = %v, want the default 1.0", link.Config.Buffer)
	}
	if len(manager.links) != 1 || len(manager.linkIndex) != 1 {
		t.Errorf("link state = %d links / %d index entries, want 1/1",
			len(manager.links), len(manager.linkIndex))
	}

	// Peer normalization means A.00 and A.0 identify the same interface.
	if err := manager.LinkAdd(LinkConfig{Peer1: "A.0", Peer2: "B.0"}, false); err == nil {
		t.Fatal("duplicate link was accepted")
	}

	if err := manager.LinkDel(LinkConfig{Peer1: "A.0", Peer2: "B.0"}, false); err != nil {
		t.Fatalf("LinkDel returned an error: %v", err)
	}
	if _, _, err := manager.GetLink("A.0", "B.0"); err == nil {
		t.Fatal("deleted link is still present")
	}
	if len(manager.links) != 0 || len(manager.linkIndex) != 0 {
		t.Errorf("link state after delete = %d links / %d index entries, want 0/0",
			len(manager.links), len(manager.linkIndex))
	}
}
