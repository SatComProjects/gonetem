package server

import (
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

// fakeRunNode is a fully in-memory node for the run/close characterization
// test. It records lifecycle transitions without touching the kernel.
type fakeRunNode struct {
	fakeStartNode
	running bool
	closed  bool
}

func (n *fakeRunNode) Start() error    { n.running = true; return nil }
func (n *fakeRunNode) Stop() error     { n.running = false; return nil }
func (n *fakeRunNode) Close() error    { n.running = false; n.closed = true; return nil }
func (n *fakeRunNode) IsRunning() bool { return n.running }
func (n *fakeRunNode) GetType() string { return "namespace" }
func (n *fakeRunNode) GetShortName() string {
	return n.name
}

// TestTopologyRunCloseCharacterization documents the lifecycle contract that
// Phase 5 extracts from the topology manager: launch-at-startup is honoured,
// configuration is loaded, and Close stops every node once.
func TestTopologyRunCloseCharacterization(t *testing.T) {
	options.InitServerConfig()
	first := &fakeRunNode{fakeStartNode: fakeStartNode{name: "n1"}}
	second := &fakeRunNode{fakeStartNode: fakeStartNode{name: "n2"}}

	manager := &NetemTopologyManager{
		prjID:       "RUN",
		path:        t.TempDir(),
		logger:      logrus.WithField("project", "RUN"),
		IdGenerator: &NodeIdentifierGenerator{lock: &sync.Mutex{}},
		nodes: []NetemNode{
			{Instance: first, LaunchAtStartup: true, Config: NodeConfig{Type: "namespace", Launch: true}},
			{Instance: second, LaunchAtStartup: false, Config: NodeConfig{Type: "namespace", Launch: false}},
		},
		links:   []*NetemLink{},
		bridges: []*NetemBridge{},
	}

	messages, err := manager.Run(nil)
	if err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if !manager.IsRunning() {
		t.Fatal("topology is not running after Run")
	}
	if !first.running || !first.configured || !first.loaded {
		t.Errorf("launch-at-startup node not started/configured/loaded: %#v", first)
	}
	if second.running || second.configured || second.loaded {
		t.Errorf("non-launch node was started/configured/loaded: %#v", second)
	}
	if len(messages) != 1 || messages[0].Name != "n1" {
		t.Errorf("unexpected node messages: %#v", messages)
	}

	if err := manager.Close(nil); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if manager.IsRunning() {
		t.Fatal("topology is still running after Close")
	}
	if !first.closed || !second.closed {
		t.Errorf("nodes were not closed: first=%v second=%v", first.closed, second.closed)
	}
}
