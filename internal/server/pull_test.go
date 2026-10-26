package server

import (
	"testing"

	"github.com/mroy31/gonetem/internal/options"
)

func TestImageTypesForPullIncludesExtraNodes(t *testing.T) {
	options.InitServerConfig()
	previous := options.ServerConfig.Docker.ExtraNodes
	t.Cleanup(func() {
		options.ServerConfig.Docker.ExtraNodes = previous
	})

	options.ServerConfig.Docker.ExtraNodes = []options.DockerNodeConfig{
		{Type: "satellite", Image: "example/satellite:v1"},
		{Type: "ground", Image: "example/satellite:v1"},
	}

	images := imageTypesForPull()
	found := 0
	for _, image := range images {
		if image == "example/satellite:v1" {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("extra-node image should appear exactly once, got %d: %#v", found, images)
	}
}
