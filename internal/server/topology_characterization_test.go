package server

import (
	"os"
	"path"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
)

// TestTopologyLoadNamespaceNodesAndLinks is a characterization test for the
// load path that later phases move into a dedicated topology model. It uses
// namespace nodes so it stays hermetic.
func TestTopologyLoadNamespaceNodesAndLinks(t *testing.T) {
	options.InitServerConfig()
	dir := t.TempDir()
	if err := os.WriteFile(path.Join(dir, networkFilename), []byte(namespaceNetwork.network), 0644); err != nil {
		t.Fatalf("unable to write topology: %v", err)
	}

	manager, err := LoadTopology("CHR", dir)
	if err != nil {
		t.Fatalf("LoadTopology returned an error: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(nil); err != nil {
			t.Logf("unable to close topology: %v", err)
		}
	})

	checkTopology(namespaceNetwork, manager, t)
	if got := len(manager.GetAllNodes()); got != 3 {
		t.Fatalf("nodes = %d, want 3", got)
	}
	if got := len(manager.links); got != 2 {
		t.Fatalf("links = %d, want 2", got)
	}

	link12, _, err := manager.GetLink("n2.0", "n1.0")
	if err != nil {
		t.Fatalf("GetLink(n2.0, n1.0) returned an error: %v", err)
	}
	if link12.Config.Delay != 5 {
		t.Errorf("link n1-n2 delay = %d, want 5", link12.Config.Delay)
	}
	if link12.Peer1.Node.GetName() != "n1" || link12.Peer2.Node.GetName() != "n2" {
		t.Errorf("unexpected peer orientation: %s-%s",
			link12.Peer1.Node.GetName(), link12.Peer2.Node.GetName())
	}

	link13, _, err := manager.GetLink("n1.1", "n3.0")
	if err != nil {
		t.Fatalf("GetLink(n1.1, n3.0) returned an error: %v", err)
	}
	if link13.Config.Loss != 1 {
		t.Errorf("link n1-n3 loss = %v, want 1", link13.Config.Loss)
	}
}
