package console

import (
	"context"
	"testing"

	"github.com/satcomprojects/gonetem/internal/proto"
	"google.golang.org/grpc"
)

type fakePromptClient struct {
	proto.NetemClient
	startedNode string
}

func (f *fakePromptClient) NodeStart(_ context.Context, request *proto.NodeRequest, _ ...grpc.CallOption) (*proto.AckResponse, error) {
	f.startedNode = request.GetNode()
	return &proto.AckResponse{Status: &proto.Status{Code: proto.StatusCode_OK}}, nil
}

func TestPromptCommandHandlerRunsWithoutServer(t *testing.T) {
	client := &fakePromptClient{}
	prompt := &NetemPrompt{
		prjID:    "prj",
		commands: NewCommandRegistry(),
		connect: func(string) (*NetemConsoleClient, error) {
			return &NetemConsoleClient{Client: client}, nil
		},
	}
	prompt.RegisterCommands()

	prompt.Execute("start R1")
	if client.startedNode != "R1" {
		t.Fatalf("NodeStart node = %q, want R1", client.startedNode)
	}
}
