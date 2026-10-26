package server

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/proto"
	"google.golang.org/grpc/metadata"
)

type fakeExecNode struct {
	fakeStartNode
}

func (n *fakeExecNode) ExecCommand(ctx context.Context, _ []string, in io.ReadCloser, _ io.Writer, _ bool, _ uint, _ uint, _ chan term.Winsize) error {
	_, err := io.Copy(io.Discard, in)
	return err
}

type execStreamStub struct {
	messages []*proto.ExecCltMsg
	index    int
	sent     []*proto.ExecSrvMsg
}

func (s *execStreamStub) Send(msg *proto.ExecSrvMsg) error {
	s.sent = append(s.sent, msg)
	return nil
}
func (s *execStreamStub) Recv() (*proto.ExecCltMsg, error) {
	if s.index >= len(s.messages) {
		return nil, io.EOF
	}
	msg := s.messages[s.index]
	s.index++
	return msg, nil
}
func (s *execStreamStub) SetHeader(metadata.MD) error  { return nil }
func (s *execStreamStub) SendHeader(metadata.MD) error { return nil }
func (s *execStreamStub) SetTrailer(metadata.MD)       {}
func (s *execStreamStub) Context() context.Context     { return context.Background() }
func (s *execStreamStub) SendMsg(any) error            { return nil }
func (s *execStreamStub) RecvMsg(any) error            { return nil }

func TestNodeExecReturnsWhenClientClosesInput(t *testing.T) {
	node := &fakeExecNode{fakeStartNode: fakeStartNode{name: "R1"}}
	prjID := registerCopyProject(t, node)

	stream := &execStreamStub{
		messages: []*proto.ExecCltMsg{
			{Code: proto.ExecCltMsg_CMD, PrjId: prjID, Node: "R1", Cmd: []string{"true"}},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- (&netemServer{}).NodeExec(stream)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("NodeExec returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("NodeExec did not return after the client input closed")
	}
}
