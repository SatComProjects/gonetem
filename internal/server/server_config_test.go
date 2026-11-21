package server

import (
	"testing"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/options"
)

func TestTwoServersUseIndependentConfigurations(t *testing.T) {
	options.InitServerConfig()
	base := ConfigFromOptions(options.ServerConfig)

	cfgA := base
	cfgA.Workdir = t.TempDir()
	cfgA.LinkConcurrency = 3
	cfgB := base
	cfgB.Workdir = t.TempDir()
	cfgB.LinkConcurrency = 9

	clientA, err := docker.NewDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := docker.NewDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	serverA := NewServer(clientA, cfgA)
	serverB := NewServer(clientB, cfgB)
	defer serverA.Close()
	defer serverB.Close()

	archive := createProjectArchive(t, namespaceNetwork)
	projectA, err := serverA.projects.Open("a", "ProjectA", archive)
	if err != nil {
		t.Fatalf("server A Open: %v", err)
	}
	projectB, err := serverB.projects.Open("b", "ProjectB", archive)
	if err != nil {
		t.Fatalf("server B Open: %v", err)
	}

	if !serverA.projects.Has("a") || serverA.projects.Has("b") {
		t.Fatal("server A registry is not isolated")
	}
	if !serverB.projects.Has("b") || serverB.projects.Has("a") {
		t.Fatal("server B registry is not isolated")
	}
	if projectA.Dir == projectB.Dir {
		t.Fatal("projects share a directory despite different workdirs")
	}
	if serverA.config.LinkConcurrency != 3 || serverB.config.LinkConcurrency != 9 {
		t.Fatalf("configurations leaked: %d/%d",
			serverA.config.LinkConcurrency, serverB.config.LinkConcurrency)
	}
}
