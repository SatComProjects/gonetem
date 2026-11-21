package server

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/satcomprojects/gonetem/internal/utils"
	"github.com/sirupsen/logrus"
)

func (s *netemServer) ProjectGetMany(ctx context.Context, empty *empty.Empty) (*proto.PrjListResponse, error) {
	prjList := s.projects.Snapshot()
	response := proto.PrjListResponse{
		Status: &proto.Status{
			Code: proto.StatusCode_OK,
		},
		Projects: make([]*proto.PrjListResponse_Info, 0, len(prjList)),
	}

	for _, prj := range prjList {
		response.Projects = append(response.Projects, &proto.PrjListResponse_Info{
			Id:     prj.Id,
			Name:   prj.Name,
			OpenAt: prj.OpenAt.Format("2006-01-02 15:04:05"),
		})
	}

	return &response, nil
}

func (s *netemServer) ProjectOpen(ctx context.Context, request *proto.OpenRequest) (*proto.PrjOpenResponse, error) {
	if s.projects.IsNameOpen(request.GetName()) {
		return &proto.PrjOpenResponse{
			Status: &proto.Status{
				Code:  proto.StatusCode_ERROR,
				Error: "A project with this name is already open.\nUse --name option if you want to open it anyway",
			},
		}, nil
	}

	prjID := utils.RandString(3)
	for s.projects.Has(prjID) {
		prjID = utils.RandString(3)
	}

	prj, err := s.projects.Open(prjID, request.GetName(), request.GetData())
	if err != nil {
		return nil, err
	}

	return &proto.PrjOpenResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
		Id:     prj.Id,
	}, nil
}

func (s *netemServer) ProjectClose(request *proto.ProjectRequest, stream proto.Netem_ProjectCloseServer) error {
	progressCh := make(chan TopologyRunCloseProgressT)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Progress reporting is best effort: keep draining progressCh even if the
	// client stopped reading, otherwise ProjectClose would block writing to it.
	sendProgress := func(msg *proto.ProjectCloseMsg) {
		if err := stream.Send(msg); err != nil {
			logrus.Warnf("unable to send project close progress: %v", err)
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-progressCh:
				switch p.Code {
				case NODE_COUNT:
					sendProgress(&proto.ProjectCloseMsg{
						Code:  proto.ProjectCloseMsg_NODE_COUNT,
						Total: int32(p.Value),
					})

				case BRIDGE_COUNT:
					sendProgress(&proto.ProjectCloseMsg{
						Code:  proto.ProjectCloseMsg_BRIDGE_COUNT,
						Total: int32(p.Value),
					})

				case CLOSE_NODE:
					sendProgress(&proto.ProjectCloseMsg{
						Code:  proto.ProjectCloseMsg_NODE_CLOSE,
						Total: int32(p.Value),
					})

				case CLOSE_BRIDGE:
					sendProgress(&proto.ProjectCloseMsg{
						Code:  proto.ProjectCloseMsg_BRIDGE_CLOSE,
						Total: int32(p.Value),
					})
				}
			}
		}
	}()

	return s.projects.Close(request.GetId(), progressCh)
}

func (s *netemServer) ProjectSave(request *proto.ProjectRequest, stream proto.Netem_ProjectSaveServer) error {
	progressCh := make(chan TopologySaveProgressT)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Progress reporting is best effort: keep draining progressCh even if the
	// client stopped reading, otherwise ProjectSave would block writing to it.
	sendProgress := func(msg *proto.ProjectSaveMsg) {
		if err := stream.Send(msg); err != nil {
			logrus.Warnf("unable to send project save progress: %v", err)
		}
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case p := <-progressCh:
				switch p.Code {
				case NODE_SAVE_COUNT:
					sendProgress(&proto.ProjectSaveMsg{
						Code:  proto.ProjectSaveMsg_NODE_COUNT,
						Total: int32(p.Value),
					})

				case NODE_SAVE:
					sendProgress(&proto.ProjectSaveMsg{
						Code: proto.ProjectSaveMsg_NODE_SAVE,
					})
				}
			}
		}
	}()

	data, err := s.projects.Save(request.GetId(), progressCh)
	if err != nil {
		return err
	}

	if err := stream.Send(&proto.ProjectSaveMsg{
		Code: proto.ProjectSaveMsg_DATA,
		Data: data.Bytes(),
	}); err != nil {
		return err
	}
	return nil
}

func (s *netemServer) ProjectGetNodeConfigs(ctx context.Context, request *proto.ProjectRequest) (*proto.FileResponse, error) {
	data, err := s.projects.GetNodeConfigs(request.GetId())
	if err != nil {
		return nil, err
	}

	return &proto.FileResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
		Data:   data.Bytes(),
	}, nil
}

func (s *netemServer) ProjectGetStatus(ctx context.Context, request *proto.ProjectRequest) (*proto.StatusResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	response := &proto.StatusResponse{
		Status:  &proto.Status{Code: proto.StatusCode_OK},
		Name:    project.Name,
		Id:      project.Id,
		OpenAt:  project.OpenAt.Format("2006-01-02 15:04:05"),
		Running: project.Topology.IsRunning(),
	}

	for _, node := range project.Topology.GetAllNodeStatuses() {
		nodeStatus := &proto.StatusResponse_NodeStatus{
			Name:    node.GetName(),
			Running: node.IsRunning(),
		}
		if project.Topology.IsRunning() {
			for ifName, state := range node.GetInterfacesState() {
				nodeStatus.Interfaces = append(nodeStatus.Interfaces, &proto.StatusResponse_IfStatus{
					Name: ifName,
					State: map[link.IfState]proto.IfState{
						link.IFSTATE_DOWN: proto.IfState_DOWN,
						link.IFSTATE_UP:   proto.IfState_UP,
					}[state],
				})
			}
		}

		response.Nodes = append(response.Nodes, nodeStatus)
	}

	return response, nil
}

func (s *netemServer) ReadNetworkFile(ctx context.Context, request *proto.ProjectRequest) (*proto.FileResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	data, err := project.Topology.ReadNetworkFile()
	if err != nil {
		return nil, err
	}
	return &proto.FileResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
		Data:   data,
	}, nil
}

func (s *netemServer) WriteNetworkFile(ctx context.Context, request *proto.WNetworkRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetId()}
	}

	if err := project.Topology.WriteNetworkFile(request.GetData()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}
