package server

import (
	"context"
	"fmt"

	"github.com/satcomprojects/gonetem/internal/proto"
)

func (s *netemServer) LinkUpdate(ctx context.Context, request *proto.LinkRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if request.GetLink() == nil {
		return nil, fmt.Errorf("link configuration is missing")
	}
	linkConfig := getLinkConfigFromRequest(request)
	if err := project.Topology.LinkUpdate(linkConfig, request.GetSync()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) LinkUpdateMany(ctx context.Context, request *proto.LinkUpdateManyRequest) (*proto.LinkUpdateManyResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	configs := make([]LinkConfig, 0, len(request.GetLinks()))
	for idx, rLink := range request.GetLinks() {
		if rLink == nil {
			return nil, fmt.Errorf("link configuration %d is missing", idx)
		}
		configs = append(configs, getLinkConfig(rLink))
	}

	outcomes, err := project.Topology.LinkUpdateMany(configs, request.GetSync())
	if err != nil {
		return nil, err
	}

	results := make([]*proto.LinkUpdateResult, 0, len(outcomes))
	updated := uint32(0)
	for _, outcome := range outcomes {
		status := &proto.Status{Code: proto.StatusCode_OK}
		if outcome.Err != nil {
			status.Code = proto.StatusCode_ERROR
			status.Error = outcome.Err.Error()
		} else {
			updated++
		}
		results = append(results, &proto.LinkUpdateResult{
			Status:         status,
			Peer1:          outcome.Peer1,
			Peer2:          outcome.Peer2,
			DurationMicros: outcome.DurationMicros,
		})
	}

	return &proto.LinkUpdateManyResponse{
		Status:  &proto.Status{Code: proto.StatusCode_OK},
		Results: results,
		Updated: updated,
	}, nil
}

func (s *netemServer) LinkSyncMany(ctx context.Context, request *proto.LinkSyncManyRequest) (*proto.LinkSyncManyResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	operations := make([]LinkSyncOperation, 0, len(request.GetOperations()))
	for idx, rOp := range request.GetOperations() {
		if rOp == nil {
			return nil, fmt.Errorf("link sync operation %d is missing", idx)
		}
		if rOp.GetLink() == nil {
			return nil, fmt.Errorf("link configuration %d is missing", idx)
		}
		action, err := linkSyncActionFromProto(rOp.GetAction())
		if err != nil {
			return nil, err
		}
		operations = append(operations, LinkSyncOperation{
			Action: action,
			Config: getLinkConfig(rOp.GetLink()),
		})
	}

	outcomes, err := project.Topology.LinkSyncMany(operations, request.GetSync())
	if err != nil {
		return nil, err
	}

	results := make([]*proto.LinkSyncResult, 0, len(outcomes))
	var added, deleted, updated uint32
	for _, outcome := range outcomes {
		status := &proto.Status{Code: proto.StatusCode_OK}
		if outcome.Err != nil {
			status.Code = proto.StatusCode_ERROR
			status.Error = outcome.Err.Error()
		} else {
			switch outcome.Action {
			case LinkSyncAdd:
				added++
			case LinkSyncDel:
				deleted++
			case LinkSyncUpdate:
				updated++
			}
		}
		results = append(results, &proto.LinkSyncResult{
			Status:         status,
			Action:         linkSyncActionToProto(outcome.Action),
			Peer1:          outcome.Peer1,
			Peer2:          outcome.Peer2,
			DurationMicros: outcome.DurationMicros,
		})
	}

	return &proto.LinkSyncManyResponse{
		Status:  &proto.Status{Code: proto.StatusCode_OK},
		Results: results,
		Added:   added,
		Deleted: deleted,
		Updated: updated,
	}, nil
}

func linkSyncActionFromProto(action proto.LinkSyncAction) (LinkSyncAction, error) {
	switch action {
	case proto.LinkSyncAction_LINK_SYNC_ADD:
		return LinkSyncAdd, nil
	case proto.LinkSyncAction_LINK_SYNC_DEL:
		return LinkSyncDel, nil
	case proto.LinkSyncAction_LINK_SYNC_UPDATE:
		return LinkSyncUpdate, nil
	default:
		return 0, fmt.Errorf("unknown link sync action %d", action)
	}
}

func linkSyncActionToProto(action LinkSyncAction) proto.LinkSyncAction {
	switch action {
	case LinkSyncAdd:
		return proto.LinkSyncAction_LINK_SYNC_ADD
	case LinkSyncDel:
		return proto.LinkSyncAction_LINK_SYNC_DEL
	case LinkSyncUpdate:
		return proto.LinkSyncAction_LINK_SYNC_UPDATE
	default:
		return proto.LinkSyncAction_LINK_SYNC_ADD
	}
}

func (s *netemServer) LinkAdd(ctx context.Context, request *proto.LinkRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if request.GetLink() == nil {
		return nil, fmt.Errorf("link configuration is missing")
	}
	linkConfig := getLinkConfigFromRequest(request)
	if err := project.Topology.LinkAdd(linkConfig, request.GetSync()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) LinkDel(ctx context.Context, request *proto.LinkRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	rLink := request.GetLink()
	if rLink == nil {
		return nil, fmt.Errorf("link configuration is missing")
	}
	linkConfig := LinkConfig{
		Peer1: rLink.GetPeer1(),
		Peer2: rLink.GetPeer2(),
	}

	if err := project.Topology.LinkDel(linkConfig, request.GetSync()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}
