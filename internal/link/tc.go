package link

import (
	"fmt"
	"runtime"

	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	uint32Max uint32 = 4294967295

	// Traffic control handle layout: major:minor.  Qdiscs use minor 0.
	rootHandleMajor = 0x1
	netemChildMajor = 0x2
	tbfInnerMinor   = 0x1
)

var (
	logger = logrus.WithField("module", "tc")

	// RootParent is the parent value for a root qdisc.
	RootParent = tc.HandleRoot
	// RootHandle is the handle used for the root qdisc (tbf or standalone netem).
	RootHandle = core.BuildHandle(rootHandleMajor, 0x0)
	// TbfInnerParent is the parent of the child netem qdisc when chained under tbf.
	TbfInnerParent = core.BuildHandle(rootHandleMajor, tbfInnerMinor)
	// NetemChildHandle is the handle of the netem qdisc when chained under tbf.
	NetemChildHandle = core.BuildHandle(netemChildMajor, 0x0)
)

func formatPercent(per float64) uint32 {
	perF := per / 100.0
	result := uint32(float64(uint32Max) * perF)

	return result
}

func formatTime(t int) uint32 {
	// TODO: understand why we need 15.625 factor
	return uint32(float64(t) * 1000 * 15.625)
}

func netemQdisc(devID netlink.Link, delay int, jitter int, loss float64, handle uint32, parent uint32) tc.Object {
	return tc.Object{
		Msg: tc.Msg{
			Family:  unix.AF_UNSPEC,
			Ifindex: uint32(devID.Attrs().Index),
			Handle:  handle,
			Parent:  parent,
			Info:    0,
		},
		Attribute: tc.Attribute{
			Kind: "netem",
			Netem: &tc.Netem{
				Qopt: tc.NetemQopt{
					Latency: formatTime(delay),
					Jitter:  formatTime(jitter),
					Limit:   1000,
					Loss:    formatPercent(loss),
				},
			},
		},
	}
}

// Netem installs a netem qdisc on the given interface.
// When both rate limiting (tbf) and netem are required, netem must be attached
// as a child of the tbf inner queue (parent TbfInnerParent) rather than at the
// root, because Linux only allows one root qdisc per interface.
func Netem(ifname string, namespace netns.NsHandle, delay int, jitter int, loss float64, change bool, parent uint32, handle uint32) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	netns.Set(namespace)

	// get interface ID
	devID, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("could not get interface ID for %s: %v", ifname, err)
	}

	// open a rtnetlink socket
	rtnl, err := tc.Open(&tc.Config{})
	if err != nil {
		return fmt.Errorf("could not open rtnetlink socket: %v", err)
	}
	defer func() {
		if err := rtnl.Close(); err != nil {
			logger.Errorf("Could not close rtnetlink socket: %v", err)
		}
	}()

	qdisc := netemQdisc(devID, delay, jitter, loss, handle, parent)
	if !change {
		// tc qdisc add dev ifname parent ... handle ... netem ...
		if err := rtnl.Qdisc().Add(&qdisc); err != nil {
			return fmt.Errorf("could not assign qdisc netem to %s: %v", ifname, err)
		}
	} else {
		// tc qdisc change dev ifname parent ... handle ... netem ...
		if err := rtnl.Qdisc().Change(&qdisc); err != nil {
			return fmt.Errorf("could not assign qdisc netem to %s: %v", ifname, err)
		}
	}

	return nil
}

func CreateTbf(ifname string, namespace netns.NsHandle, delay, rate int, bufFactor float64, burst int, change bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	netns.Set(namespace)

	// get interface ID
	devID, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("could not get interface ID for %s: %v", ifname, err)
	}

	// open a rtnetlink socket
	rtnl, err := tc.Open(&tc.Config{})
	if err != nil {
		return fmt.Errorf("could not open rtnetlink socket: %v", err)
	}
	defer func() {
		if err := rtnl.Close(); err != nil {
			logger.Errorf("Could not close rtnetlink socket: %v", err)
		}
	}()

	linklayerEthernet := uint8(1)
	// burst has to specified in bytes
	tbfBurst := uint32(rate * 4 / 8.0) // rate (in bps) / 250 HZ
	if burst > 0 {
		tbfBurst = uint32(burst)
	}
	// limit (as rate) has to specified in bytes
	limit := uint32(bufFactor * float64(rate) * float64(delay) / 8.0) // rate * latency * BDPFactor

	qdisc := tc.Object{
		Msg: tc.Msg{
			Family:  unix.AF_UNSPEC,
			Ifindex: uint32(devID.Attrs().Index),
			Handle:  RootHandle,
			Parent:  tc.HandleRoot,
			Info:    0,
		},
		Attribute: tc.Attribute{
			Kind: "tbf",
			Tbf: &tc.Tbf{
				Parms: &tc.TbfQopt{
					Mtu:   1514,
					Limit: limit,
					Rate: tc.RateSpec{
						Rate:      uint32(rate * 125),
						Linklayer: linklayerEthernet,
						CellLog:   0x3,
					},
				},
				Burst: &tbfBurst,
			},
		},
	}

	if !change {
		// tc qdisc add dev ifname root tbf ...
		if err := rtnl.Qdisc().Add(&qdisc); err != nil {
			return fmt.Errorf("could not assign qdisc tbf to %s: %v", ifname, err)
		}
	} else {
		// tc qdisc change dev ifname root tbf ...
		if err := rtnl.Qdisc().Change(&qdisc); err != nil {
			return fmt.Errorf("could not change qdisc tbf to %s: %v", ifname, err)
		}
	}

	return nil
}

// ResetQdiscs removes all configured qdiscs from the given interface.
// It is used before re-applying QoS so that transitions between standalone
// netem, standalone tbf and chained tbf+netem are handled cleanly.
func ResetQdiscs(ifname string, namespace netns.NsHandle) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	netns.Set(namespace)

	devID, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("could not get interface ID for %s: %v", ifname, err)
	}

	rtnl, err := tc.Open(&tc.Config{})
	if err != nil {
		return fmt.Errorf("could not open rtnetlink socket: %v", err)
	}
	defer func() {
		if err := rtnl.Close(); err != nil {
			logger.Errorf("Could not close rtnetlink socket: %v", err)
		}
	}()

	qdiscs, err := rtnl.Qdisc().Get()
	if err != nil {
		return fmt.Errorf("could not list qdiscs on %s: %v", ifname, err)
	}

	ifIndex := uint32(devID.Attrs().Index)

	// First delete non-root qdiscs (children), then root qdiscs, so that
	// deleting a parent does not cause a subsequent child deletion to fail.
	var roots []tc.Object
	var children []tc.Object
	for _, q := range qdiscs {
		if q.Ifindex != ifIndex {
			continue
		}
		// Do not try to delete the default root qdisc that the kernel reports
		// for an interface with no explicit qdisc (it has no handle).
		if q.Handle == 0 {
			continue
		}
		if q.Parent == tc.HandleRoot {
			roots = append(roots, q)
		} else {
			children = append(children, q)
		}
	}

	for _, q := range children {
		if err := rtnl.Qdisc().Delete(&q); err != nil {
			return fmt.Errorf("could not delete qdisc %s on %s: %v", q.Kind, ifname, err)
		}
	}
	for _, q := range roots {
		if err := rtnl.Qdisc().Delete(&q); err != nil {
			return fmt.Errorf("could not delete qdisc %s on %s: %v", q.Kind, ifname, err)
		}
	}

	return nil
}
