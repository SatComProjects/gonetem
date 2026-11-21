package server

import (
	"context"
	"fmt"
	"sync"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/sirupsen/logrus"
)

type netemServer struct {
	proto.UnimplementedNetemServer
	// routeApplier is injectable for tests. Production uses applyRoutesToNode.
	routeApplier func(context.Context, RouteNode, []link.RouteSpec) (int, error)
	// dockerClient is injectable at the composition root. The fallback keeps
	// bare test servers usable.
	dockerClient docker.ContainerAPI
	// projects is the per-server project registry. Handlers must use it; the
	// zero value is not usable for project RPCs.
	projects *ProjectRegistry
	config   Config
	factory  *NodeFactory
	// updateMu serializes self-update operations.
	updateMu sync.Mutex
}

func (s *netemServer) getDockerClient() (docker.ContainerAPI, error) {
	if s.dockerClient == nil {
		return nil, fmt.Errorf("docker client is not configured")
	}
	return s.dockerClient, nil
}

func (s *netemServer) Close() error {
	var ids []string
	for _, project := range s.projects.Snapshot() {
		ids = append(ids, project.Id)
	}

	for _, prjId := range ids {
		if err := s.projects.Close(prjId, nil); err != nil {
			logrus.Errorf("Error when closing project %s: %v", prjId, err)
		}
	}

	return nil
}

func NewServer(client docker.ContainerAPI, config Config) *netemServer {
	factory := NewNodeFactory(client, config.Docker)
	return &netemServer{
		dockerClient: client,
		config:       config,
		factory:      factory,
		projects:     NewProjectRegistry(config, factory),
	}
}
