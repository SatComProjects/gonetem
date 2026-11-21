package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/moby/term"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/proto"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func (s *netemServer) NodeAdd(ctx context.Context, request *proto.NodeAddRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	config := getNodeConfigFromRequest(request)
	if err := project.Topology.NodeAdd(request.GetNode(), config, request.GetSync()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeDel(ctx context.Context, request *proto.NodeDelRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if err := project.Topology.NodeDel(request.GetNode(), request.GetSync()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeStart(ctx context.Context, request *proto.NodeRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if _, err := project.Topology.Start(request.GetNode()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeStop(ctx context.Context, request *proto.NodeRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if err := project.Topology.Stop(request.GetNode()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeRestart(ctx context.Context, request *proto.NodeRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	if err := project.Topology.Stop(request.GetNode()); err != nil {
		return nil, err
	}
	if _, err := project.Topology.Start(request.GetNode()); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeSetIfState(ctx context.Context, request *proto.NodeIfStateRequest) (*proto.AckResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	stateNode := InterfaceStateNode(project.Topology.GetNode(request.GetNode()))
	if stateNode == nil {
		return nil, &NodeNotFoundError{request.GetPrjId(), request.GetNode()}
	}

	var state link.IfState
	switch request.GetState() {
	case proto.IfState_DOWN:
		state = link.IFSTATE_DOWN
	case proto.IfState_UP:
		state = link.IFSTATE_UP
	default:
		return nil, fmt.Errorf("invalid interface state %d", request.GetState())
	}

	if err := stateNode.SetInterfaceState(int(request.GetIfIndex()), state); err != nil {
		return nil, err
	}

	return &proto.AckResponse{
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeCapture(request *proto.NodeInterfaceRequest, stream proto.Netem_NodeCaptureServer) error {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return stream.Send(&proto.CaptureSrvMsg{
			Code: proto.CaptureSrvMsg_ERROR,
			Data: []byte(fmt.Sprintf("Project %s not found", request.PrjId)),
		})
	}

	captureNode := CaptureNode(project.Topology.GetNode(request.GetNode()))
	if captureNode == nil {
		return stream.Send(&proto.CaptureSrvMsg{
			Code: proto.CaptureSrvMsg_ERROR,
			Data: []byte(fmt.Sprintf("Node %s not found", request.GetNode())),
		})
	}

	if err := stream.Send(&proto.CaptureSrvMsg{
		Code: proto.CaptureSrvMsg_OK,
	}); err != nil {
		return err
	}

	logger := logrus.WithFields(logrus.Fields{
		"project": project.Id,
		"node":    captureNode.GetName(),
	})

	rOut, wOut := io.Pipe()
	waitCh := make(chan error, 1)

	go func() {
		defer wOut.Close()
		defer rOut.Close()

		data := make([]byte, 256)
		for {
			n, err := rOut.Read(data)
			if err != nil {
				if err == io.EOF {
					waitCh <- nil
				} else {
					waitCh <- err
				}
				return
			} else if n == 0 {
				continue
			}

			if err := stream.Send(&proto.CaptureSrvMsg{
				Code: proto.CaptureSrvMsg_STDOUT,
				Data: data[:n],
			}); err != nil {
				_ = rOut.CloseWithError(err)
				waitCh <- err
				return
			}
		}
	}()

	logger.Debugf("Start capture on interface %d", request.IfIndex)
	if err := captureNode.Capture(stream.Context(), int(request.GetIfIndex()), wOut); err != nil {
		if sendErr := stream.Send(&proto.CaptureSrvMsg{
			Code: proto.CaptureSrvMsg_ERROR,
			Data: []byte(err.Error()),
		}); sendErr != nil {
			_ = wOut.Close()
			return sendErr
		}
	}

	logger.Debugf("Stop capture on interface %d", request.IfIndex)
	_ = wOut.Close()
	return <-waitCh
}

func (s *netemServer) NodeReadConfigFiles(ctx context.Context, request *proto.NodeRequest) (*proto.ConfigFilesResponse, error) {
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	configFiles, err := project.Topology.ReadConfigFiles(request.GetNode())
	if err != nil {
		return &proto.ConfigFilesResponse{
			Status: &proto.Status{
				Code:  proto.StatusCode_ERROR,
				Error: fmt.Sprintf("Unable to read config files of node %s: %v", request.GetNode(), err),
			},
		}, nil
	}

	answer := &proto.ConfigFilesResponse{
		Status: &proto.Status{
			Code: proto.StatusCode_OK,
		},
		Source: proto.ConfigFilesResponse_ARCHIVE,
	}
	if project.Topology.GetNode(request.GetNode()).IsRunning() {
		answer.Source = proto.ConfigFilesResponse_RUNNING
	}

	for name, data := range configFiles {
		answer.Files = append(answer.Files, &proto.ConfigFilesResponse_ConfigFile{
			Name: name,
			Data: data,
		})
	}

	return answer, nil
}

func (s *netemServer) NodeExec(stream proto.Netem_NodeExecServer) error {
	// read first msg from client
	msg, err := stream.Recv()
	if err != nil {
		return err
	}

	if msg.GetCode() != proto.ExecCltMsg_CMD {
		return stream.Send(&proto.ExecSrvMsg{
			Code: proto.ExecSrvMsg_ERROR,
			Data: []byte("Wrong code for first msg"),
		})
	}

	// get project
	project := s.projects.Get(msg.GetPrjId())
	if project == nil {
		return stream.Send(&proto.ExecSrvMsg{
			Code: proto.ExecSrvMsg_ERROR,
			Data: []byte(fmt.Sprintf("Project %s not found", msg.PrjId)),
		})
	}

	// get node
	execNode := ExecNode(project.Topology.GetNode(msg.GetNode()))
	if execNode == nil {
		return stream.Send(&proto.ExecSrvMsg{
			Code: proto.ExecSrvMsg_ERROR,
			Data: []byte(fmt.Sprintf("Node %s not found in project %s", msg.GetNode(), msg.GetPrjId())),
		})
	}

	logger := logrus.WithFields(logrus.Fields{
		"project": project.Id,
		"node":    execNode.GetName(),
	})
	logger.Debug("Start Exec stream")

	rIn, wIn := io.Pipe()
	rOut, wOut := io.Pipe()

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	resizeCh := make(chan term.Winsize)

	g := new(errgroup.Group)
	g.Go(func() error {
		defer close(resizeCh)
		defer func() { _ = wIn.Close() }()

		for {
			in, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}

			switch in.GetCode() {
			case proto.ExecCltMsg_DATA:
				if _, err := wIn.Write(in.GetData()); err != nil {
					return err
				}
			case proto.ExecCltMsg_RESIZE:
				select {
				case resizeCh <- term.Winsize{
					Width:  uint16(in.GetTtyWidth()),
					Height: uint16(in.GetTtyHeight()),
				}:
				case <-ctx.Done():
					return ctx.Err()
				}
			case proto.ExecCltMsg_CLOSE:
				cancel()
				return nil
			}
		}
	})

	g.Go(func() error {
		defer rOut.Close()

		data := make([]byte, 32)
		for {
			n, err := rOut.Read(data)
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			} else if n == 0 {
				continue
			}

			if err := stream.Send(&proto.ExecSrvMsg{
				Code: proto.ExecSrvMsg_STDOUT,
				Data: data[:n],
			}); err != nil {
				cancel()
				return err
			}
		}
	})

	err = execNode.ExecCommand(
		ctx, msg.GetCmd(), rIn, wOut,
		msg.GetTty(), uint(msg.GetTtyHeight()), uint(msg.GetTtyWidth()),
		resizeCh)
	var sendErr error
	if err != nil {
		sendErr = stream.Send(&proto.ExecSrvMsg{
			Code: proto.ExecSrvMsg_ERROR,
			Data: []byte(err.Error()),
		})
	} else {
		sendErr = stream.Send(&proto.ExecSrvMsg{
			Code: proto.ExecSrvMsg_CLOSE,
		})
	}

	_ = wOut.Close()
	defer func() { _ = wIn.Close() }()

	if sendErr != nil {
		return fmt.Errorf("unable to send exec result: %w", sendErr)
	}

	logger.Debug("Close exec stream")
	return g.Wait()
}

func (s *netemServer) NodeGetConsoleCmd(ctx context.Context, request *proto.ConsoleCmdRequest) (*proto.ConsoleCmdResponse, error) {
	// get project
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	// get node
	consoleNode := ConsoleNode(project.Topology.GetNode(request.GetNode()))
	if consoleNode == nil {
		return nil, &NodeNotFoundError{
			prjId: request.GetPrjId(),
			name:  request.GetNode(),
		}
	}

	cmd, err := consoleNode.GetConsoleCmd(request.GetShell())
	if err != nil {
		return nil, err
	}
	return &proto.ConsoleCmdResponse{
		Cmd:    cmd,
		Status: &proto.Status{Code: proto.StatusCode_OK},
	}, nil
}

func (s *netemServer) NodeRun(ctx context.Context, request *proto.NodeRunRequest) (*proto.NodeRunResponse, error) {
	// get project
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	// get node
	runNode := RunNode(project.Topology.GetNode(request.GetNode()))
	if runNode == nil {
		return nil, &NodeNotFoundError{
			prjId: request.GetPrjId(),
			name:  request.GetNode(),
		}
	}

	stdout, stderr, exitCode, err := runNode.RunCommand(request.GetCmd())
	if err != nil {
		return &proto.NodeRunResponse{
			Status: &proto.Status{
				Code:  proto.StatusCode_ERROR,
				Error: err.Error(),
			},
		}, nil
	}

	return &proto.NodeRunResponse{
		Status:   &proto.Status{Code: proto.StatusCode_OK},
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: int32(exitCode),
	}, nil
}

func (s *netemServer) NodeCopyFrom(request *proto.CopyMsg, stream proto.Netem_NodeCopyFromServer) error {
	// get project
	project := s.projects.Get(request.GetPrjId())
	if project == nil {
		return stream.Send(&proto.CopyMsg{
			Code: proto.CopyMsg_ERROR,
			Data: []byte(fmt.Sprintf("Project %s not found", request.PrjId)),
		})
	}

	// get node
	copyNode := CopyNode(project.Topology.GetNode(request.GetNode()))
	if copyNode == nil {
		return stream.Send(&proto.CopyMsg{
			Code: proto.CopyMsg_ERROR,
			Data: []byte(fmt.Sprintf("Node %s not found in project %s", request.GetNode(), request.GetPrjId())),
		})
	}

	// first copy the file into a private temp directory
	tmpDir, err := os.MkdirTemp(s.config.Workdir, "gonetem-copy-")
	if err != nil {
		return fmt.Errorf("unable to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := path.Base(request.GetNodePath())
	if fileName == "." || fileName == "/" || fileName == "" {
		fileName = "copy"
	}
	tempPath := path.Join(tmpDir, fileName)

	if err := copyNode.CopyFrom(request.GetNodePath(), tempPath); err != nil {
		return err
	}

	tempFile, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("unable to open copied file %s: %w", tempPath, err)
	}
	defer tempFile.Close()

	buffer := make([]byte, 1024)
	for {
		n, err := tempFile.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if err := stream.Send(&proto.CopyMsg{
			Code: proto.CopyMsg_DATA,
			Data: buffer[:n],
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *netemServer) NodeCopyTo(stream proto.Netem_NodeCopyToServer) error {
	var tempDir, tempPath, destPath string
	var tempFile *os.File
	var node CopyNode

	defer func() {
		if tempFile != nil {
			_ = tempFile.Close()
		}
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			if tempPath == "" || node == nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: "temp file does not exist",
					},
				})
			}

			if tempFile != nil {
				if err := tempFile.Close(); err != nil {
					return stream.SendAndClose(&proto.AckResponse{
						Status: &proto.Status{
							Code:  proto.StatusCode_ERROR,
							Error: fmt.Sprintf("unable to close temp file: %v", err),
						},
					})
				}
				tempFile = nil
			}

			if err := node.CopyTo(tempPath, destPath); err != nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: err.Error(),
					},
				})
			}

			return stream.SendAndClose(&proto.AckResponse{
				Status: &proto.Status{
					Code: proto.StatusCode_OK,
				},
			})
		}
		if err != nil {
			return err
		}

		switch msg.Code {
		case proto.CopyMsg_INIT:
			if tempFile != nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: "INIT received twice",
					},
				})
			}

			project := s.projects.Get(msg.GetPrjId())
			if project == nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: "project not found",
					},
				})
			}

			node = project.Topology.GetNode(msg.GetNode())
			if node == nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: fmt.Sprintf("node %s not found", msg.GetNode()),
					},
				})
			}

			destPath = msg.GetNodePath()

			tempDir, err = os.MkdirTemp(s.config.Workdir, "gonetem-copy-")
			if err != nil {
				tempDir = ""
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: fmt.Sprintf("unable to create temp directory: %v", err),
					},
				})
			}

			tempFile, err = os.CreateTemp(tempDir, "upload-")
			if err != nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: fmt.Sprintf("unable to create temp file: %v", err),
					},
				})
			}
			tempPath = tempFile.Name()

		case proto.CopyMsg_DATA:
			if tempFile == nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: "DATA received before INIT",
					},
				})
			}
			if _, err := tempFile.Write(msg.GetData()); err != nil {
				return stream.SendAndClose(&proto.AckResponse{
					Status: &proto.Status{
						Code:  proto.StatusCode_ERROR,
						Error: fmt.Sprintf("unable to write temp file: %v", err),
					},
				})
			}
		}
	}
}
