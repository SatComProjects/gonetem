package link

import (
	"testing"
	"time"

	"github.com/florianl/go-tc"
	"github.com/florianl/go-tc/core"
	"github.com/mroy31/gonetem/internal/utils"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func TestFormatTimeUsesKernelClock(t *testing.T) {
	tcTime, err := core.Duration2TcTime(100 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	expected := core.Time2Tick(tcTime)

	got, err := formatTime(100)
	if err != nil {
		t.Fatalf("formatTime returned an error: %v", err)
	}
	if got != expected {
		t.Fatalf("formatTime(100) = %d, expected %d", got, expected)
	}
}

func TestFormatTimeRejectsNegativeValue(t *testing.T) {
	if _, err := formatTime(-1); err == nil {
		t.Fatal("formatTime accepted a negative value")
	}
}

func listQdiscKinds(t *testing.T, ns netns.NsHandle, ifName string) map[string]int {
	t.Helper()

	kinds := make(map[string]int)
	err := withNetns(ns, func() error {
		dev, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}

		rtnl, err := tc.Open(&tc.Config{})
		if err != nil {
			return err
		}
		defer rtnl.Close()

		qdiscs, err := rtnl.Qdisc().Get()
		if err != nil {
			return err
		}
		for _, q := range qdiscs {
			if q.Ifindex == uint32(dev.Attrs().Index) {
				kinds[q.Kind]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return kinds
}

func TestResetQdiscsIsIdempotent(t *testing.T) {
	skipUnlessRoot(t)

	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	veth, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(veth)

	if err := ResetQdiscs(veth.Name, ns); err != nil {
		t.Fatalf("first ResetQdiscs returned an error: %v", err)
	}
	if err := ResetQdiscs(veth.Name, ns); err != nil {
		t.Fatalf("second ResetQdiscs returned an error: %v", err)
	}
}

func TestApplyQoSInstallsAndClearsQdiscs(t *testing.T) {
	skipUnlessRoot(t)

	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	veth, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(veth)

	qos := QoS{
		Delay:  10,
		Jitter: 2,
		Loss:   1,
		Rate:   1000,
		Buffer: 1,
	}

	hasTbf, hasNetem, err := ApplyQoS(veth.Name, ns, qos, false, false, false)
	if err != nil {
		t.Fatalf("ApplyQoS returned an error: %v", err)
	}
	if !hasTbf || !hasNetem {
		t.Fatalf("expected tbf+netem, got tbf=%v netem=%v", hasTbf, hasNetem)
	}

	kinds := listQdiscKinds(t, ns, veth.Name)
	if kinds["tbf"] != 1 || kinds["netem"] != 1 {
		t.Fatalf("unexpected qdiscs after apply: %#v", kinds)
	}

	hasTbf, hasNetem, err = ApplyQoS(veth.Name, ns, qos, false, true, true)
	if err != nil {
		t.Fatalf("ApplyQoS change returned an error: %v", err)
	}
	if !hasTbf || !hasNetem {
		t.Fatalf("expected tbf+netem after change, got tbf=%v netem=%v", hasTbf, hasNetem)
	}

	hasTbf, hasNetem, err = ApplyQoS(veth.Name, ns, QoS{}, true, false, false)
	if err != nil {
		t.Fatalf("ApplyQoS reset returned an error: %v", err)
	}
	if hasTbf || hasNetem {
		t.Fatalf("expected no qdiscs after reset, got tbf=%v netem=%v", hasTbf, hasNetem)
	}

	kinds = listQdiscKinds(t, ns, veth.Name)
	if kinds["tbf"] != 0 || kinds["netem"] != 0 {
		t.Fatalf("qdiscs remain after reset: %#v", kinds)
	}
}
