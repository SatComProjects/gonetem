package server

import (
	"sync"
	"testing"
)

func TestProjectMapConcurrentAccess(t *testing.T) {
	const id = "concurrent-test"
	project := &NetemProject{Id: id, Name: id}

	projectsMu.Lock()
	openProjects[id] = project
	projectsMu.Unlock()
	defer func() {
		projectsMu.Lock()
		delete(openProjects, id)
		projectsMu.Unlock()
	}()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			_ = ProjectIsExist(id)
			_ = ProjectIsIdExist(id)
			_ = ProjectGetOne(id)
			_ = ProjectGetMany()
		}()

		go func() {
			defer wg.Done()
			projectsMu.Lock()
			openProjects[id].Name = id
			projectsMu.Unlock()
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
