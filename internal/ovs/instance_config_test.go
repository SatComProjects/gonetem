package ovs

import (
	"errors"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
)

func TestOvsProjectInstance_LoadConfigMissingFileIsNoop(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)

	messages, err := instance.LoadConfig("sw1", "br0", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("LoadConfig messages = %#v, want empty", messages)
	}
	if client.copyToCallCount() != 0 {
		t.Fatal("LoadConfig copied a missing file")
	}
}

func TestOvsProjectInstance_LoadConfigCopiesAndExecs(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(path.Join(confDir, "sw1.conf"), []byte("config"), 0o644); err != nil {
		t.Fatalf("unable to write fixture config: %v", err)
	}
	client := newFakeContainerAPI()
	client.execOutput = "line1\nline2"
	instance := newTestInstance(client)

	messages, err := instance.LoadConfig("sw1", "br0", confDir, 0)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if !reflect.DeepEqual(messages, []string{"line1", "line2"}) {
		t.Fatalf("LoadConfig messages = %#v", messages)
	}
	if client.copyToCallCount() != 1 {
		t.Fatalf("config copied %d times, want 1", client.copyToCallCount())
	}
	want := []string{"ovs-config.py", "-a", "load", "-c", "/tmp/sw1.conf", "br0"}
	if calls := client.execCalls(); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}
}

func TestOvsProjectInstance_LoadConfigWrapsCopyError(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(path.Join(confDir, "sw1.conf"), []byte("config"), 0o644); err != nil {
		t.Fatalf("unable to write fixture config: %v", err)
	}
	copyErr := errors.New("copy failed")
	client := newFakeContainerAPI()
	client.copyToErr = copyErr
	instance := newTestInstance(client)

	_, err := instance.LoadConfig("sw1", "br0", confDir, 0)
	if !errors.Is(err, copyErr) || !strings.Contains(err.Error(), "unable to copy config file") {
		t.Fatalf("LoadConfig error = %v, want wrapped copy error", err)
	}
}

func TestOvsProjectInstance_LoadConfigWrapsExecError(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(path.Join(confDir, "sw1.conf"), []byte("config"), 0o644); err != nil {
		t.Fatalf("unable to write fixture config: %v", err)
	}
	execErr := errors.New("load failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	instance := newTestInstance(client)

	_, err := instance.LoadConfig("sw1", "br0", confDir, 0)
	if !errors.Is(err, execErr) || !strings.Contains(err.Error(), "unable to load config file") {
		t.Fatalf("LoadConfig error = %v, want wrapped exec error", err)
	}
}

func TestOvsProjectInstance_SaveConfigExecsAndCopies(t *testing.T) {
	client := newFakeContainerAPI()
	instance := newTestInstance(client)
	dstDir := t.TempDir()

	if err := instance.SaveConfig("sw1", "br0", dstDir, 0); err != nil {
		t.Fatalf("SaveConfig returned an error: %v", err)
	}
	want := []string{"ovs-config.py", "-a", "save", "-c", "/tmp/sw1.conf", "br0"}
	if calls := client.execCalls(); len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("unexpected exec calls: %#v", calls)
	}
	if client.copyFromCallCount() != 1 {
		t.Fatalf("config copied %d times, want 1", client.copyFromCallCount())
	}
}

func TestOvsProjectInstance_SaveConfigWrapsExecError(t *testing.T) {
	execErr := errors.New("save failed")
	client := newFakeContainerAPI()
	client.execErr = execErr
	instance := newTestInstance(client)

	err := instance.SaveConfig("sw1", "br0", t.TempDir(), 0)
	if !errors.Is(err, execErr) || !strings.Contains(err.Error(), "unable to save config") {
		t.Fatalf("SaveConfig error = %v, want wrapped exec error", err)
	}
}

func TestOvsProjectInstance_SaveConfigWrapsCopyError(t *testing.T) {
	copyErr := errors.New("copy failed")
	client := newFakeContainerAPI()
	client.copyFromErr = copyErr
	instance := newTestInstance(client)

	err := instance.SaveConfig("sw1", "br0", t.TempDir(), 0)
	if !strings.Contains(err.Error(), "Unable to save file") || !strings.Contains(err.Error(), "copy failed") {
		t.Fatalf("SaveConfig error = %v, want wrapped copy message", err)
	}
}
