package docker

import (
	"sync"

	"github.com/satcomprojects/gonetem/internal/link"
)

// interfaceTable owns the Docker interface bookkeeping of a node. It hides the
// map and its lock so lifecycle, netns and exec code never manipulate the map
// directly. The zero value is usable: reads behave like an empty map.
type interfaceTable struct {
	mu      sync.RWMutex
	entries map[string]*DockerInterface
}

func newInterfaceTable() *interfaceTable {
	return &interfaceTable{entries: make(map[string]*DockerInterface)}
}

// attach records an interface. It replaces any existing entry with the same
// name, matching the previous map assignment semantics.
func (t *interfaceTable) attach(ifName string, iface *DockerInterface) {
	t.mu.Lock()
	if t.entries == nil {
		t.entries = make(map[string]*DockerInterface)
	}
	t.entries[ifName] = iface
	t.mu.Unlock()
}

func (t *interfaceTable) detach(ifName string) {
	t.mu.Lock()
	delete(t.entries, ifName)
	t.mu.Unlock()
}

// markConfigured flags an attached interface as configured, if it is still
// present.
func (t *interfaceTable) markConfigured(ifName string) {
	t.mu.Lock()
	if iface := t.entries[ifName]; iface != nil {
		iface.Configured = true
	}
	t.mu.Unlock()
}

// markConfiguredAll flags several interfaces, skipping entries that were
// detached in the meantime.
func (t *interfaceTable) markConfiguredAll(ifNames []string) {
	t.mu.Lock()
	for _, ifName := range ifNames {
		if iface := t.entries[ifName]; iface != nil {
			iface.Configured = true
		}
	}
	t.mu.Unlock()
}

// pending returns the interfaces that have not been configured yet.
func (t *interfaceTable) pending() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pending := make([]string, 0, len(t.entries))
	for ifName, iface := range t.entries {
		if iface == nil || !iface.Configured {
			pending = append(pending, ifName)
		}
	}
	return pending
}

// names returns a snapshot of the attached interface names.
func (t *interfaceTable) names() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	names := make([]string, 0, len(t.entries))
	for ifName := range t.entries {
		names = append(names, ifName)
	}
	return names
}

// state returns the link state of an interface.
func (t *interfaceTable) state(ifName string) (link.IfState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	iface, found := t.entries[ifName]
	if !found {
		return 0, false
	}
	return iface.State, true
}

// setState updates the state of an attached interface and reports whether the
// interface still existed.
func (t *interfaceTable) setState(ifName string, state link.IfState) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	iface := t.entries[ifName]
	if iface == nil {
		return false
	}
	iface.State = state
	return true
}

// states returns a copy of the link states keyed by interface name.
func (t *interfaceTable) states() map[string]link.IfState {
	t.mu.RLock()
	defer t.mu.RUnlock()

	states := make(map[string]link.IfState, len(t.entries))
	for ifName, iface := range t.entries {
		states[ifName] = iface.State
	}
	return states
}

// reset drops all bookkeeping. It is used when the container is removed.
func (t *interfaceTable) reset() {
	t.mu.Lock()
	t.entries = make(map[string]*DockerInterface)
	t.mu.Unlock()
}
