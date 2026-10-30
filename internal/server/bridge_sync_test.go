package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSynchroniseTopologyPreservesBridgeConfigName(t *testing.T) {
	dir := t.TempDir()
	manager := &NetemTopologyManager{
		prjID: "abc",
		path:  dir,
		nodes: make([]NetemNode, 0),
		links: make([]*NetemLink, 0),
		bridges: []*NetemBridge{
			{
				Name:          "ntmabc.0br1",
				ConfigName:    "br1",
				HostInterface: "eth9",
				Config: BridgeConfig{
					Host:       "eth9",
					Interfaces: []string{"R1.0"},
				},
			},
		},
	}

	if err := manager.SynchroniseTopology(); err != nil {
		t.Fatalf("SynchroniseTopology returned an error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, networkFilename))
	if err != nil {
		t.Fatal(err)
	}

	var topo NetemTopology
	if err := yaml.Unmarshal(data, &topo); err != nil {
		t.Fatal(err)
	}

	if _, ok := topo.Bridges["br1"]; !ok {
		t.Fatalf("bridge config name was not preserved, got bridges: %#v", topo.Bridges)
	}
	if _, ok := topo.Bridges["ntmabc.0br1"]; ok {
		t.Fatalf("internal bridge name leaked into network.yml: %#v", topo.Bridges)
	}
}

func TestSynchroniseTopologyKeepsNoQoSLinksValid(t *testing.T) {
	dir := t.TempDir()
	manager := &NetemTopologyManager{
		prjID: "abc",
		path:  dir,
		nodes: []NetemNode{
			{
				Instance: &fakeStartNode{name: "A"},
				Config:   NodeConfig{Type: "docker.host", Launch: true},
			},
			{
				Instance: &fakeStartNode{name: "B"},
				Config:   NodeConfig{Type: "docker.host", Launch: true},
			},
		},
		links: []*NetemLink{
			{Config: LinkConfig{Peer1: "A.0", Peer2: "B.0"}},
		},
		bridges: make([]*NetemBridge, 0),
	}

	if err := manager.SynchroniseTopology(); err != nil {
		t.Fatalf("SynchroniseTopology returned an error: %v", err)
	}

	_, errors := CheckTopology(filepath.Join(dir, networkFilename))
	if len(errors) > 0 {
		t.Fatalf("saved topology is invalid: %v", errors)
	}
}

func TestSynchroniseTopologyConcurrentWritesAreAtomic(t *testing.T) {
	dir := t.TempDir()
	manager := &NetemTopologyManager{
		prjID:   "abc",
		path:    dir,
		nodes:   make([]NetemNode, 0),
		links:   make([]*NetemLink, 0),
		bridges: make([]*NetemBridge, 0),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.SynchroniseTopology(); err != nil {
				t.Errorf("SynchroniseTopology returned an error: %v", err)
			}
		}()
	}
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != networkFilename {
		t.Fatalf("unexpected files after concurrent sync: %#v", entries)
	}
	data, err := os.ReadFile(filepath.Join(dir, networkFilename))
	if err != nil {
		t.Fatal(err)
	}
	var topo NetemTopology
	if err := yaml.Unmarshal(data, &topo); err != nil {
		t.Fatalf("network.yml is not valid YAML: %v", err)
	}
}
