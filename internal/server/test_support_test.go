package server

import (
	"bytes"
	"testing"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/testutil"
	"github.com/satcomprojects/gonetem/internal/utils"
)

func testServerConfig() Config {
	if options.ServerConfig.Docker.Nodes.Host.Image == "" {
		options.InitServerConfig()
	}
	return ConfigFromOptions(options.ServerConfig)
}

func testNodeFactory() *NodeFactory {
	client, err := docker.NewDockerClient()
	if err != nil {
		panic(err)
	}
	return NewNodeFactory(client, testServerConfig().Docker)
}

func newTestRegistry() *ProjectRegistry {
	return NewProjectRegistry(testServerConfig(), testNodeFactory())
}

// newTestServer builds a handler with an empty per-test project registry.
func newTestServer() *netemServer {
	client, err := docker.NewDockerClient()
	if err != nil {
		panic(err)
	}
	return NewServer(client, testServerConfig())
}

func loadTestTopology(prjID, dir string) (*NetemTopologyManager, error) {
	return LoadTopology(prjID, dir, testServerConfig(), testNodeFactory())
}

// testServerWithProject registers an already-built project and returns a
// handler backed by the same registry.
func testServerWithProject(t *testing.T, project *NetemProject) *netemServer {
	t.Helper()

	server := newTestServer()
	if err := server.projects.Add(project); err != nil {
		t.Fatalf("unable to register test project: %v", err)
	}
	t.Cleanup(func() { server.projects.Remove(project.Id) })
	return server
}

// createProjectArchive builds an in-memory .gnet archive from a topology
// fixture, so project tests do not need a temporary file on disk.
func createProjectArchive(t *testing.T, topo TopologyTestData) []byte {
	t.Helper()

	var buffer bytes.Buffer
	if err := utils.CreateOneFileArchive(&buffer, networkFilename, []byte(topo.network)); err != nil {
		t.Fatalf("unable to create project archive: %v", err)
	}
	return buffer.Bytes()
}

// requireSimpleNetworkCapabilities guards tests that run the docker/OVS
// fixture. RequireRoot and RequireImage turn skips into failures when
// GONETEM_REQUIRE_INTEGRATION=1.
func requireSimpleNetworkCapabilities(t *testing.T) {
	t.Helper()
	options.InitServerConfig()
	testutil.RequireRoot(t)
	testutil.RequireMPLS(t)
	requireDockerImage(t, options.ServerConfig.Docker.Nodes.Router.Image)
	requireDockerImage(t, options.ServerConfig.Docker.Nodes.Host.Image)
	requireDockerImage(t, options.ServerConfig.Docker.OvsImage)
}

func requireRouterCapabilities(t *testing.T) {
	t.Helper()
	options.InitServerConfig()
	testutil.RequireRoot(t)
	testutil.RequireMPLS(t)
	requireDockerImage(t, options.ServerConfig.Docker.Nodes.Router.Image)
}

func requireOvsCapabilities(t *testing.T) {
	t.Helper()
	options.InitServerConfig()
	testutil.RequireRoot(t)
	requireDockerImage(t, options.ServerConfig.Docker.OvsImage)
}

func requireDockerImage(t *testing.T, image string) {
	t.Helper()
	testutil.RequireImage(t, options.GetDockerImageId(image))
}
