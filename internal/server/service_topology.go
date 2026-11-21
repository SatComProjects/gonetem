package server

import (
	"context"
	"fmt"

	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func toppologyRunProgressGoroutine(
	ctx context.Context,
	progressCh chan TopologyRunCloseProgressT,
	stream proto.Netem_TopologyRunServer,
) {
	// Progress reporting is best effort: keep draining progressCh even if the
	// client stopped reading, otherwise the topology run would block writing
	// to it.
	sendProgress := func(msg *proto.TopologyRunMsg) {
		if err := stream.Send(msg); err != nil {
			logrus.Warnf("unable to send topology run progress: %v", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case p := <-progressCh:
			switch p.Code {
			case NODE_COUNT:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_NODE_COUNT,
					Total: int32(p.Value),
				})
			case BRIDGE_COUNT:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_BRIDGE_COUNT,
					Total: int32(p.Value),
				})
			case LINK_COUNT:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_LINK_COUNT,
					Total: int32(p.Value),
				})
			case SETUP_LINK:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_LINK_SETUP,
					Total: int32(p.Value),
				})
			case START_NODE:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_NODE_START,
					Total: int32(p.Value),
				})
			case START_BRIDGE:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_BRIDGE_START,
					Total: int32(p.Value),
				})
			case LOADCONFIG_NODE:
				sendProgress(&proto.TopologyRunMsg{
					Code:  proto.TopologyRunMsg_NODE_LOADCONFIG,
					Total: int32(p.Value),
				})
			}
		}
	}
}

func toProtoNodeMessages(messages []NodeRunMessages) []*proto.TopologyRunMsg_NodeMessages {
	converted := make([]*proto.TopologyRunMsg_NodeMessages, len(messages))
	for idx, message := range messages {
		converted[idx] = &proto.TopologyRunMsg_NodeMessages{
			Name:     message.Name,
			Messages: append([]string(nil), message.Messages...),
		}
	}
	return converted
}

func (s *netemServer) TopologyRun(request *proto.ProjectRequest, stream proto.Netem_TopologyRunServer) error {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return &ProjectNotFoundError{request.GetId()}
	}

	progressCh := make(chan TopologyRunCloseProgressT, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go toppologyRunProgressGoroutine(ctx, progressCh, stream)

	nodeMessages, err := project.Topology.Run(progressCh)
	if err != nil {
		return err
	}

	if err := stream.Send(&proto.TopologyRunMsg{
		Code:         proto.TopologyRunMsg_NODE_MESSAGES,
		NodeMessages: toProtoNodeMessages(nodeMessages),
	}); err != nil {
		return err
	}
	return nil
}

func (s *netemServer) TopologyReload(request *proto.ProjectRequest, stream proto.Netem_TopologyReloadServer) error {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return &ProjectNotFoundError{request.GetId()}
	}

	progressCh := make(chan TopologyRunCloseProgressT)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go toppologyRunProgressGoroutine(ctx, progressCh, stream)

	nodeMessages, err := project.Topology.Reload(progressCh)
	if err != nil {
		return err
	}

	if err := stream.Send(&proto.TopologyRunMsg{
		Code:         proto.TopologyRunMsg_NODE_MESSAGES,
		NodeMessages: toProtoNodeMessages(nodeMessages),
	}); err != nil {
		return err
	}
	return nil
}

func (s *netemServer) TopologyStartAll(ctx context.Context, request *proto.ProjectRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	if !project.Topology.IsRunning() {
		return nil, fmt.Errorf("topology is not running; use TopologyRun first")
	}

	nodes := project.Topology.GetAllNodes()
	g := new(errgroup.Group)
	g.SetLimit(maxConcurrentNodeTask)

	for _, node := range nodes {
		node := node
		if node.IsRunning() {
			continue
		}

		g.Go(func() error {
			_, err := project.Topology.startNode(node)
			return err
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) TopologyStopAll(ctx context.Context, request *proto.ProjectRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	nodes := project.Topology.GetAllNodes()
	g := new(errgroup.Group)
	g.SetLimit(maxConcurrentNodeTask)

	for _, node := range nodes {
		node := node
		if !node.IsRunning() {
			continue
		}

		g.Go(func() error { return node.Stop() })
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) TopologyCheck(ctx context.Context, request *proto.ProjectRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	if err := project.Topology.Check(); err != nil {
		return &proto.AckResponse{
			Status: &proto.Status{
				Code:  proto.StatusCode_ERROR,
				Error: err.Error(),
			},
		}, nil
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}
