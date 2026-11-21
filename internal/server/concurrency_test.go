package server

import (
	"fmt"
	"sync"
	"testing"
)

func TestProjectRegistryConcurrentAccess(t *testing.T) {
	const readers = 50
	const writers = 25
	registry := NewProjectRegistry()

	for i := 0; i < writers; i++ {
		id := fmt.Sprintf("project-%d", i)
		if err := registry.Add(&NetemProject{Id: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writers; j++ {
				id := fmt.Sprintf("project-%d", j)
				_ = registry.IsNameOpen(id)
				_ = registry.Has(id)
				_ = registry.Get(id)
				_ = registry.Snapshot()
			}
		}()
	}
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("dynamic-%d", i)
			if err := registry.Add(&NetemProject{Id: id, Name: id}); err != nil {
				t.Errorf("Add failed: %v", err)
				return
			}
			registry.Remove(id)
		}()
	}
	wg.Wait()
}

func TestTopologyStateConcurrentAccess(t *testing.T) {
	manager := &NetemTopologyManager{
		nodes:   make([]NetemNode, 0),
		links:   make([]*NetemLink, 0),
		bridges: make([]*NetemBridge, 0),
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func(running bool) {
			defer wg.Done()
			_ = manager.IsRunning()
			_ = manager.GetAllNodes()
			_ = manager.GetNode("missing")
			_, _, _ = manager.GetLink("R1.0", "R2.0")
			_ = manager.IsNodeLaunchAtStartup("missing")

			manager.mu.Lock()
			manager.running = running
			manager.mu.Unlock()
		}(i%2 == 0)

		go func() {
			defer wg.Done()
			_ = manager.IsRunning()
			_ = manager.GetAllNodes()
		}()
	}
	wg.Wait()
}
