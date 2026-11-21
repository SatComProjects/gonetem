package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTopologyFile(t *testing.T, network string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), networkFilename)
	if err := os.WriteFile(path, []byte(network), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckTopologyReportsFileAndParseErrors(t *testing.T) {
	if _, errs := CheckTopology(filepath.Join(t.TempDir(), "missing.yml")); len(errs) == 0 {
		t.Fatal("CheckTopology accepted a missing file")
	}

	path := writeTopologyFile(t, "nodes: [")
	if _, errs := CheckTopology(path); len(errs) == 0 {
		t.Fatal("CheckTopology accepted invalid YAML")
	}

	path = writeTopologyFile(t, "nodes:\n  n1:\n    type: namespace\n    vrfs: not-a-list\n")
	if _, errs := CheckTopology(path); len(errs) == 0 {
		t.Fatal("CheckTopology accepted an invalid NodeConfig field type")
	}
}

func TestCheckTopologyReportsSemanticErrors(t *testing.T) {
	path := writeTopologyFile(t, `
nodes:
  n1:
    type: namespace
links:
- peer1: n1.0
  peer2: n1.0
mgntnet:
  enable: true
  address: not-a-cidr
`)
	_, errs := CheckTopology(path)
	if len(errs) < 2 {
		t.Fatalf("expected same-peer and management errors, got %v", errs)
	}
}

func TestCheckTopologyRejectsInvalidBridgeName(t *testing.T) {
	path := writeTopologyFile(t, `
nodes:
  n1:
    type: namespace
bridges:
  bad-name:
    host: lo
    interfaces:
    - n1.0
`)
	_, errs := CheckTopology(path)
	if len(errs) == 0 {
		t.Fatal("CheckTopology accepted an invalid bridge name")
	}
	found := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "bad-name") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bridge name error missing: %v", errs)
	}
}

func TestCheckTopologyRejectsUnknownLinkNode(t *testing.T) {
	path := writeTopologyFile(t, `
nodes:
  n1:
    type: namespace
links:
- peer1: ghost.0
  peer2: n1.0
`)
	_, errs := CheckTopology(path)
	if len(errs) == 0 {
		t.Fatal("CheckTopology accepted an unknown link node")
	}
}
