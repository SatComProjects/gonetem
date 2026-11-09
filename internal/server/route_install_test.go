package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/proto"
)

func registerRouteProject(t *testing.T, nodes ...INetemNode) string {
	t.Helper()

	prjID := "routetest"
	netemNodes := make([]NetemNode, len(nodes))
	for idx, node := range nodes {
		netemNodes[idx] = NetemNode{Instance: node}
	}

	projectsMu.Lock()
	openProjects[prjID] = &NetemProject{
		Id:       prjID,
		Name:     prjID,
		Topology: &NetemTopologyManager{nodes: netemNodes},
	}
	projectsMu.Unlock()
	t.Cleanup(func() {
		projectsMu.Lock()
		delete(openProjects, prjID)
		projectsMu.Unlock()
	})
	return prjID
}

type fakeRouteNode struct {
	fakeStartNode
	nodeType string
	running  bool
}

func (n *fakeRouteNode) GetType() string { return n.nodeType }
func (n *fakeRouteNode) IsRunning() bool { return n.running }

func routeBatch(dst ...string) *proto.RouteBatch {
	routes := make([]*proto.RouteSpec, 0, len(dst))
	for _, destination := range dst {
		routes = append(routes, &proto.RouteSpec{
			Action:      proto.RouteAction_ROUTE_REPLACE,
			Destination: destination,
			Gateway:     "192.0.2.1",
			Device:      "eth0",
		})
	}
	return &proto.RouteBatch{Routes: routes}
}

func TestApplyRoutesToNodeRejectsUnsupportedOrStoppedNodes(t *testing.T) {
	specs := []link.RouteSpec{{Action: link.RouteReplace}}

	if _, err := applyRoutesToNode(context.Background(), &fakeRouteNode{
		fakeStartNode: fakeStartNode{name: "SW1"},
		nodeType:      "ovs",
		running:       true,
	}, specs); err == nil {
		t.Fatal("OVS node accepted route installation")
	}

	if _, err := applyRoutesToNode(context.Background(), &fakeRouteNode{
		fakeStartNode: fakeStartNode{name: "R1"},
		nodeType:      "docker",
		running:       false,
	}, specs); err == nil {
		t.Fatal("stopped node accepted route installation")
	}
}

func TestRouteInstallAppliesBatchesWithBoundedConcurrency(t *testing.T) {
	previous := options.ServerConfig.Docker.RouteConcurrency
	options.ServerConfig.Docker.RouteConcurrency = 8
	defer func() { options.ServerConfig.Docker.RouteConcurrency = previous }()

	prjID := registerRouteProject(t,
		&fakeStartNode{name: "R1"},
		&fakeStartNode{name: "R2"},
		&fakeStartNode{name: "R3"},
		&fakeStartNode{name: "R4"},
	)

	var active, maxActive atomic.Int32
	apply := func(_ context.Context, node INetemNode, specs []link.RouteSpec) (int, error) {
		current := active.Add(1)
		for {
			observed := maxActive.Load()
			if current <= observed || maxActive.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		if node.GetName() == "R2" {
			return 0, errors.New("synthetic route failure")
		}
		return len(specs), nil
	}

	response, err := (&netemServer{routeApplier: apply}).RouteInstall(context.Background(), &proto.RouteInstallRequest{
		PrjId:          prjID,
		MaxConcurrency: 2,
		Nodes: map[string]*proto.RouteBatch{
			"R1": routeBatch("10.0.0.0/24"),
			"R2": routeBatch("10.1.0.0/24"),
			"R3": routeBatch("10.2.0.0/24"),
			"R4": routeBatch("10.3.0.0/24"),
		},
	})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if got := maxActive.Load(); got > 2 {
		t.Fatalf("observed %d concurrent route batches, limit was 2", got)
	}
	if len(response.GetResults()) != 4 {
		t.Fatalf("expected 4 results, got %d", len(response.GetResults()))
	}
	if response.GetResults()["R1"].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("R1 failed: %v", response.GetResults()["R1"].GetStatus().GetError())
	}
	if response.GetResults()["R2"].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatal("R2 should have failed")
	}
	if response.GetResults()["R3"].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("R3 failed: %v", response.GetResults()["R3"].GetStatus().GetError())
	}
	if response.GetResults()["R4"].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatalf("R4 failed: %v", response.GetResults()["R4"].GetStatus().GetError())
	}
}

func TestRouteInstallReportsMissingNodeWithoutAborting(t *testing.T) {
	prjID := registerRouteProject(t, &fakeStartNode{name: "R1"})

	called := 0
	apply := func(_ context.Context, _ INetemNode, specs []link.RouteSpec) (int, error) {
		called++
		return len(specs), nil
	}

	response, err := (&netemServer{routeApplier: apply}).RouteInstall(context.Background(), &proto.RouteInstallRequest{
		PrjId: prjID,
		Nodes: map[string]*proto.RouteBatch{
			"R1":      routeBatch("10.0.0.0/24"),
			"missing": routeBatch("10.1.0.0/24"),
		},
	})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if called != 1 {
		t.Fatalf("route applier called %d times, want 1", called)
	}
	if response.GetResults()["missing"].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatal("missing node was not reported")
	}
	if response.GetResults()["R1"].GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatal("existing node was not applied")
	}
}

func TestRouteInstallRejectsInvalidSpecBeforeApplying(t *testing.T) {
	prjID := registerRouteProject(t, &fakeStartNode{name: "R1"})

	called := false
	apply := func(_ context.Context, _ INetemNode, _ []link.RouteSpec) (int, error) {
		called = true
		return 0, nil
	}

	response, err := (&netemServer{routeApplier: apply}).RouteInstall(context.Background(), &proto.RouteInstallRequest{
		PrjId: prjID,
		Nodes: map[string]*proto.RouteBatch{
			"R1": routeBatch("not-a-cidr"),
		},
	})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if called {
		t.Fatal("invalid route was passed to the applier")
	}
	if response.GetResults()["R1"].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatal("invalid route was not reported")
	}
}

func TestRouteInstallHonorsCanceledContext(t *testing.T) {
	prjID := registerRouteProject(t, &fakeStartNode{name: "R1"})

	called := false
	apply := func(_ context.Context, _ INetemNode, _ []link.RouteSpec) (int, error) {
		called = true
		return 0, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err := (&netemServer{routeApplier: apply}).RouteInstall(ctx, &proto.RouteInstallRequest{
		PrjId: prjID,
		Nodes: map[string]*proto.RouteBatch{
			"R1": routeBatch("10.0.0.0/24"),
		},
	})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if called {
		t.Fatal("route applier was called after cancellation")
	}
	if response.GetResults()["R1"].GetStatus().GetCode() != proto.StatusCode_ERROR {
		t.Fatal("canceled route was not reported")
	}
}

func TestRouteInstallEmptyRequest(t *testing.T) {
	prjID := registerRouteProject(t)

	response, err := (&netemServer{}).RouteInstall(context.Background(), &proto.RouteInstallRequest{PrjId: prjID})
	if err != nil {
		t.Fatalf("RouteInstall returned an error: %v", err)
	}
	if response.GetStatus().GetCode() != proto.StatusCode_OK {
		t.Fatal("empty request was not accepted")
	}
	if len(response.GetResults()) != 0 {
		t.Fatal("empty request produced results")
	}
}

func TestRouteInstallLimit(t *testing.T) {
	previous := options.ServerConfig.Docker.RouteConcurrency
	defer func() { options.ServerConfig.Docker.RouteConcurrency = previous }()

	options.ServerConfig.Docker.RouteConcurrency = 8
	if got := routeInstallLimit(0); got != 8 {
		t.Fatalf("server default = %d, want 8", got)
	}
	if got := routeInstallLimit(4); got != 4 {
		t.Fatalf("requested lower limit = %d, want 4", got)
	}
	if got := routeInstallLimit(16); got != 8 {
		t.Fatalf("requested higher limit = %d, want 8", got)
	}

	options.ServerConfig.Docker.RouteConcurrency = 0
	if got := routeInstallLimit(0); got != defaultRouteConcurrency {
		t.Fatalf("fallback default = %d, want %d", got, defaultRouteConcurrency)
	}
}
