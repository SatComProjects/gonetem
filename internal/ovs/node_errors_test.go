package ovs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/moby/term"
)

func TestOvsNode_ExecCommandWhenStopped(t *testing.T) {
	node := newTestNode()

	err := node.ExecCommand(context.Background(), []string{"true"}, nil, &bytes.Buffer{}, false, 0, 0, nil)
	if err == nil || err.Error() != "not running" {
		t.Fatalf("ExecCommand error = %v, want not running", err)
	}
}

func TestOvsNode_ExecCommandWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	err := node.ExecCommand(context.Background(), []string{"true"}, nil, &bytes.Buffer{}, false, 0, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("ExecCommand error = %v, want uninitialized instance error", err)
	}
}

func TestOvsNode_ExecCommandUsesInjectedInstance(t *testing.T) {
	client := newFakeContainerAPI()
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newTestInstance(client)
	cmd := []string{"ovs-vsctl", "show"}

	err := node.ExecCommand(context.Background(), cmd, nil, &bytes.Buffer{}, true, 24, 80, make(chan term.Winsize))
	if err != nil {
		t.Fatalf("ExecCommand returned an error: %v", err)
	}
	if calls := client.ttyCalls(); len(calls) != 1 || strings.Join(calls[0], " ") != strings.Join(cmd, " ") {
		t.Fatalf("unexpected TTY calls: %#v", calls)
	}
}

func TestOvsNode_RunCommandWhenStopped(t *testing.T) {
	node := newTestNode()

	_, _, exitCode, err := node.RunCommand([]string{"true"})
	if err == nil || err.Error() != "not running" {
		t.Fatalf("RunCommand error = %v, want not running", err)
	}
	if exitCode != -1 {
		t.Fatalf("RunCommand exit code = %d, want -1", exitCode)
	}
}

func TestOvsNode_RunCommandWithoutClient(t *testing.T) {
	node := newTestNode()
	node.Running = true
	node.OvsInstance = &OvsProjectInstance{}

	_, _, exitCode, err := node.RunCommand([]string{"true"})
	if err == nil || !strings.Contains(err.Error(), "docker client is not configured") {
		t.Fatalf("RunCommand error = %v, want missing client error", err)
	}
	if exitCode != -1 {
		t.Fatalf("RunCommand exit code = %d, want -1", exitCode)
	}
}

func TestOvsNode_CaptureWhenStopped(t *testing.T) {
	node := newTestNode()

	err := node.Capture(context.Background(), 0, &bytes.Buffer{})
	if err == nil || err.Error() != "not running" {
		t.Fatalf("Capture error = %v, want not running", err)
	}
}

func TestOvsNode_CaptureWithStoppedInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true
	node.OvsInstance = newTestInstance(newFakeContainerAPI())

	err := node.Capture(context.Background(), 0, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("Capture error = %v, want instance not running error", err)
	}
}

func TestOvsNode_GetNetnsWithoutInstance(t *testing.T) {
	node := newTestNode()

	if _, err := node.GetNetns(); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("GetNetns error = %v, want uninitialized instance error", err)
	}
}

func TestOvsNode_GetNetnsWithStoppedInstance(t *testing.T) {
	node := newTestNode()
	node.OvsInstance = newTestInstance(newFakeContainerAPI())

	if _, err := node.GetNetns(); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("GetNetns error = %v, want instance not running error", err)
	}
}

func TestOvsNode_StartWithoutInstance(t *testing.T) {
	node := newTestNode()

	if err := node.Start(); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("Start error = %v, want uninitialized instance error", err)
	}
	if node.IsRunning() {
		t.Fatal("node is running after a failed Start")
	}
}

func TestOvsNode_StopWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	if err := node.Stop(); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("Stop error = %v, want uninitialized instance error", err)
	}
	if !node.IsRunning() {
		t.Fatal("node is stopped after a failed Stop")
	}
}

func TestOvsNode_AttachInterfaceWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true

	if err := node.AttachInterface("sw1.0", 0, false); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("AttachInterface error = %v, want uninitialized instance error", err)
	}
	if _, ok := node.GetInterfacesState()["sw1.0"]; ok {
		t.Fatal("failed AttachInterface updated bookkeeping")
	}
}

func TestOvsNode_DetachInterfaceWithoutInstance(t *testing.T) {
	node := newTestNode()
	node.Running = true
	node.Interfaces["sw1.0"] = 1

	if err := node.DetachInterface("sw1.0"); err == nil || !strings.Contains(err.Error(), "is not initialized") {
		t.Fatalf("DetachInterface error = %v, want uninitialized instance error", err)
	}
}

func TestOvsNode_GetConsoleCmd(t *testing.T) {
	node := newTestNode()

	shell, err := node.GetConsoleCmd(true)
	if err != nil {
		t.Fatalf("GetConsoleCmd(true) returned an error: %v", err)
	}
	if len(shell) != 1 || shell[0] != "/bin/bash" {
		t.Fatalf("GetConsoleCmd(true) = %#v, want /bin/bash", shell)
	}

	ovsConsole, err := node.GetConsoleCmd(false)
	if err != nil {
		t.Fatalf("GetConsoleCmd(false) returned an error: %v", err)
	}
	if len(ovsConsole) != 2 || ovsConsole[0] != "/usr/bin/ovs-console.py" || ovsConsole[1] != "sw1" {
		t.Fatalf("GetConsoleCmd(false) = %#v", ovsConsole)
	}
}

func TestOvsNode_UnsupportedOperations(t *testing.T) {
	node := newTestNode()

	if err := node.CopyFrom("a", "b"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("CopyFrom error = %v, want not supported", err)
	}
	if err := node.CopyTo("a", "b"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("CopyTo error = %v, want not supported", err)
	}
	if err := node.AttachMgntInterface("mgmt0", 0, "10.0.0.1/24"); err == nil || !strings.Contains(err.Error(), "management network") {
		t.Fatalf("AttachMgntInterface error = %v, want unsupported error", err)
	}
	if err := node.ConfigureInterfaces(); err != nil {
		t.Fatalf("ConfigureInterfaces returned an error: %v", err)
	}
	if err := node.Kill(); err != nil {
		t.Fatalf("Kill returned an error: %v", err)
	}
}
