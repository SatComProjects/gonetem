package server

import (
	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/options"
)

// Config is the server's injected runtime configuration. Parsing and defaults
// stay in internal/options; the server, topology and adapters receive values.
type Config struct {
	Workdir string

	DockerTimeout int

	CreateConcurrency int
	StartConcurrency  int
	RemoveConcurrency int
	ConfigConcurrency int
	LinkConcurrency   int
	RouteConcurrency  int

	Update options.UpdateConfig
	// PullImages is the ordered list of node/OVS image names to pull.
	PullImages []string
	// Docker is the adapter configuration for Docker-backed nodes and OVS.
	Docker docker.RuntimeConfig
}

func ConfigFromOptions(cfg options.NetemServerConfig) Config {
	images := []string{
		cfg.Docker.Nodes.Router.Image,
		cfg.Docker.Nodes.Host.Image,
		cfg.Docker.Nodes.Server.Image,
		cfg.Docker.Nodes.P4sw.Image,
		cfg.Docker.OvsImage,
	}
	for _, node := range cfg.Docker.ExtraNodes {
		images = append(images, node.Image)
	}
	seen := make(map[string]struct{}, len(images))
	unique := make([]string, 0, len(images))
	for _, image := range images {
		if image == "" {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		unique = append(unique, image)
	}
	images = unique

	return Config{
		Workdir:           cfg.Workdir,
		DockerTimeout:     cfg.Docker.Timeoutop,
		CreateConcurrency: cfg.Docker.CreateConcurrency,
		StartConcurrency:  cfg.Docker.StartConcurrency,
		RemoveConcurrency: cfg.Docker.RemoveConcurrency,
		ConfigConcurrency: cfg.Docker.ConfigConcurrency,
		LinkConcurrency:   cfg.Docker.LinkConcurrency,
		RouteConcurrency:  cfg.Docker.RouteConcurrency,
		Update:            cfg.Update,
		PullImages:        images,
		Docker:            docker.RuntimeConfigFromOptions(cfg),
	}
}
