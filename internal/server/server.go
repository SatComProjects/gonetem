package server

import (
	"context"
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
	// updateMu serializes self-update operations.
	updateMu sync.Mutex
}

func (s *netemServer) getDockerClient() (docker.ContainerAPI, error) {
	if s.dockerClient != nil {
		return s.dockerClient, nil
	}
	return docker.NewDockerClient()
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

func NewServer() *netemServer {
	client, err := docker.NewDockerClient()
	if err != nil {
		logrus.Errorf("unable to initialize Docker client: %v", err)
	}
	return &netemServer{
		dockerClient: client,
		projects:     NewProjectRegistry(),
	}
}
