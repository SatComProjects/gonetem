package ovs

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestOvsProjectInstance_FindBr(t *testing.T) {
	tests := []struct {
		name    string
		bridges []string
		target  string
		want    int
	}{
		{name: "empty registry", target: "br0", want: -1},
		{name: "missing", bridges: []string{"br0", "br1"}, target: "br2", want: -1},
		{name: "first", bridges: []string{"br0", "br1"}, target: "br0", want: 0},
		{name: "last", bridges: []string{"br0", "br1"}, target: "br1", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := newTestInstance(nil)
			instance.bridges = tt.bridges

			if got := instance.findBr(tt.target); got != tt.want {
				t.Fatalf("findBr(%q) = %d, want %d", tt.target, got, tt.want)
			}
		})
	}
}

func TestOvsProjectInstance_AddBrRecordsBridgeOnce(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if err := instance.AddBr("br0"); err != nil {
		t.Fatalf("AddBr returned an error: %v", err)
	}
	if got := instance.findBr("br0"); got != 0 {
		t.Fatalf("bridge was not recorded: index %d", got)
	}

	wantCmd := []string{"ovs-vsctl", "add-br", "br0"}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{wantCmd}) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}

	if err := instance.AddBr("br0"); err == nil {
		t.Fatal("duplicate AddBr did not return an error")
	}
	if calls := client.execCalls(); len(calls) != 1 {
		t.Fatalf("duplicate AddBr reached Docker: %#v", calls)
	}
}

func TestOvsProjectInstance_AddBrDoesNotRecordOnError(t *testing.T) {
	execErr := errors.New("ovs-vsctl failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	instance := newTestInstance(client)

	if err := instance.AddBr("br0"); !errors.Is(err, execErr) {
		t.Fatalf("AddBr error = %v, want %v", err, execErr)
	}
	if got := instance.findBr("br0"); got != -1 {
		t.Fatalf("failed AddBr recorded bridge at index %d", got)
	}
}

func TestOvsProjectInstance_DelBrRemovesRecordedBridge(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)
	instance.bridges = []string{"br0", "br1"}

	if err := instance.DelBr("br0"); err != nil {
		t.Fatalf("DelBr returned an error: %v", err)
	}
	if got := instance.findBr("br0"); got != -1 {
		t.Fatalf("deleted bridge still recorded at index %d", got)
	}
	if got := instance.findBr("br1"); got != 0 {
		t.Fatalf("unrelated bridge bookkeeping changed: index %d", got)
	}

	wantCmd := []string{"ovs-vsctl", "del-br", "br0"}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{wantCmd}) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}
}

func TestOvsProjectInstance_DelBrMissingIsNoop(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)
	instance.bridges = []string{"br0"}

	if err := instance.DelBr("missing"); err != nil {
		t.Fatalf("DelBr(missing) returned an error: %v", err)
	}
	if calls := client.execCalls(); len(calls) != 0 {
		t.Fatalf("DelBr(missing) reached Docker: %#v", calls)
	}
	if got := instance.findBr("br0"); got != 0 {
		t.Fatalf("existing bridge was removed: index %d", got)
	}
}

func TestOvsProjectInstance_AddBridgeWithPortsRecordsBridge(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if err := instance.AddBridgeWithPorts("br0", []string{"p1", "p2"}); err != nil {
		t.Fatalf("AddBridgeWithPorts returned an error: %v", err)
	}
	if got := instance.findBr("br0"); got != 0 {
		t.Fatalf("bridge was not recorded: index %d", got)
	}

	wantCmd := []string{
		"ovs-vsctl", "--may-exist", "add-br", "br0",
		"--", "--may-exist", "add-port", "br0", "p1",
		"--", "--may-exist", "add-port", "br0", "p2",
	}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{wantCmd}) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}

	if err := instance.AddBridgeWithPorts("br0", nil); err == nil {
		t.Fatal("duplicate AddBridgeWithPorts did not return an error")
	}
	if calls := client.execCalls(); len(calls) != 1 {
		t.Fatalf("duplicate AddBridgeWithPorts reached Docker: %#v", calls)
	}
}

func TestOvsProjectInstance_AddBridgeWithPortsDoesNotRecordOnError(t *testing.T) {
	execErr := errors.New("ovs-vsctl failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	instance := newTestInstance(client)

	if err := instance.AddBridgeWithPorts("br0", []string{"p1"}); !errors.Is(err, execErr) {
		t.Fatalf("AddBridgeWithPorts error = %v, want %v", err, execErr)
	}
	if got := instance.findBr("br0"); got != -1 {
		t.Fatalf("failed AddBridgeWithPorts recorded bridge at index %d", got)
	}
}

func TestOvsProjectInstance_AddPortDelPortCommands(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if err := instance.AddPort("br0", "p1"); err != nil {
		t.Fatalf("AddPort returned an error: %v", err)
	}
	if err := instance.DelPort("br0", "p1"); err != nil {
		t.Fatalf("DelPort returned an error: %v", err)
	}

	want := [][]string{
		{"ovs-vsctl", "add-port", "br0", "p1"},
		{"ovs-vsctl", "del-port", "br0", "p1"},
	}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}
}

func TestOvsProjectInstance_ConcurrentBridgeBookkeeping(t *testing.T) {
	const count = 50
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := instance.AddBr(fmt.Sprintf("br%d", i)); err != nil {
				t.Errorf("AddBr(br%d) returned an error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if len(instance.bridges) != count {
		t.Fatalf("recorded %d bridges, want %d", len(instance.bridges), count)
	}

	for i := 0; i < count; i += 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := instance.DelBr(fmt.Sprintf("br%d", i)); err != nil {
				t.Errorf("DelBr(br%d) returned an error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if len(instance.bridges) != count/2 {
		t.Fatalf("recorded %d bridges after deletions, want %d", len(instance.bridges), count/2)
	}
}

func TestOvsProjectInstance_ConcurrentDuplicateAddBr(t *testing.T) {
	const count = 20
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	var wg sync.WaitGroup
	var mu sync.Mutex
	created, duplicates := 0, 0
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := instance.AddBr("br0")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				created++
				return
			}
			duplicates++
		}()
	}
	wg.Wait()

	if created != 1 {
		t.Fatalf("AddBr succeeded %d times, want 1", created)
	}
	if duplicates != count-1 {
		t.Fatalf("AddBr reported %d duplicates, want %d", duplicates, count-1)
	}
	if calls := client.execCalls(); len(calls) != 1 {
		t.Fatalf("Docker received %d add-br calls, want 1", len(calls))
	}
}
