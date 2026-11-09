package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/proto"
	"golang.org/x/sync/errgroup"
)

const defaultRouteConcurrency = 32

// routeInstallLimit returns the number of node batches that may run in
// parallel. A client-provided value can only lower the server-side limit.
func routeInstallLimit(requested int) int {
	configured := options.ServerConfig.Docker.RouteConcurrency
	if configured <= 0 {
		configured = defaultRouteConcurrency
	}
	if requested <= 0 || requested > configured {
		return configured
	}
	return requested
}

func (s *netemServer) applyNodeRoutes(ctx context.Context, node INetemNode, specs []link.RouteSpec) (int, error) {
	if s.routeApplier != nil {
		return s.routeApplier(ctx, node, specs)
	}
	return applyRoutesToNode(ctx, node, specs)
}

func applyRoutesToNode(ctx context.Context, node INetemNode, specs []link.RouteSpec) (int, error) {
	if node.GetType() == "ovs" {
		return 0, fmt.Errorf("route installation is not supported on OVS node %s", node.GetName())
	}
	if !node.IsRunning() {
		return 0, fmt.Errorf("node %s is not running", node.GetName())
	}

	ns, err := node.GetNetns()
	if err != nil {
		return 0, fmt.Errorf("unable to get network namespace of node %s: %w", node.GetName(), err)
	}
	defer ns.Close()
	if !ns.IsOpen() {
		return 0, fmt.Errorf("node %s returned an invalid network namespace", node.GetName())
	}

	return link.ApplyRoutes(ctx, ns, specs)
}

// RouteInstall applies a batch of route changes to several nodes. Per-node
// failures are reported in the response and do not abort the other nodes.
func (s *netemServer) RouteInstall(ctx context.Context, request *proto.RouteInstallRequest) (*proto.RouteInstallResponse, error) {
	project := ProjectGetOne(request.GetPrjId())
	if project == nil {
		return nil, &ProjectNotFoundError{request.GetPrjId()}
	}

	batches := request.GetNodes()
	results := make(map[string]*proto.RouteInstallResult, len(batches))
	for name := range batches {
		results[name] = &proto.RouteInstallResult{
			Status: &proto.Status{
				Code:  proto.StatusCode_ERROR,
				Error: "route installation was not executed",
			},
		}
	}

	if len(batches) == 0 {
		return &proto.RouteInstallResponse{
			Status:  &proto.Status{Code: proto.StatusCode_OK},
			Results: results,
		}, nil
	}

	limit := routeInstallLimit(int(request.GetMaxConcurrency()))
	group := new(errgroup.Group)
	group.SetLimit(limit)

	var mu sync.Mutex
	for name, batch := range batches {
		name, batch := name, batch
		group.Go(func() error {
			result := s.installNodeRoutes(ctx, project, name, batch)
			mu.Lock()
			results[name] = result
			mu.Unlock()
			return nil
		})
	}
	_ = group.Wait()

	return &proto.RouteInstallResponse{
		Status:  &proto.Status{Code: proto.StatusCode_OK},
		Results: results,
	}, nil
}

func (s *netemServer) installNodeRoutes(ctx context.Context, project *NetemProject, name string, batch *proto.RouteBatch) *proto.RouteInstallResult {
	start := time.Now()
	result := &proto.RouteInstallResult{
		Status: &proto.Status{Code: proto.StatusCode_ERROR},
	}
	defer func() {
		result.DurationMicros = time.Since(start).Microseconds()
	}()

	if err := ctx.Err(); err != nil {
		result.Status.Error = err.Error()
		return result
	}

	node := project.Topology.GetNode(name)
	if node == nil {
		result.Status.Error = (&NodeNotFoundError{prjId: project.Id, name: name}).Error()
		return result
	}

	specs, err := routeSpecsFromProto(batch)
	if err != nil {
		result.Status.Error = err.Error()
		return result
	}

	if len(specs) == 0 {
		result.Status.Code = proto.StatusCode_OK
		return result
	}

	applied, err := s.applyNodeRoutes(ctx, node, specs)
	result.Applied = uint32(applied)
	if err != nil {
		result.Status.Error = err.Error()
		return result
	}

	result.Status.Code = proto.StatusCode_OK
	return result
}

func routeSpecsFromProto(batch *proto.RouteBatch) ([]link.RouteSpec, error) {
	if batch == nil {
		return nil, nil
	}

	specs := make([]link.RouteSpec, 0, len(batch.GetRoutes()))
	for idx, route := range batch.GetRoutes() {
		_, dst, err := net.ParseCIDR(route.GetDestination())
		if err != nil {
			return nil, fmt.Errorf("route %d: invalid destination %q: %w", idx, route.GetDestination(), err)
		}
		if route.GetDevice() == "" {
			return nil, fmt.Errorf("route %d: device is required", idx)
		}

		var gateway net.IP
		if route.GetGateway() != "" {
			gateway = net.ParseIP(route.GetGateway())
			if gateway == nil {
				return nil, fmt.Errorf("route %d: invalid gateway %q", idx, route.GetGateway())
			}
		}

		action := link.RouteReplace
		switch route.GetAction() {
		case proto.RouteAction_ROUTE_REPLACE:
			action = link.RouteReplace
		case proto.RouteAction_ROUTE_DELETE:
			action = link.RouteDelete
		default:
			return nil, fmt.Errorf("route %d: unsupported action %d", idx, route.GetAction())
		}

		specs = append(specs, link.RouteSpec{
			Action: action,
			Dst:    dst,
			Gw:     gateway,
			Device: route.GetDevice(),
		})
	}

	return specs, nil
}
