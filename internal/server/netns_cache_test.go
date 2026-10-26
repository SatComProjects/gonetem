package server

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vishvananda/netns"
)

type fakeNetnsNode struct {
	fakeStartNode
	handle netns.NsHandle
	calls  atomic.Int32
}

func (n *fakeNetnsNode) GetNetns() (netns.NsHandle, error) {
	n.calls.Add(1)
	return n.handle, nil
}

func TestNodeNetnsCacheCollapsesConcurrentLookups(t *testing.T) {
	handle, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}

	node := &fakeNetnsNode{
		fakeStartNode: fakeStartNode{name: "R1"},
		handle:        handle,
	}
	cache := newNodeNetnsCache()
	defer cache.closeAll()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.get(node)
			if err != nil {
				t.Errorf("cache.get returned an error: %v", err)
				return
			}
			if !got.Equal(handle) {
				t.Errorf("cache returned a different namespace handle")
			}
		}()
	}
	wg.Wait()

	if got := node.calls.Load(); got != 1 {
		t.Fatalf("GetNetns was called %d times, expected 1", got)
	}
}
