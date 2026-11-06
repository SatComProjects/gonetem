package server

import (
	"sync"
	"testing"
)

func newTestLink() *NetemLink {
	return newTestLinkWithPeers("A", "B")
}

func newTestLinkWithPeers(peer1, peer2 string) *NetemLink {
	return &NetemLink{
		Peer1: NetemLinkPeer{Node: &fakeStartNode{name: peer1}, IfIndex: 0},
		Peer2: NetemLinkPeer{Node: &fakeStartNode{name: peer2}, IfIndex: 0},
	}
}

func TestLinkUpdateConcurrentDistinctLinks(t *testing.T) {
	linkAB := newTestLinkWithPeers("A", "B")
	linkCD := newTestLinkWithPeers("C", "D")
	manager := &NetemTopologyManager{
		nodes:   make([]NetemNode, 0),
		links:   []*NetemLink{linkAB, linkCD},
		bridges: make([]*NetemBridge, 0),
	}

	configs := []LinkConfig{
		{
			Peer1:    "A.0",
			Peer2:    "B.0",
			Peer1QoS: QoSConfig{Delay: 10},
			Peer2QoS: QoSConfig{Delay: 10},
		},
		{
			Peer1:    "C.0",
			Peer2:    "D.0",
			Peer1QoS: QoSConfig{Delay: 20},
			Peer2QoS: QoSConfig{Delay: 20},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		cfg := configs[i%len(configs)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := manager.LinkUpdate(cfg, false); err != nil {
				t.Errorf("LinkUpdate failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := linkAB.Config.GetPeer1QoS().Delay; got != 10 {
		t.Fatalf("link AB delay = %d, want 10", got)
	}
	if got := linkCD.Config.GetPeer1QoS().Delay; got != 20 {
		t.Fatalf("link CD delay = %d, want 20", got)
	}
}

func TestStartStopRequireRunningTopology(t *testing.T) {
	manager := &NetemTopologyManager{
		nodes: make([]NetemNode, 0),
		links: make([]*NetemLink, 0),
	}

	if _, err := manager.Start("missing"); err == nil {
		t.Fatal("Start unexpectedly succeeded on a stopped topology")
	}
	if err := manager.Stop("missing"); err == nil {
		t.Fatal("Stop unexpectedly succeeded on a stopped topology")
	}
}

func TestApplyLinkUpdateConfigClearsQoS(t *testing.T) {
	l := newTestLink()
	l.Config.Delay = 50
	l.Config.Jitter = 10
	l.Config.Loss = 5
	l.Config.Rate = 1000
	l.Config.Buffer = 1
	l.Config.Limit = 750
	l.Config.TxQueuelen = 1500

	applyLinkUpdateConfig(l, LinkConfig{Peer1: "A.0", Peer2: "B.0"})

	if l.Config.Delay != 0 || l.Config.Jitter != 0 || l.Config.Loss != 0 ||
		l.Config.Rate != 0 || l.Config.Buffer != 0 ||
		l.Config.Limit != 0 || l.Config.TxQueuelen != 0 {
		t.Fatalf("QoS was not cleared: %+v", l.Config)
	}
	if q := l.Config.GetPeer1QoS(); q != (QoSConfig{}) {
		t.Fatalf("effective peer1 QoS is not cleared: %+v", q)
	}
}

func TestApplyLinkUpdateConfigHandlesInverseOrientation(t *testing.T) {
	l := newTestLink()

	applyLinkUpdateConfig(l, LinkConfig{
		Peer1:    "B.0",
		Peer2:    "A.0",
		Peer1QoS: QoSConfig{Delay: 10},
	})

	if l.Config.Peer1QoS.Delay != 0 {
		t.Fatalf("inverse request was applied to peer1: %+v", l.Config.Peer1QoS)
	}
	if l.Config.Peer2QoS.Delay != 10 {
		t.Fatalf("inverse request was not applied to peer2: %+v", l.Config.Peer2QoS)
	}
}

func TestApplyLinkUpdateConfigDefaultsBuffer(t *testing.T) {
	l := newTestLink()

	applyLinkUpdateConfig(l, LinkConfig{
		Peer1:    "A.0",
		Peer2:    "B.0",
		Peer1QoS: QoSConfig{Delay: 10, Rate: 1000},
	})

	if l.Config.Peer1QoS.Buffer != 1.0 {
		t.Fatalf("per-peer buffer was not defaulted: %+v", l.Config.Peer1QoS)
	}
}

func TestApplyPeerQoSDiffNoopSkipsKernelWork(t *testing.T) {
	hasTbf, hasNetem := false, false
	q := QoSConfig{Delay: 10, Loss: 1}
	if err := applyPeerQoSDiff("missing", 0, q, q, &hasTbf, &hasNetem); err != nil {
		t.Fatalf("no-op diff returned an error: %v", err)
	}
}

func TestLinkUpdateNoOpSkipsKernelWork(t *testing.T) {
	link := newTestLink()
	manager := &NetemTopologyManager{
		nodes:   make([]NetemNode, 0),
		links:   []*NetemLink{link},
		bridges: make([]*NetemBridge, 0),
		running: true,
	}

	if err := manager.LinkUpdate(LinkConfig{
		Peer1: "A.0",
		Peer2: "B.0",
	}, false); err != nil {
		t.Fatalf("no-op LinkUpdate returned an error: %v", err)
	}
}

func TestLinkUpdateWhileStoppedOnlyUpdatesConfig(t *testing.T) {
	manager := &NetemTopologyManager{
		nodes:   make([]NetemNode, 0),
		links:   []*NetemLink{newTestLink()},
		bridges: make([]*NetemBridge, 0),
	}

	err := manager.LinkUpdate(LinkConfig{
		Peer1:    "A.0",
		Peer2:    "B.0",
		Peer1QoS: QoSConfig{Delay: 10},
	}, false)
	if err != nil {
		t.Fatalf("LinkUpdate on a stopped topology returned an error: %v", err)
	}
	if manager.links[0].Config.Peer1QoS.Delay != 10 {
		t.Fatalf("config was not updated: %+v", manager.links[0].Config)
	}
}

func TestApplyLinkUpdateConfigMirrorsEqualPeers(t *testing.T) {
	l := newTestLink()

	applyLinkUpdateConfig(l, LinkConfig{
		Peer1:    "A.0",
		Peer2:    "B.0",
		Peer1QoS: QoSConfig{Delay: 10, Jitter: 2},
		Peer2QoS: QoSConfig{Delay: 10, Jitter: 2},
	})

	if l.Config.Delay != 10 || l.Config.Jitter != 2 {
		t.Fatalf("equal peer QoS was not mirrored: %+v", l.Config)
	}
}

func TestApplyLinkUpdateConfigQueueFields(t *testing.T) {
	l := newTestLink()
	q := QoSConfig{Limit: 750, TxQueuelen: 1500}

	applyLinkUpdateConfig(l, LinkConfig{
		Peer1:    "A.0",
		Peer2:    "B.0",
		Peer1QoS: q,
		Peer2QoS: q,
	})

	if l.Config.Peer1QoS.Limit != 750 || l.Config.Peer1QoS.TxQueuelen != 1500 {
		t.Fatalf("peer1 queue fields were not applied: %+v", l.Config.Peer1QoS)
	}
	if l.Config.Peer2QoS.Limit != 750 || l.Config.Peer2QoS.TxQueuelen != 1500 {
		t.Fatalf("peer2 queue fields were not applied: %+v", l.Config.Peer2QoS)
	}
	if l.Config.Limit != 750 || l.Config.TxQueuelen != 1500 {
		t.Fatalf("equal peer queue fields were not mirrored: %+v", l.Config)
	}
}

func TestApplyLinkUpdateConfigClearsQueueFields(t *testing.T) {
	l := newTestLink()
	l.Config.Limit = 750
	l.Config.TxQueuelen = 1500

	applyLinkUpdateConfig(l, LinkConfig{Peer1: "A.0", Peer2: "B.0"})

	if l.Config.Limit != 0 || l.Config.TxQueuelen != 0 {
		t.Fatalf("queue fields were not cleared: %+v", l.Config)
	}
	if q := l.Config.GetPeer1QoS(); q.Limit != 0 || q.TxQueuelen != 0 {
		t.Fatalf("effective peer1 queue fields are not cleared: %+v", q)
	}
}

func TestQoSDiffNetemLimitChange(t *testing.T) {
	old := QoSConfig{Delay: 10, Limit: 1000}
	next := QoSConfig{Delay: 10, Limit: 2000}

	reset, changeTbf, changeNetem, changed := qosDiff(old, next, false, true)
	if !changed {
		t.Fatal("netem limit change was not detected")
	}
	if reset || changeTbf {
		t.Fatalf("netem limit change reset qdiscs: reset=%v changeTbf=%v", reset, changeTbf)
	}
	if !changeNetem {
		t.Fatal("netem limit change did not request a netem update")
	}
}

func TestQoSDiffTxQueuelenOnlyChange(t *testing.T) {
	reset, changeTbf, changeNetem, changed := qosDiff(
		QoSConfig{}, QoSConfig{TxQueuelen: 1500}, false, false,
	)
	if !changed {
		t.Fatal("txqueuelen change was not detected")
	}
	if reset || changeTbf || changeNetem {
		t.Fatalf("txqueuelen-only change touched qdiscs: reset=%v tbf=%v netem=%v",
			reset, changeTbf, changeNetem)
	}
}
