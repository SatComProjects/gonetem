package link

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// RouteAction selects the operation applied to one route.
type RouteAction int

const (
	RouteReplace RouteAction = iota
	RouteDelete
)

// RouteSpec is the typed equivalent of:
//
//	ip route replace <destination> via <gateway> dev <device>
//
// Gateway may be nil for an on-link route. Destination is always a CIDR.
type RouteSpec struct {
	Action RouteAction
	Dst    *net.IPNet
	Gw     net.IP
	Device string
}

func (r RouteSpec) String() string {
	action := "replace"
	if r.Action == RouteDelete {
		action = "del"
	}

	dst := "<nil>"
	if r.Dst != nil {
		dst = r.Dst.String()
	}

	if r.Gw == nil {
		return fmt.Sprintf("route %s %s dev %s", action, dst, r.Device)
	}
	return fmt.Sprintf("route %s %s via %s dev %s", action, dst, r.Gw, r.Device)
}

// ApplyRoutes applies a batch of route operations inside ns. It opens one
// netlink handle for the namespace, resolves each device once, and attempts
// every operation even when an earlier one fails. The returned count is the
// number of operations that succeeded.
func ApplyRoutes(ctx context.Context, ns netns.NsHandle, specs []RouteSpec) (int, error) {
	if len(specs) == 0 {
		return 0, nil
	}

	handle, err := netlink.NewHandleAt(ns, unix.NETLINK_ROUTE)
	if err != nil {
		return 0, fmt.Errorf("unable to open netlink handle in node namespace: %w", err)
	}
	defer handle.Close()

	var (
		indexes  = make(map[string]int)
		failures []error
		applied  int
	)

	for idx, spec := range specs {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}

		if err := validateRouteSpec(spec); err != nil {
			failures = append(failures, fmt.Errorf("route %d (%s): %w", idx, spec, err))
			continue
		}

		linkIndex, err := routeLinkIndex(handle, spec.Device, indexes)
		if err != nil {
			failures = append(failures, fmt.Errorf("route %d (%s): %w", idx, spec, err))
			continue
		}

		route := &netlink.Route{
			LinkIndex: linkIndex,
			Dst:       spec.Dst,
			Gw:        spec.Gw,
			Family:    routeFamily(spec.Dst),
			Table:     unix.RT_TABLE_MAIN,
			Scope:     routeScope(spec),
			Protocol:  unix.RTPROT_BOOT,
		}

		switch spec.Action {
		case RouteReplace:
			err = handle.RouteReplace(route)
		case RouteDelete:
			err = deleteRoute(handle, route)
		default:
			err = fmt.Errorf("unsupported route action %d", spec.Action)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("route %d (%s): %w", idx, spec, err))
			continue
		}

		applied++
	}

	if len(failures) > 0 {
		return applied, errors.Join(failures...)
	}
	return applied, nil
}

func validateRouteSpec(spec RouteSpec) error {
	if spec.Dst == nil {
		return errors.New("destination is required")
	}
	if spec.Device == "" {
		return errors.New("device is required")
	}
	if spec.Action != RouteReplace && spec.Action != RouteDelete {
		return fmt.Errorf("unsupported route action %d", spec.Action)
	}

	dstV4 := spec.Dst.IP.To4() != nil
	if spec.Gw != nil && (spec.Gw.To4() != nil) != dstV4 {
		return errors.New("gateway and destination address families differ")
	}
	return nil
}

func routeFamily(dst *net.IPNet) int {
	if dst.IP.To4() != nil {
		return unix.AF_INET
	}
	return unix.AF_INET6
}

func routeScope(spec RouteSpec) netlink.Scope {
	if spec.Gw == nil {
		return netlink.SCOPE_LINK
	}
	return netlink.SCOPE_UNIVERSE
}

func routeLinkIndex(handle *netlink.Handle, name string, indexes map[string]int) (int, error) {
	if idx, found := indexes[name]; found {
		return idx, nil
	}

	link, err := handle.LinkByName(name)
	if err != nil {
		return -1, fmt.Errorf("unable to find device %s: %w", name, err)
	}

	idx := link.Attrs().Index
	indexes[name] = idx
	return idx, nil
}

// deleteRoute first tries an exact delete. If the exact attributes do not
// match (for example because the route was installed by an older gonetem
// version), it lists routes with the same destination/device and deletes the
// matching entry. A route that is already absent is not an error.
func deleteRoute(handle *netlink.Handle, route *netlink.Route) error {
	err := handle.RouteDel(route)
	if err == nil || errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
		return nil
	}

	filter := &netlink.Route{
		LinkIndex: route.LinkIndex,
		Dst:       route.Dst,
		Family:    route.Family,
		Table:     route.Table,
	}
	routes, listErr := handle.RouteListFiltered(
		route.Family,
		filter,
		netlink.RT_FILTER_OIF|netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE,
	)
	if listErr != nil {
		return fmt.Errorf("unable to list routes after delete failed: %w", listErr)
	}

	for i := range routes {
		candidate := routes[i]
		if !sameRoute(&candidate, route) {
			continue
		}
		if delErr := handle.RouteDel(&candidate); delErr != nil &&
			!errors.Is(delErr, unix.ESRCH) && !errors.Is(delErr, unix.ENOENT) {
			return delErr
		}
		return nil
	}

	return nil
}

func sameRoute(a, b *netlink.Route) bool {
	if a.LinkIndex != b.LinkIndex || a.Table != b.Table {
		return false
	}
	if (a.Dst == nil) != (b.Dst == nil) {
		return false
	}
	if a.Dst != nil && a.Dst.String() != b.Dst.String() {
		return false
	}
	return a.Gw.Equal(b.Gw)
}
