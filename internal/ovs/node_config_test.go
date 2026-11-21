package ovs

import (
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
)

func TestOvsNode_ReadConfigFilesMissingFile(t *testing.T) {
	node := newTestNode()

	files, err := node.ReadConfigFiles(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("ReadConfigFiles returned an error: %v", err)
	}
	want := map[string][]byte{"OVS": {}}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("ReadConfigFiles = %#v, want %#v", files, want)
	}
}

func TestOvsNode_ReadConfigFilesReadsLocalFile(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(path.Join(confDir, "sw1.conf"), []byte("local config"), 0o644); err != nil {
		t.Fatalf("unable to write fixture config: %v", err)
	}
	node := newTestNode()

	files, err := node.ReadConfigFiles(confDir, 0)
	if err != nil {
		t.Fatalf("ReadConfigFiles returned an error: %v", err)
	}
	if string(files["OVS"]) != "local config" {
		t.Fatalf("ReadConfigFiles OVS = %q", files["OVS"])
	}
}

func TestOvsNode_ReadConfigFilesRunningSavesToWorkdir(t *testing.T) {
	workdir := t.TempDir()
	previous := options.ServerConfig.Workdir
	options.ServerConfig.Workdir = workdir
	t.Cleanup(func() { options.ServerConfig.Workdir = previous })

	client := newFakeContainerAPI()
	client.copyFromData = []byte("remote config")
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newRunningInstance(client)

	files, err := node.ReadConfigFiles("", 0)
	if err != nil {
		t.Fatalf("ReadConfigFiles returned an error: %v", err)
	}
	if string(files["OVS"]) != "remote config" {
		t.Fatalf("ReadConfigFiles OVS = %q, want remote config", files["OVS"])
	}
	if client.copyFromCallCount() != 1 {
		t.Fatalf("config saved %d times, want 1", client.copyFromCallCount())
	}
}

func TestOvsNode_ReadConfigFilesRunningWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	if _, err := node.ReadConfigFiles(t.TempDir(), 0); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("ReadConfigFiles error = %v, want uninitialized instance error", err)
	}
}

func TestOvsNode_LoadConfigNotRunning(t *testing.T) {
	node := newTestNode()

	messages, err := node.LoadConfig(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("LoadConfig messages = %#v, want empty", messages)
	}
}

func TestOvsNode_LoadConfigRunningWithInstance(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(path.Join(confDir, "sw1.conf"), []byte("config"), 0o644); err != nil {
		t.Fatalf("unable to write fixture config: %v", err)
	}
	client := newFakeContainerAPI()
	client.execOutput = "loaded"
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newRunningInstance(client)

	messages, err := node.LoadConfig(confDir, 0)
	if err != nil {
		t.Fatalf("LoadConfig returned an error: %v", err)
	}
	if !reflect.DeepEqual(messages, []string{"loaded"}) {
		t.Fatalf("LoadConfig messages = %#v", messages)
	}
}

func TestOvsNode_LoadConfigRunningWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	if _, err := node.LoadConfig(t.TempDir(), 0); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("LoadConfig error = %v, want uninitialized instance error", err)
	}
}

func TestOvsNode_SaveNotRunning(t *testing.T) {
	node := newTestNode()

	if err := node.Save(t.TempDir(), 0); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}
}

func TestOvsNode_SaveRunningWithInstance(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newRunningInstance(client)

	if err := node.Save(t.TempDir(), 0); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}
	if client.copyFromCallCount() != 1 {
		t.Fatalf("config saved %d times, want 1", client.copyFromCallCount())
	}
}

func TestOvsNode_SaveRunningWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	if err := node.Save(t.TempDir(), 0); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("Save error = %v, want uninitialized instance error", err)
	}
}
