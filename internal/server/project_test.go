package server

import (
	"os"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/utils"
)

func createProject(filepath string, topo TopologyTestData) error {
	prj, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer prj.Close()

	return utils.CreateOneFileArchive(prj, networkFilename, []byte(topo.network))
}

func TestProject_OpenClose(t *testing.T) {
	options.InitServerConfig()

	// open a project built from namespace nodes: no images or privileges are
	// needed until the topology is started.
	prjID := utils.RandString(4)
	registry := newTestRegistry()
	project, err := registry.Open(prjID, "PrjTest", createProjectArchive(t, namespaceNetwork))
	if err != nil {
		t.Fatalf("Unable to open project: %v", err)
	}
	defer registry.Close(prjID, nil)

	// check topology
	checkTopology(namespaceNetwork, project.Topology, t)
}

func TestProject_Save(t *testing.T) {
	requireSimpleNetworkCapabilities(t)

	prjPath := "/tmp/prjtest-archive.gnet"
	if err := createProject(prjPath, simpleNetwork); err != nil {
		t.Errorf("Unable to create .gnet file: %v", err)
		return
	}
	defer os.Remove(prjPath)

	data, err := os.ReadFile(prjPath)
	if err != nil {
		t.Errorf("Unable to open created .gnet file: %v", err)
		return
	}

	// open project
	prjID := utils.RandString(4)
	registry := newTestRegistry()
	project, err := registry.Open(prjID, "PrjTest", data)
	if err != nil {
		t.Errorf("Unable to open project: %v", err)
		return
	}
	defer registry.Close(prjID, nil)

	if _, err := project.Topology.Run(nil); err != nil {
		t.Errorf("Unable to start project: %v", err)
		return
	}

	if err := project.Topology.Save(nil); err != nil {
		t.Errorf("Unable to save project: %v", err)
		return
	}

	for _, n := range simpleNetwork.nodes {
		node := project.Topology.GetNode(n.name)
		if node == nil {
			t.Errorf("Node %s is not found", n.name)
		} else if n.launch && !node.IsRunning() {
			t.Errorf("Node %s is not running", n.name)
		}
	}
}
