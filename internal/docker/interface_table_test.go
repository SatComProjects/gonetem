package docker

import (
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/link"
)

func TestInterfaceTableAttachDetachAndStates(t *testing.T) {
	table := newInterfaceTable()

	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})
	table.attach("eth1", &DockerInterface{
		Configured: true,
		State:      link.IFSTATE_DOWN,
	})

	states := table.states()
	if len(states) != 2 {
		t.Fatalf("states = %v, want two entries", states)
	}
	if states["eth0"] != link.IFSTATE_UP || states["eth1"] != link.IFSTATE_DOWN {
		t.Fatalf("unexpected states: %v", states)
	}

	table.detach("eth0")
	if _, found := table.state("eth0"); found {
		t.Fatal("eth0 is still present after detach")
	}
	if state, found := table.state("eth1"); !found || state != link.IFSTATE_DOWN {
		t.Fatalf("eth1 state = %v,%v, want DOWN,true", state, found)
	}
}

func TestInterfaceTableSnapshotIsACopy(t *testing.T) {
	table := newInterfaceTable()
	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})

	states := table.states()
	states["eth0"] = link.IFSTATE_DOWN
	delete(states, "eth0")

	if got := table.states()["eth0"]; got != link.IFSTATE_UP {
		t.Fatalf("table was modified through the snapshot: %v", got)
	}
}

func TestInterfaceTablePendingAndConfigured(t *testing.T) {
	table := newInterfaceTable()
	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})
	table.attach("eth1", &DockerInterface{Configured: true, State: link.IFSTATE_UP})

	pending := table.pending()
	if len(pending) != 1 || pending[0] != "eth0" {
		t.Fatalf("pending = %v, want [eth0]", pending)
	}

	table.markConfigured("eth0")
	if pending := table.pending(); len(pending) != 0 {
		t.Fatalf("pending after markConfigured = %v, want none", pending)
	}

	table.attach("eth2", &DockerInterface{State: link.IFSTATE_UP})
	table.markConfiguredAll([]string{"eth2", "missing"})
	if pending := table.pending(); len(pending) != 0 {
		t.Fatalf("pending after markConfiguredAll = %v, want none", pending)
	}
}

func TestInterfaceTableNames(t *testing.T) {
	table := newInterfaceTable()
	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})
	table.attach("eth1", &DockerInterface{State: link.IFSTATE_UP})

	names := table.names()
	if len(names) != 2 {
		t.Fatalf("names = %v, want two entries", names)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if !seen["eth0"] || !seen["eth1"] {
		t.Fatalf("unexpected names: %v", names)
	}
}

func TestInterfaceTableSetState(t *testing.T) {
	table := newInterfaceTable()
	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})

	if !table.setState("eth0", link.IFSTATE_DOWN) {
		t.Fatal("setState reported a missing interface")
	}
	if state, _ := table.state("eth0"); state != link.IFSTATE_DOWN {
		t.Fatalf("state after setState = %v, want DOWN", state)
	}
	if table.setState("missing", link.IFSTATE_DOWN) {
		t.Fatal("setState reported success for a missing interface")
	}
}

func TestInterfaceTableReset(t *testing.T) {
	table := newInterfaceTable()
	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})

	table.reset()

	if states := table.states(); len(states) != 0 {
		t.Fatalf("states after reset = %v, want none", states)
	}
	if len(table.names()) != 0 || len(table.pending()) != 0 {
		t.Fatal("table still reports entries after reset")
	}
}

func TestInterfaceTableZeroValueReadsEmpty(t *testing.T) {
	var table interfaceTable

	if states := table.states(); len(states) != 0 {
		t.Fatalf("zero-value states = %v, want none", states)
	}
	if names := table.names(); len(names) != 0 {
		t.Fatalf("zero-value names = %v, want none", names)
	}
	if pending := table.pending(); len(pending) != 0 {
		t.Fatalf("zero-value pending = %v, want none", pending)
	}
	table.detach("eth0")

	table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})
	if state, found := table.state("eth0"); !found || state != link.IFSTATE_UP {
		t.Fatalf("state after attach on zero value = %v,%v", state, found)
	}
}

func TestInterfaceTableConcurrentAccess(t *testing.T) {
	table := newInterfaceTable()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(5)
		go func() {
			defer wg.Done()
			table.attach("eth0", &DockerInterface{State: link.IFSTATE_UP})
		}()
		go func() {
			defer wg.Done()
			table.detach("eth0")
		}()
		go func() {
			defer wg.Done()
			_ = table.states()
		}()
		go func() {
			defer wg.Done()
			table.markConfigured("eth0")
		}()
		go func() {
			defer wg.Done()
			_ = table.pending()
		}()
	}
	wg.Wait()
}
