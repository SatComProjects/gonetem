package link

import (
	"fmt"
	"time"

	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	uint32Max uint32 = 4294967295

	// Traffic control handle layout: major:minor. Qdiscs use minor 0.
	rootHandleMajor = 0x1
	netemChildMajor = 0x2
	tbfInnerMinor   = 0x1
)

// Handle values are functions rather than mutable package variables. The
// builder is cheap and keeps the package free of shared writable state.
func rootParent() uint32 { return tc.HandleRoot }

func rootHandle() uint32 { return core.BuildHandle(rootHandleMajor, 0x0) }

func tbfInnerParent() uint32 { return core.BuildHandle(rootHandleMajor, tbfInnerMinor) }

func netemChildHandle() uint32 { return core.BuildHandle(netemChildMajor, 0x0) }

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

// Default queue sizes used when the request leaves them unset.
const (
	// DefaultNetemLimit is the netem queue limit in packets.
	DefaultNetemLimit = 1000
	// DefaultTxQueuelen is the interface transmit queue length in packets.
	DefaultTxQueuelen = 1000
)

// QoS groups the parameters needed to configure one link direction.
type QoS struct {
	Delay      int
	Jitter     int
	Loss       float64
	Rate       int
	Buffer     float64
	Burst      int
	Limit      int // netem queue limit in packets; <= 0 keeps the default
	TxQueuelen int // interface tx queue length in packets; <= 0 keeps the default
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
			Handle:  rootHandle(),
			Parent:  rootParent(),
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

func effectiveNetemLimit(limit int) uint32 {
	if limit <= 0 {
		return DefaultNetemLimit
	}
	return uint32(limit)
}

func effectiveTxQueuelen(qlen int) int {
	if qlen <= 0 {
		return DefaultTxQueuelen
	}
	return qlen
}

func netemQdisc(devID netlink.Link, delay int, jitter int, loss float64, limit int, handle uint32, parent uint32) (tc.Object, error) {
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
					Limit:   effectiveNetemLimit(limit),
					Loss:    formatPercent(loss),
				},
			},
		},
	}, nil
}
