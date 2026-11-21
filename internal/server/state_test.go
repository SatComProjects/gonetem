package server

import (
	"context"
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/proto"
)

type fakeIfNode struct {
	fakeStartNode
}

func (n *fakeIfNode) SetInterfaceState(int, link.IfState) error {
	return nil
}

func TestNodeSetIfStateRejectsUnknownState(t *testing.T) {
	node := &fakeIfNode{fakeStartNode: fakeStartNode{name: "R1"}}
	server, prjID := registerCopyProject(t, node)

	_, err := server.NodeSetIfState(context.Background(), &proto.NodeIfStateRequest{
		PrjId:   prjID,
		Node:    "R1",
		IfIndex: 0,
		State:   proto.IfState(99),
	})
	if err == nil {
		t.Fatal("NodeSetIfState accepted an unknown state")
	}
}
