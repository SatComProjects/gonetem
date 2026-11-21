package namespace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// commandCall records one command invoked through the fake executor.
type commandCall struct {
	name      string
	args      []string
	stdin     string
	hasStdin  bool
	withCtx   bool
	stdoutNil bool
	stderrNil bool
}

// fakeCommandExecutor replaces nsenter in unit tests. It records argv/stdio
// and can replay stdout, stderr and an error.
type fakeCommandExecutor struct {
	calls  []commandCall
	stdout string
	stderr string
	err    error
}

func (f *fakeCommandExecutor) record(withCtx bool, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) {
	call := commandCall{
		name:      name,
		args:      append([]string(nil), args...),
		withCtx:   withCtx,
		stdoutNil: stdout == nil,
		stderrNil: stderr == nil,
	}
	if stdin != nil {
		call.hasStdin = true
		data, _ := io.ReadAll(stdin)
		call.stdin = string(data)
	}
	f.calls = append(f.calls, call)

	if stdout != nil {
		_, _ = io.WriteString(stdout, f.stdout)
	}
	if stderr != nil {
		_, _ = io.WriteString(stderr, f.stderr)
	}
}

func (f *fakeCommandExecutor) Run(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.record(false, name, args, stdin, stdout, stderr)
	return f.err
}

func (f *fakeCommandExecutor) RunContext(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.record(true, name, args, stdin, stdout, stderr)
	return f.err
}

func commandExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("helper command exited with status 0, want %d", code)
	}
	return err
}

func TestNamespaceNodeRunCommandInvokesNSEnter(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	fake := &fakeCommandExecutor{stdout: "ok\n", stderr: "warn"}
	node.executor = fake

	stdout, stderr, code, err := node.RunCommand([]string{"echo", "hi"})
	if err != nil {
		t.Fatalf("RunCommand returned an error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if string(stdout) != "ok\n" || string(stderr) != "warn" {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("executor called %d times, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if call.name != "nsenter" {
		t.Fatalf("command = %q, want nsenter", call.name)
	}
	if call.withCtx {
		t.Fatal("RunCommand used a context, want plain exec.Command semantics")
	}
	wantArgs := []string{"--net=" + node.netnsPath(), "--", "echo", "hi"}
	if len(call.args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", call.args, wantArgs)
	}
	for i := range wantArgs {
		if call.args[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %q, want %q", i, call.args[i], wantArgs[i])
		}
	}
	if call.hasStdin {
		t.Fatal("RunCommand passed stdin to nsenter")
	}
}

func TestNamespaceNodeRunCommandReportsExitCode(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	fake := &fakeCommandExecutor{err: commandExitError(t, 7)}
	node.executor = fake

	_, _, code, err := node.RunCommand([]string{"false"})
	if err != nil {
		t.Fatalf("RunCommand returned an error for a non-zero exit: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestNamespaceNodeRunCommandReportsStartError(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	wantErr := errors.New("unable to start nsenter")
	node.executor = &fakeCommandExecutor{err: wantErr}

	stdout, stderr, code, err := node.RunCommand([]string{"true"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if code != -1 {
		t.Fatalf("exit code = %d, want -1", code)
	}
	if stdout != nil || stderr != nil {
		t.Fatalf("output on start failure: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestNamespaceNodeStoppedCommandsDoNotInvokeExecutor(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	fake := &fakeCommandExecutor{}
	node.executor = fake

	if _, _, _, err := node.RunCommand([]string{"true"}); err == nil {
		t.Fatal("RunCommand on a stopped node did not return an error")
	}
	if err := node.Capture(context.Background(), 0, io.Discard); err == nil {
		t.Fatal("Capture on a stopped node did not return an error")
	}
	if err := node.ExecCommand(context.Background(), []string{"true"}, nil, io.Discard, false, 0, 0, nil); err == nil {
		t.Fatal("ExecCommand on a stopped node did not return an error")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("executor was called %d times for a stopped node", len(fake.calls))
	}
}

func TestNamespaceNodeCaptureStreamsOutput(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	fake := &fakeCommandExecutor{stdout: "pcap"}
	node.executor = fake

	var out bytes.Buffer
	if err := node.Capture(context.Background(), 2, &out); err != nil {
		t.Fatalf("Capture returned an error: %v", err)
	}
	if out.String() != "pcap" {
		t.Fatalf("captured output = %q, want pcap", out.String())
	}

	if len(fake.calls) != 1 {
		t.Fatalf("executor called %d times, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if !call.withCtx {
		t.Fatal("Capture did not use a context")
	}
	wantArgs := []string{"--net=" + node.netnsPath(), "--", "tcpdump", "-w", "-", "-s", "0", "-U", "-i", "eth2"}
	if got := strings.Join(call.args, " "); got != strings.Join(wantArgs, " ") {
		t.Fatalf("args = %v, want %v", call.args, wantArgs)
	}
}

func TestNamespaceNodeCaptureIncludesStderrOnFailure(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	node.executor = &fakeCommandExecutor{
		stderr: "permission denied\n",
		err:    commandExitError(t, 1),
	}

	err := node.Capture(context.Background(), 0, io.Discard)
	if err == nil {
		t.Fatal("Capture succeeded despite a failing tcpdump")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error does not include stderr: %v", err)
	}
}

func TestNamespaceNodeExecCommandNonTTYUsesExecutor(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	node.Running = true
	fake := &fakeCommandExecutor{stdout: "out", stderr: "err"}
	node.executor = fake

	var out bytes.Buffer
	in := io.NopCloser(strings.NewReader("input"))
	if err := node.ExecCommand(context.Background(), []string{"cat"}, in, &out, false, 0, 0, nil); err != nil {
		t.Fatalf("ExecCommand returned an error: %v", err)
	}
	if out.String() != "outerr" {
		t.Fatalf("output = %q, want outerr", out.String())
	}

	if len(fake.calls) != 1 {
		t.Fatalf("executor called %d times, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if !call.withCtx {
		t.Fatal("ExecCommand did not use a context")
	}
	if call.stdin != "input" {
		t.Fatalf("stdin = %q, want input", call.stdin)
	}
}
