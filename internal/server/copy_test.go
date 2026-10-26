package server

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/mroy31/gonetem/internal/proto"
	"google.golang.org/grpc/metadata"
)

type fakeCopyNode struct {
	fakeStartNode
	copyFromCalled bool
	copyToCalled   bool
	copiedData     string
}

func (n *fakeCopyNode) CopyFrom(string, string) error {
	n.copyFromCalled = true
	return nil
}

func (n *fakeCopyNode) CopyTo(src, _ string) error {
	n.copyToCalled = true
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	n.copiedData = string(data)
	return nil
}

type copyToStreamStub struct {
	messages []*proto.CopyMsg
	index    int
	ack      *proto.AckResponse
}

func (s *copyToStreamStub) SendAndClose(ack *proto.AckResponse) error {
	s.ack = ack
	return nil
}
func (s *copyToStreamStub) Recv() (*proto.CopyMsg, error) {
	if s.index >= len(s.messages) {
		return nil, io.EOF
	}
	msg := s.messages[s.index]
	s.index++
	return msg, nil
}
func (s *copyToStreamStub) SetHeader(metadata.MD) error  { return nil }
func (s *copyToStreamStub) SendHeader(metadata.MD) error { return nil }
func (s *copyToStreamStub) SetTrailer(metadata.MD)       {}
func (s *copyToStreamStub) Context() context.Context     { return context.Background() }
func (s *copyToStreamStub) SendMsg(any) error            { return nil }
func (s *copyToStreamStub) RecvMsg(any) error            { return nil }

type copyFromStreamStub struct {
	messages []*proto.CopyMsg
	err      error
}

func (s *copyFromStreamStub) Send(msg *proto.CopyMsg) error {
	s.messages = append(s.messages, msg)
	return nil
}
func (s *copyFromStreamStub) SetHeader(metadata.MD) error  { return nil }
func (s *copyFromStreamStub) SendHeader(metadata.MD) error { return nil }
func (s *copyFromStreamStub) SetTrailer(metadata.MD)       {}
func (s *copyFromStreamStub) Context() context.Context     { return context.Background() }
func (s *copyFromStreamStub) SendMsg(any) error            { return nil }
func (s *copyFromStreamStub) RecvMsg(any) error            { return nil }

func registerCopyProject(t *testing.T, node INetemNode) string {
	t.Helper()

	prjID := "copytest"
	openProjects[prjID] = &NetemProject{
		Id:       prjID,
		Name:     prjID,
		Topology: &NetemTopologyManager{nodes: []NetemNode{{Instance: node}}},
	}
	t.Cleanup(func() {
		projectsMu.Lock()
		delete(openProjects, prjID)
		projectsMu.Unlock()
	})
	return prjID
}

func TestNodeCopyToRejectsDataBeforeInit(t *testing.T) {
	stream := &copyToStreamStub{
		messages: []*proto.CopyMsg{
			{Code: proto.CopyMsg_DATA, Data: []byte("data")},
		},
	}

	if err := (&netemServer{}).NodeCopyTo(stream); err != nil {
		t.Fatalf("NodeCopyTo returned an error: %v", err)
	}
	if stream.ack == nil || stream.ack.GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatalf("expected an error status, got %#v", stream.ack)
	}
}

func TestNodeCopyToCopiesFile(t *testing.T) {
	node := &fakeCopyNode{fakeStartNode: fakeStartNode{name: "R1"}}
	prjID := registerCopyProject(t, node)

	stream := &copyToStreamStub{
		messages: []*proto.CopyMsg{
			{Code: proto.CopyMsg_INIT, PrjId: prjID, Node: "R1", NodePath: "/tmp/dest"},
			{Code: proto.CopyMsg_DATA, Data: []byte("hello")},
		},
	}

	if err := (&netemServer{}).NodeCopyTo(stream); err != nil {
		t.Fatalf("NodeCopyTo returned an error: %v", err)
	}
	if stream.ack == nil || stream.ack.GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("expected an OK status, got %#v", stream.ack)
	}
	if !node.copyToCalled || node.copiedData != "hello" {
		t.Fatalf("CopyTo was not called with the uploaded data: called=%v data=%q", node.copyToCalled, node.copiedData)
	}
}

func TestNodeCopyFromMissingFileReturnsError(t *testing.T) {
	node := &fakeCopyNode{fakeStartNode: fakeStartNode{name: "R1"}}
	prjID := registerCopyProject(t, node)

	stream := &copyFromStreamStub{}
	err := (&netemServer{}).NodeCopyFrom(&proto.CopyMsg{
		PrjId:    prjID,
		Node:     "R1",
		NodePath: "/tmp/missing",
	}, stream)
	if err == nil {
		t.Fatal("NodeCopyFrom unexpectedly succeeded without creating a temp file")
	}
	if !node.copyFromCalled {
		t.Fatal("CopyFrom was not called")
	}
}
