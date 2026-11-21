package link

import (
	"errors"
	"fmt"

	"github.com/florianl/go-tc"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

func tcLogger() *logrus.Entry {
	return logrus.WithField("module", "tc")
}

// Netem installs a netem qdisc on the given interface.
// When both rate limiting (tbf) and netem are required, netem must be attached
// as a child of the tbf inner queue (parent tbfInnerParent) rather than at the
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
				tcLogger().Errorf("Could not close rtnetlink socket: %v", err)
			}
		}()

		qdisc, err := netemQdisc(devID, delay, jitter, loss, 0, handle, parent)
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
				tcLogger().Errorf("Could not close rtnetlink socket: %v", err)
			}
		}()

		qdisc := tbfQdisc(devID, delay, rate, bufFactor, burst)
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

// QoSOperation describes one interface update inside ApplyQoSBatch.
type QoSOperation struct {
	IfName      string
	QoS         QoS
	Reset       bool
	ChangeTbf   bool
	ChangeNetem bool
}

// QoSOperationResult reports the qdisc state left on one interface.
type QoSOperationResult struct {
	HasTbf   bool
	HasNetem bool
	Err      error
}

// ApplyQoS installs or updates the qdiscs for one link direction in a single
// namespace transition and a single rtnetlink session.
func ApplyQoS(ifname string, namespace netns.NsHandle, q QoS, reset, changeTbf, changeNetem bool) (hasTbf, hasNetem bool, err error) {
	results, err := ApplyQoSBatch(namespace, []QoSOperation{{
		IfName:      ifname,
		QoS:         q,
		Reset:       reset,
		ChangeTbf:   changeTbf,
		ChangeNetem: changeNetem,
	}})
	if err != nil {
		return false, false, err
	}
	if len(results) != 1 {
		return false, false, errors.New("invalid qdisc batch result")
	}
	return results[0].HasTbf, results[0].HasNetem, results[0].Err
}

// ApplyQoSBatch enters namespace once, opens one rtnetlink session, and applies
// every operation. Per-interface failures are reported in the result slice so
// the caller can continue with the remaining interfaces.
func ApplyQoSBatch(namespace netns.NsHandle, ops []QoSOperation) ([]QoSOperationResult, error) {
	results := make([]QoSOperationResult, len(ops))
	if len(ops) == 0 {
		return results, nil
	}

	err := withNetns(namespace, func() error {
		rtnl, err := tc.Open(&tc.Config{})
		if err != nil {
			openErr := fmt.Errorf("could not open rtnetlink socket: %w", err)
			for idx := range results {
				results[idx].Err = openErr
			}
			return nil
		}
		defer func() {
			if err := rtnl.Close(); err != nil {
				tcLogger().Errorf("Could not close rtnetlink socket: %v", err)
			}
		}()

		for idx := range ops {
			results[idx].HasTbf, results[idx].HasNetem, results[idx].Err = applyQoSAt(rtnl, ops[idx])
		}
		return nil
	})

	return results, err
}

func applyQoSAt(rtnl *tc.Tc, op QoSOperation) (hasTbf, hasNetem bool, err error) {
	devID, err := netlink.LinkByName(op.IfName)
	if err != nil {
		return false, false, fmt.Errorf("could not get interface ID for %s: %w", op.IfName, err)
	}

	if op.Reset {
		if err := resetQdiscs(devID.Attrs().Index); err != nil {
			return false, false, err
		}
		op.ChangeTbf = false
		op.ChangeNetem = false
	}

	txQueuelen := effectiveTxQueuelen(op.QoS.TxQueuelen)
	if devID.Attrs().TxQLen != txQueuelen {
		if err := netlink.LinkSetTxQLen(devID, txQueuelen); err != nil {
			return false, false, fmt.Errorf("could not set txqueuelen on %s: %w", op.IfName, err)
		}
	}

	if op.QoS.Rate > 0 {
		qdisc := tbfQdisc(devID, op.QoS.Delay+op.QoS.Jitter, op.QoS.Rate, op.QoS.Buffer, op.QoS.Burst)
		if op.ChangeTbf {
			if err := rtnl.Qdisc().Change(&qdisc); err != nil {
				return false, false, fmt.Errorf("could not change qdisc tbf to %s: %w", op.IfName, err)
			}
		} else if err := rtnl.Qdisc().Add(&qdisc); err != nil {
			return false, false, fmt.Errorf("could not assign qdisc tbf to %s: %w", op.IfName, err)
		}
		hasTbf = true
	}

	if op.QoS.Delay > 0 || op.QoS.Jitter > 0 || op.QoS.Loss > 0 {
		parent, handle := rootParent(), rootHandle()
		if hasTbf {
			parent = tbfInnerParent()
			handle = netemChildHandle()
		}

		qdisc, err := netemQdisc(devID, op.QoS.Delay, op.QoS.Jitter, op.QoS.Loss, op.QoS.Limit, handle, parent)
		if err != nil {
			return hasTbf, false, err
		}
		if op.ChangeNetem {
			if err := rtnl.Qdisc().Change(&qdisc); err != nil {
				return hasTbf, false, fmt.Errorf("could not change qdisc netem on %s: %w", op.IfName, err)
			}
		} else if err := rtnl.Qdisc().Add(&qdisc); err != nil {
			return hasTbf, false, fmt.Errorf("could not assign qdisc netem to %s: %w", op.IfName, err)
		}
		hasNetem = true
	}

	return hasTbf, hasNetem, nil
}

func resetQdiscs(ifIndex int) error {
	// All gonetem qdisc trees are rooted at handle 1:0. Deleting the root
	// qdisc also deletes its child netem qdisc, so no qdisc dump is needed.
	qdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: ifIndex,
			Handle:    rootHandle(),
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
