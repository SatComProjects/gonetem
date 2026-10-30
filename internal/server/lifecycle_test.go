package server

import (
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

func TestProgressBatcherAggregatesEvents(t *testing.T) {
	ch := make(chan TopologyRunCloseProgressT, 8)
	batcher := newProgressBatcher(ch)

	batcher.Set(NODE_COUNT, 5)
	batcher.Add(START_NODE)
	batcher.Add(START_NODE)
	batcher.Add(START_NODE)
	batcher.Flush()

	first := <-ch
	if first.Code != NODE_COUNT || first.Value != 5 {
		t.Fatalf("unexpected count event: %+v", first)
	}
	second := <-ch
	if second.Code != START_NODE || second.Value != 3 {
		t.Fatalf("unexpected batched event: %+v", second)
	}
}

func TestConcurrencyLimitsAreConfigurable(t *testing.T) {
	options.InitServerConfig()
	previous := options.ServerConfig.Docker.LinkConcurrency
	t.Cleanup(func() {
		options.ServerConfig.Docker.LinkConcurrency = previous
	})

	options.ServerConfig.Docker.LinkConcurrency = 7
	if got := linkCreateLimit(); got != 7 {
		t.Fatalf("linkCreateLimit() = %d, expected 7", got)
	}

	options.ServerConfig.Docker.LinkConcurrency = 0
	if got := linkCreateLimit(); got != maxConcurrentNodeTask {
		t.Fatalf("zero concurrency should use the fallback, got %d", got)
	}
}

func TestRunWithoutOvsInstance(t *testing.T) {
	manager := &NetemTopologyManager{
		prjID:   "no-ovs",
		path:    t.TempDir(),
		nodes:   make([]NetemNode, 0),
		links:   make([]*NetemLink, 0),
		bridges: make([]*NetemBridge, 0),
		logger:  logrus.WithField("test", "no-ovs"),
		IdGenerator: &NodeIdentifierGenerator{
			lock: &sync.Mutex{},
		},
	}

	if _, err := manager.Run(nil); err != nil {
		t.Fatalf("Run without OVS returned an error: %v", err)
	}
	if !manager.IsRunning() {
		t.Fatal("Run without OVS did not mark the topology as running")
	}
	if err := manager.Close(nil); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
}

func TestCloseResetsRunningState(t *testing.T) {
	manager := &NetemTopologyManager{
		prjID:   "lifecycle",
		nodes:   make([]NetemNode, 0),
		links:   make([]*NetemLink, 0),
		bridges: make([]*NetemBridge, 0),
		running: true,
		logger:  logrus.WithField("test", "lifecycle"),
		IdGenerator: &NodeIdentifierGenerator{
			lock: &sync.Mutex{},
		},
	}

	if err := manager.Close(nil); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if manager.IsRunning() {
		t.Fatal("Close left the topology marked as running")
	}
}
