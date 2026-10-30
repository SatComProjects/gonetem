package link

import (
	"errors"
	"fmt"
	"time"

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

func formatTime(t int) (uint32, error) {
	if t < 0 {
		return 0, fmt.Errorf("negative time value: %d", t)
	}

	tcTime, err := core.Duration2TcTime(time.Duration(t) * time.Millisecond)
	if err != nil {
		return 0, fmt.Errorf("unable to convert %dms to tc time: %w", t, err)
	}
	return core.Time2Tick(tcTime), nil
}

// QoS groups the parameters needed to configure one link direction.
type QoS struct {
	Delay  int
	Jitter int
	Loss   float64
	Rate   int
	Buffer float64
	Burst  int
}

func tbfQdisc(devID netlink.Link, delay, rate int, bufFactor float64, burst int) tc.Object {
	linklayerEthernet := uint8(1)

	tbfBurst := uint32(rate * 4 / 8.0) // rate (in bps) / 250 HZ
	if burst > 0 {
		tbfBurst = uint32(burst)
	}

	limit := uint32(bufFactor * float64(rate) * float64(delay) / 8.0) // rate * latency * BDPFactor
	if limit == 0 && rate > 0 {
		// A zero limit is not a usable TBF; hold at least one MTU.
		limit = 1514
	}

	return tc.Object{
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
}

func netemQdisc(devID netlink.Link, delay int, jitter int, loss float64, handle uint32, parent uint32) (tc.Object, error) {
	latency, err := formatTime(delay)
	if err != nil {
		return tc.Object{}, err
	}
	jitterTicks, err := formatTime(jitter)
	if err != nil {
		return tc.Object{}, err
	}

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
					Latency: latency,
					Jitter:  jitterTicks,
					Limit:   1000,
					Loss:    formatPercent(loss),
				},
			},
		},
	}, nil
}

// Netem installs a netem qdisc on the given interface.
// When both rate limiting (tbf) and netem are required, netem must be attached
// as a child of the tbf inner queue (parent TbfInnerParent) rather than at the
// root, because Linux only allows one root qdisc per interface.
func Netem(ifname string, namespace netns.NsHandle, delay int, jitter int, loss float64, change bool, parent uint32, handle uint32) error {
	return withNetns(namespace, func() error {
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

		qdisc, err := netemQdisc(devID, delay, jitter, loss, handle, parent)
		if err != nil {
			return err
		}
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
	})
}

func CreateTbf(ifname string, namespace netns.NsHandle, delay, rate int, bufFactor float64, burst int, change bool) error {
	return withNetns(namespace, func() error {
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
		if limit == 0 && rate > 0 {
			// A zero limit is not a usable TBF; hold at least one MTU.
			limit = 1514
		}

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
	})
}

// ApplyQoS installs or updates the qdiscs for one link direction in a single
// namespace transition and a single rtnetlink session.
func ApplyQoS(ifname string, namespace netns.NsHandle, q QoS, reset, changeTbf, changeNetem bool) (hasTbf, hasNetem bool, err error) {
	err = withNetns(namespace, func() error {
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

		if reset {
			if err := resetQdiscs(devID.Attrs().Index); err != nil {
				return err
			}
			changeTbf = false
			changeNetem = false
		}

		if q.Rate > 0 {
			qdisc := tbfQdisc(devID, q.Delay+q.Jitter, q.Rate, q.Buffer, q.Burst)
			if changeTbf {
				if err := rtnl.Qdisc().Change(&qdisc); err != nil {
					return fmt.Errorf("could not change qdisc tbf to %s: %v", ifname, err)
				}
			} else if err := rtnl.Qdisc().Add(&qdisc); err != nil {
				return fmt.Errorf("could not assign qdisc tbf to %s: %v", ifname, err)
			}
			hasTbf = true
		}

		if q.Delay > 0 || q.Jitter > 0 || q.Loss > 0 {
			parent, handle := RootParent, RootHandle
			if hasTbf {
				parent = TbfInnerParent
				handle = NetemChildHandle
			}

			qdisc, err := netemQdisc(devID, q.Delay, q.Jitter, q.Loss, handle, parent)
			if err != nil {
				return err
			}
			if changeNetem {
				if err := rtnl.Qdisc().Change(&qdisc); err != nil {
					return fmt.Errorf("could not change qdisc netem on %s: %v", ifname, err)
				}
			} else if err := rtnl.Qdisc().Add(&qdisc); err != nil {
				return fmt.Errorf("could not assign qdisc netem to %s: %v", ifname, err)
			}
			hasNetem = true
		}

		return nil
	})

	return hasTbf, hasNetem, err
}

func resetQdiscs(ifIndex int) error {
	// All gonetem qdisc trees are rooted at handle 1:0. Deleting the root
	// qdisc also deletes its child netem qdisc, so no qdisc dump is needed.
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: ifIndex,
			Handle:    RootHandle,
			Parent:    tc.HandleRoot,
		},
	}

	err := netlink.QdiscDel(qdisc)
	if err == nil || errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EINVAL) {
		return nil
	}
	return fmt.Errorf("could not reset qdiscs on interface %d: %w", ifIndex, err)
}

// ResetQdiscs removes all configured qdiscs from the given interface.
// It is used before re-applying QoS so that transitions between standalone
// netem, standalone tbf and chained tbf+netem are handled cleanly.
func ResetQdiscs(ifname string, namespace netns.NsHandle) error {
	return withNetns(namespace, func() error {
		devID, err := netlink.LinkByName(ifname)
		if err != nil {
			return fmt.Errorf("could not get interface ID for %s: %v", ifname, err)
		}
		return resetQdiscs(devID.Attrs().Index)
	})
}
