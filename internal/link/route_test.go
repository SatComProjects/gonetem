package link

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func mustCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatalf("invalid CIDR %q: %v", value, err)
	}
	return network
}

func TestValidateRouteSpec(t *testing.T) {
	valid := RouteSpec{
		Action: RouteReplace,
		Dst:    mustCIDR(t, "10.0.0.0/24"),
		Gw:     net.ParseIP("192.0.2.1"),
		Device: "eth0",
	}
	if err := validateRouteSpec(valid); err != nil {
		t.Fatalf("valid route rejected: %v", err)
	}

	tests := []struct {
		name string
		spec RouteSpec
	}{
		{name: "missing destination", spec: RouteSpec{Device: "eth0"}},
		{name: "missing device", spec: RouteSpec{Dst: mustCIDR(t, "10.0.0.0/24")}},
		{name: "unknown action", spec: RouteSpec{Action: RouteAction(99), Dst: mustCIDR(t, "10.0.0.0/24"), Device: "eth0"}},
		{name: "mixed families", spec: RouteSpec{Dst: mustCIDR(t, "10.0.0.0/24"), Gw: net.ParseIP("2001:db8::1"), Device: "eth0"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRouteSpec(test.spec); err == nil {
				t.Fatal("invalid route accepted")
			}
		})
	}
}

func TestRouteSpecString(t *testing.T) {
	spec := RouteSpec{
		Action: RouteReplace,
		Dst:    mustCIDR(t, "10.0.0.0/24"),
		Gw:     net.ParseIP("192.0.2.1"),
		Device: "eth0",
	}
	if got, want := spec.String(), "route replace 10.0.0.0/24 via 192.0.2.1 dev eth0"; got != want {
		t.Fatalf("unexpected route string: %q", got)
	}

	spec.Action = RouteDelete
	spec.Gw = nil
	if got, want := spec.String(), "route del 10.0.0.0/24 dev eth0"; got != want {
		t.Fatalf("unexpected route string: %q", got)
	}
}

func TestApplyRoutesReplaceAndDelete(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Test requires root privileges.")
	}

	name := fmt.Sprintf("gonetem-route-test-%d", os.Getpid())
	ns, err := CreateNetns(name)
	if err != nil {
		t.Fatalf("unable to create test namespace: %v", err)
	}
	defer func() {
		_ = ns.Close()
		_ = DeleteNetns(name)
	}()

	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dummy0"}}
	if err := withNetlinkHandle(ns, func(handle *netlink.Handle) error {
		if err := handle.LinkAdd(dummy); err != nil {
			return err
		}
		link, err := handle.LinkByName("dummy0")
		if err != nil {
			return err
		}
		return handle.LinkSetUp(link)
	}); err != nil {
		t.Fatalf("unable to create dummy link: %v", err)
	}

	dst := mustCIDR(t, "10.99.0.0/24")
	specs := []RouteSpec{{Action: RouteReplace, Dst: dst, Device: "dummy0"}}
	applied, err := ApplyRoutes(context.Background(), ns, specs)
	if err != nil {
		t.Fatalf("ApplyRoutes replace returned an error: %v", err)
	}
	if applied != 1 {
		t.Fatalf("unexpected applied count: %d", applied)
	}

	verify := func(want int) {
		t.Helper()
		err := withNetlinkHandle(ns, func(handle *netlink.Handle) error {
			routes, err := handle.RouteListFiltered(
				unix.AF_INET,
				&netlink.Route{Dst: dst, Table: unix.RT_TABLE_MAIN},
				netlink.RT_FILTER_DST|netlink.RT_FILTER_TABLE,
			)
			if err != nil {
				return err
			}
			if len(routes) != want {
				return fmt.Errorf("found %d routes, want %d", len(routes), want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	verify(1)

	specs[0].Action = RouteDelete
	applied, err = ApplyRoutes(context.Background(), ns, specs)
	if err != nil {
		t.Fatalf("ApplyRoutes delete returned an error: %v", err)
	}
	if applied != 1 {
		t.Fatalf("unexpected applied count: %d", applied)
	}
	verify(0)

	// Deleting an already absent route is idempotent.
	if _, err := ApplyRoutes(context.Background(), ns, specs); err != nil {
		t.Fatalf("deleting an absent route returned an error: %v", err)
	}
}
