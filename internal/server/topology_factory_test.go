package server

import (
	"os"
	"path"
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

// TestTopologyLoadUsesNodeFactory documents the YAML-to-domain mapping without
// creating Docker containers or network namespaces. The factory is the seam
// later phases use to keep topology tests hermetic.
func TestTopologyLoadUsesNodeFactory(t *testing.T) {
	options.InitServerConfig()
	dir := t.TempDir()
	if err := os.WriteFile(path.Join(dir, networkFilename), []byte(namespaceNetwork.network), 0644); err != nil {
		t.Fatalf("unable to write topology: %v", err)
	}

	type createdNode struct {
		shortName string
		kind      string
		launch    bool
	}
	var (
		mu      sync.Mutex
		created = make(map[string]createdNode)
	)

	manager := &NetemTopologyManager{
		prjID:       "TST",
		path:        dir,
		logger:      logrus.WithField("project", "TST"),
		IdGenerator: &NodeIdentifierGenerator{lock: &sync.Mutex{}},
		nodeFactory: func(prjID, name, shortName string, config NodeConfig) (INetemNode, error) {
			mu.Lock()
			created[name] = createdNode{shortName: shortName, kind: config.Type, launch: config.Launch}
			mu.Unlock()
			return &fakeStartNode{name: name}, nil
		},
	}

	if err := manager.Load(); err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(created) != 3 {
		t.Fatalf("node factory called for %d nodes, want 3: %#v", len(created), created)
	}
	for _, name := range []string{"n1", "n2", "n3"} {
		got, ok := created[name]
		if !ok {
			t.Fatalf("node factory was not called for %s", name)
		}
		if got.kind != "namespace" {
			t.Errorf("%s type = %q, want namespace", name, got.kind)
		}
		if got.shortName == "" {
			t.Errorf("%s has an empty short name", name)
		}
		if manager.GetNode(name) == nil {
			t.Errorf("%s was not added to the topology", name)
		}
	}
	if created["n3"].launch {
		t.Error("n3 launch = true, want false")
	}
	if len(manager.links) != 2 {
		t.Errorf("links = %d, want 2", len(manager.links))
	}
}
