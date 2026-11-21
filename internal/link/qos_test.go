package link

import (
	"testing"

	"github.com/vishvananda/netlink"
)

func TestFormatPercentBoundaries(t *testing.T) {
	cases := []struct {
		loss float64
		want uint32
	}{
		{0, 0},
		{0.5, 21474836},
		{1, 42949672},
		{100, uint32Max},
	}
	for _, tc := range cases {
		if got := formatPercent(tc.loss); got != tc.want {
			t.Errorf("formatPercent(%v) = %d, want %d", tc.loss, got, tc.want)
		}
	}
}

func TestFormatTimeBoundaries(t *testing.T) {
	if _, err := formatTime(-1); err == nil {
		t.Fatal("formatTime accepted a negative value")
	}
	for _, ms := range []int{0, 1, 10, 100, 1000} {
		if _, err := formatTime(ms); err != nil {
			t.Errorf("formatTime(%d) returned an error: %v", ms, err)
		}
	}

	maxInt := int(^uint(0) >> 1)
	if _, err := formatTime(maxInt); err == nil {
		t.Fatal("formatTime accepted an overflowing duration")
	}
}

func TestEffectiveQueueDefaults(t *testing.T) {
	if got := effectiveNetemLimit(0); got != DefaultNetemLimit {
		t.Errorf("effectiveNetemLimit(0) = %d, want %d", got, DefaultNetemLimit)
	}
	if got := effectiveNetemLimit(-1); got != DefaultNetemLimit {
		t.Errorf("effectiveNetemLimit(-1) = %d, want %d", got, DefaultNetemLimit)
	}
	if got := effectiveNetemLimit(2500); got != 2500 {
		t.Errorf("effectiveNetemLimit(2500) = %d, want 2500", got)
	}
	if got := effectiveTxQueuelen(0); got != DefaultTxQueuelen {
		t.Errorf("effectiveTxQueuelen(0) = %d, want %d", got, DefaultTxQueuelen)
	}
	if got := effectiveTxQueuelen(-5); got != DefaultTxQueuelen {
		t.Errorf("effectiveTxQueuelen(-5) = %d, want %d", got, DefaultTxQueuelen)
	}
	if got := effectiveTxQueuelen(1500); got != 1500 {
		t.Errorf("effectiveTxQueuelen(1500) = %d, want 1500", got)
	}
}

func TestTBFQdiscParameters(t *testing.T) {
	dev := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: 7}}

	qdisc := tbfQdisc(dev, 10, 1000, 1, 0)
	if qdisc.Kind != "tbf" || qdisc.Tbf == nil || qdisc.Tbf.Parms == nil {
		t.Fatalf("unexpected tbf qdisc: %#v", qdisc)
	}
	if qdisc.Ifindex != 7 {
		t.Errorf("ifindex = %d, want 7", qdisc.Ifindex)
	}
	if qdisc.Handle != rootHandle() || qdisc.Parent != rootParent() {
		t.Errorf("handles = %#x/%#x, want %#x/%#x",
			qdisc.Handle, qdisc.Parent, rootHandle(), rootParent())
	}
	if got := qdisc.Tbf.Parms.Rate.Rate; got != 125000 {
		t.Errorf("rate = %d, want 125000", got)
	}
	if got := *qdisc.Tbf.Burst; got != 500 {
		t.Errorf("default burst = %d, want 500", got)
	}
	if got := qdisc.Tbf.Parms.Limit; got != 1250 {
		t.Errorf("limit = %d, want 1250", got)
	}
}

func TestTBFQdiscExplicitBurstAndMinimumLimit(t *testing.T) {
	dev := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: 9}}

	qdisc := tbfQdisc(dev, 10, 1000, 0, 900)
	if got := *qdisc.Tbf.Burst; got != 900 {
		t.Errorf("burst = %d, want 900", got)
	}
	if got := qdisc.Tbf.Parms.Limit; got != 1514 {
		t.Errorf("zero BDP limit = %d, want the 1514-byte floor", got)
	}

	zeroRate := tbfQdisc(dev, 10, 0, 1, 0)
	if got := zeroRate.Tbf.Parms.Limit; got != 0 {
		t.Errorf("zero-rate limit = %d, want 0", got)
	}
}

func TestNetemQdiscParametersAndErrors(t *testing.T) {
	dev := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Index: 3}}

	qdisc, err := netemQdisc(dev, 10, 2, 0.5, 2500, netemChildHandle(), tbfInnerParent())
	if err != nil {
		t.Fatalf("netemQdisc returned an error: %v", err)
	}
	if qdisc.Kind != "netem" || qdisc.Netem == nil {
		t.Fatalf("unexpected netem qdisc: %#v", qdisc)
	}
	if qdisc.Ifindex != 3 || qdisc.Handle != netemChildHandle() || qdisc.Parent != tbfInnerParent() {
		t.Errorf("unexpected msg: %#v", qdisc.Msg)
	}
	if got := qdisc.Netem.Qopt.Limit; got != 2500 {
		t.Errorf("limit = %d, want 2500", got)
	}
	if got := qdisc.Netem.Qopt.Loss; got != formatPercent(0.5) {
		t.Errorf("loss = %d, want %d", got, formatPercent(0.5))
	}

	if _, err := netemQdisc(dev, -1, 0, 0, 0, 0, 0); err == nil {
		t.Fatal("netemQdisc accepted a negative delay")
	}
	if _, err := netemQdisc(dev, 0, -1, 0, 0, 0, 0); err == nil {
		t.Fatal("netemQdisc accepted a negative jitter")
	}
}

func FuzzFormatTime(f *testing.F) {
	f.Add(0)
	f.Add(-1)
	f.Add(10)
	f.Fuzz(func(t *testing.T, value int) {
		_, err := formatTime(value)
		if value < 0 && err == nil {
			t.Fatalf("formatTime(%d) accepted a negative value", value)
		}
		if value >= 0 && err != nil {
			t.Fatalf("formatTime(%d) returned an unexpected error: %v", value, err)
		}
	})
}
