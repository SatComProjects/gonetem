package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/utils"
)

func TestTopologyRunCreatesOvsLazily(t *testing.T) {
	options.InitServerConfig()

	dir := t.TempDir()
	network := `
nodes:
  sw1:
    type: ovs
`
	if err := os.WriteFile(filepath.Join(dir, networkFilename), []byte(network), 0644); err != nil {
		t.Fatal(err)
	}

	topology, err := LoadTopology(utils.RandString(6), dir)
	if err != nil {
		t.Fatalf("LoadTopology returned an error: %v", err)
	}
	if topology.ovsInstance != nil {
		t.Fatal("OVS instance was created during LoadTopology")
	}

	if _, err := topology.Run(nil); err != nil {
		t.Fatalf("Run returned an error: %v", err)
	}
	if topology.ovsInstance == nil {
		t.Fatal("Run did not create the OVS instance")
	}
	if !topology.IsRunning() {
		t.Fatal("topology is not running after Run")
	}

	if err := topology.Close(nil); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
}
