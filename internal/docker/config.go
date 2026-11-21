package docker

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/options"
)

// RuntimeConfig is the Docker adapter configuration passed down from the
// composition root. It contains no mutable global state.
type RuntimeConfig struct {
	Workdir    string
	Timeout    int
	OvsImage   string
	nodes      map[string]*options.DockerNodeConfig
	extraNodes []*options.DockerNodeConfig
}

// RuntimeConfigFromOptions snapshots the parsed server configuration into the
// Docker adapter's value object.
func RuntimeConfigFromOptions(cfg options.NetemServerConfig) RuntimeConfig {
	nodes := map[string]*options.DockerNodeConfig{
		"router": &cfg.Docker.Nodes.Router,
		"host":   &cfg.Docker.Nodes.Host,
		"server": &cfg.Docker.Nodes.Server,
		"p4sw":   &cfg.Docker.Nodes.P4sw,
	}
	extra := make([]*options.DockerNodeConfig, 0, len(cfg.Docker.ExtraNodes))
	extraMap := make(map[string]*options.DockerNodeConfig, len(cfg.Docker.ExtraNodes))
	for idx := range cfg.Docker.ExtraNodes {
		extra = append(extra, &cfg.Docker.ExtraNodes[idx])
		extraMap[cfg.Docker.ExtraNodes[idx].Type] = &cfg.Docker.ExtraNodes[idx]
	}
	for name, node := range extraMap {
		nodes[name] = node
	}
	return RuntimeConfig{
		Workdir:    cfg.Workdir,
		Timeout:    cfg.Docker.Timeoutop,
		OvsImage:   cfg.Docker.OvsImage,
		nodes:      nodes,
		extraNodes: extra,
	}
}

// NodeConfig resolves a node type definition.
func (c RuntimeConfig) NodeConfig(nodeType string) (*options.DockerNodeConfig, error) {
	if node, ok := c.nodes[nodeType]; ok && node != nil {
		return node, nil
	}
	return nil, fmt.Errorf("docker node of type %s does not exist in the configuration", nodeType)
}

// ExtraNodeConfigs returns the configured custom node types.
func (c RuntimeConfig) ExtraNodeConfigs() []*options.DockerNodeConfig {
	return c.extraNodes
}
