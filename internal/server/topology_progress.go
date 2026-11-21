package server

import (
	"sync"
	"time"
)

type RunCloseProgressCode int
type SaveProgressCode int
type CloseProgressCode int

const (
	NODE_COUNT      RunCloseProgressCode = 1
	BRIDGE_COUNT    RunCloseProgressCode = 3
	LINK_COUNT      RunCloseProgressCode = 4
	LOAD_TOPO       RunCloseProgressCode = 5
	START_NODE      RunCloseProgressCode = 6
	SETUP_LINK      RunCloseProgressCode = 7
	START_BRIDGE    RunCloseProgressCode = 8
	LOADCONFIG_NODE RunCloseProgressCode = 10
	CLOSE_NODE      RunCloseProgressCode = 11
	CLOSE_BRIDGE    RunCloseProgressCode = 12
)

type TopologyRunCloseProgressT struct {
	Code  RunCloseProgressCode
	Value int
}

// NodeRunMessages is the domain representation of configuration messages
// produced while starting a node. The transport layer converts it to protobuf.
type NodeRunMessages struct {
	Name     string
	Messages []string
}

type progressBatcher struct {
	ch       chan TopologyRunCloseProgressT
	mu       sync.Mutex
	pending  map[RunCloseProgressCode]int
	lastSent time.Time
	interval time.Duration
}

func newProgressBatcher(ch chan TopologyRunCloseProgressT) *progressBatcher {
	return &progressBatcher{
		ch:       ch,
		pending:  make(map[RunCloseProgressCode]int),
		lastSent: time.Now(),
		interval: 100 * time.Millisecond,
	}
}

func (b *progressBatcher) Add(code RunCloseProgressCode) {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.pending[code]++
	if time.Since(b.lastSent) >= b.interval {
		b.flushLocked()
	}
}

func (b *progressBatcher) Set(code RunCloseProgressCode, value int) {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.flushLocked()
	b.ch <- TopologyRunCloseProgressT{Code: code, Value: value}
	b.lastSent = time.Now()
}

func (b *progressBatcher) Flush() {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked()
}

func (b *progressBatcher) flushLocked() {
	for code, count := range b.pending {
		if count > 0 {
			b.ch <- TopologyRunCloseProgressT{Code: code, Value: count}
		}
		delete(b.pending, code)
	}
	b.lastSent = time.Now()
}

const (
	NODE_SAVE_COUNT SaveProgressCode = 1
	NODE_SAVE       SaveProgressCode = 2
)

type TopologySaveProgressT struct {
	Code  SaveProgressCode
	Value int
}
