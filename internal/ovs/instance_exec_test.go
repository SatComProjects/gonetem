package ovs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestOvsProjectInstance_ExecRequiresClient(t *testing.T) {
	instance := newTestInstance(nil)

	err := instance.Exec([]string{"ovs-vsctl", "show"})
	if err == nil || !strings.Contains(err.Error(), "docker client is not configured") {
		t.Fatalf("Exec error = %v, want missing client error", err)
	}
}

func TestOvsProjectInstance_ExecUsesInjectedClient(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)
	cmd := []string{"ovs-vsctl", "show"}

	if err := instance.Exec(cmd); err != nil {
		t.Fatalf("Exec returned an error: %v", err)
	}
	if calls := client.execCalls(); !reflect.DeepEqual(calls, [][]string{cmd}) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}
}

func TestOvsProjectInstance_ExecPropagatesClientError(t *testing.T) {
	execErr := errors.New("exec failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	instance := newTestInstance(client)

	if err := instance.Exec([]string{"true"}); !errors.Is(err, execErr) {
		t.Fatalf("Exec error = %v, want %v", err, execErr)
	}
}

func TestOvsProjectInstance_GetNetnsNotStarted(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if _, err := instance.GetNetns(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("GetNetns error = %v, want not running error", err)
	}
	if client.pidCalls() != 0 {
		t.Fatal("GetNetns queried the PID of a stopped instance")
	}
}

func TestOvsProjectInstance_GetNetnsUsesInjectedClient(t *testing.T) {
	client := newFakeContainerAPI()
	client.pid = os.Getpid()
	instance := newTestInstance(client)
	instance.state = started

	handle, err := instance.GetNetns()
	if err != nil {
		t.Fatalf("GetNetns returned an error: %v", err)
	}
	defer handle.Close()

	if client.pidCalls() != 1 {
		t.Fatalf("PID queried %d times, want 1", client.pidCalls())
	}
}

func TestOvsProjectInstance_GetNetnsPropagatesPidError(t *testing.T) {
	pidErr := errors.New("no such container")
	client := newFakeContainerAPI()
	client.pidErr = pidErr
	instance := newTestInstance(client)
	instance.state = started

	if _, err := instance.GetNetns(); !errors.Is(err, pidErr) {
		t.Fatalf("GetNetns error = %v, want %v", err, pidErr)
	}
}

func TestOvsProjectInstance_CaptureNotStarted(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	err := instance.Capture(context.Background(), "sw1.0", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("Capture error = %v, want not running error", err)
	}
	if calls := client.streamCalls(); len(calls) != 0 {
		t.Fatalf("Capture reached Docker while stopped: %#v", calls)
	}
}

func TestOvsProjectInstance_CaptureUsesInterfaceName(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)
	instance.state = started
	var out bytes.Buffer

	if err := instance.Capture(context.Background(), "sw1.0", &out); err != nil {
		t.Fatalf("Capture returned an error: %v", err)
	}
	if out.String() != "capture" {
		t.Fatalf("Capture output = %q, want fake output", out.String())
	}

	wantCmd := []string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", "sw1.0"}
	if calls := client.streamCalls(); !reflect.DeepEqual(calls, [][]string{wantCmd}) {
		t.Fatalf("unexpected stream calls: %#v", calls)
	}
}

func TestOvsProjectInstance_StartRequiresClient(t *testing.T) {
	instance := newTestInstance(nil)

	if err := instance.Start(); err == nil || !strings.Contains(err.Error(), "docker client is not configured") {
		t.Fatalf("Start error = %v, want missing client error", err)
	}
}

func TestOvsProjectInstance_StartTransitionsOnce(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if err := instance.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	if instance.state != started {
		t.Fatalf("state = %d, want started", instance.state)
	}
	if err := instance.Start(); err != nil {
		t.Fatalf("second Start returned an error: %v", err)
	}
	if calls := client.startCalls(); len(calls) != 1 {
		t.Fatalf("container started %d times, want 1", len(calls))
	}
}

func TestOvsProjectInstance_ConcurrentStartStartsOnce(t *testing.T) {
	const count = 10
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := instance.Start(); err != nil {
				t.Errorf("Start returned an error: %v", err)
			}
		}()
	}
	wg.Wait()

	if calls := client.startCalls(); len(calls) != 1 {
		t.Fatalf("container started %d times, want 1", len(calls))
	}
}

func TestOvsProjectInstance_CloseRemovesContainer(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	if err := instance.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if removed := client.removedContainers(); !reflect.DeepEqual(removed, []string{"cid"}) {
		t.Fatalf("unexpected removed containers: %#v", removed)
	}
}

func TestOvsProjectInstance_ClosePropagatesClientError(t *testing.T) {
	rmErr := errors.New("remove failed")
	client := newFakeContainerAPI()
	client.rmErr = rmErr
	instance := newTestInstance(client)

	if err := instance.Close(); !errors.Is(err, rmErr) {
		t.Fatalf("Close error = %v, want %v", err, rmErr)
	}
}
