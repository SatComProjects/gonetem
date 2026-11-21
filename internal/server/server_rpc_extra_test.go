package server

import (
	"context"
	"io"
	"testing"

	"github.com/satcomprojects/gonetem/internal/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestRPCProjectErrorsAndBatchResults(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client := proto.NewNetemClient(conn)

	if _, err := client.ProjectGetStatus(ctx, &proto.ProjectRequest{Id: "missing"}); err == nil {
		t.Fatal("ProjectGetStatus accepted a missing project")
	}
	if _, err := client.ProjectGetNodeConfigs(ctx, &proto.ProjectRequest{Id: "missing"}); err == nil {
		t.Fatal("ProjectGetNodeConfigs accepted a missing project")
	}
	if _, err := client.ProjectOpen(ctx, &proto.OpenRequest{Name: "bad", Data: []byte("not-an-archive")}); err == nil {
		t.Fatal("ProjectOpen accepted an invalid archive")
	}

	open, err := client.ProjectOpen(ctx, &proto.OpenRequest{
		Name: "rpc-extra.gnet",
		Data: createProjectArchive(t, namespaceNetwork),
	})
	if err != nil {
		t.Fatalf("ProjectOpen returned an error: %v", err)
	}
	prjID := open.GetId()

	status, err := client.ProjectGetStatus(ctx, &proto.ProjectRequest{Id: prjID})
	if err != nil || status.GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("ProjectGetStatus = %v, %v", status, err)
	}
	if len(status.GetNodes()) != 3 {
		t.Fatalf("status nodes = %d, want 3", len(status.GetNodes()))
	}

	if _, err := client.NodeAdd(ctx, &proto.NodeAddRequest{
		PrjId: prjID,
		Node:  "bad",
		Config: &proto.NodeConfigMsg{
			Type: "not-a-node-type",
		},
	}); err == nil {
		t.Fatal("NodeAdd accepted an invalid node type")
	}

	routeResponse, err := client.RouteInstall(ctx, &proto.RouteInstallRequest{
		PrjId: prjID,
		Nodes: map[string]*proto.RouteBatch{
			"n1": {Routes: []*proto.RouteSpec{{
				Action:      proto.RouteAction_ROUTE_REPLACE,
				Destination: "not-a-cidr",
				Gateway:     "192.0.2.1",
				Device:      "eth0",
			}}},
		},
	})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if routeResponse.GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("top-level batch status = %v, want OK", routeResponse.GetStatus())
	}
	result := routeResponse.GetResults()["n1"]
	if result == nil || result.GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatalf("partial failure was not reported: %#v", result)
	}

	configs, err := client.ProjectGetNodeConfigs(ctx, &proto.ProjectRequest{Id: prjID})
	if err != nil || len(configs.GetData()) == 0 {
		t.Fatalf("ProjectGetNodeConfigs returned %d bytes, err %v", len(configs.GetData()), err)
	}

	if _, err := client.ProjectGetMany(ctx, &emptypb.Empty{}); err != nil {
		t.Fatalf("ProjectGetMany returned an error: %v", err)
	}

	closeStream, err := client.ProjectClose(ctx, &proto.ProjectRequest{Id: prjID})
	if err != nil {
		t.Fatalf("ProjectClose returned an error: %v", err)
	}
	for {
		_, err := closeStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ProjectClose stream returned an error: %v", err)
		}
	}
	if _, err := client.ProjectGetStatus(ctx, &proto.ProjectRequest{Id: prjID}); err == nil {
		t.Fatal("closed project is still visible")
	}
}
