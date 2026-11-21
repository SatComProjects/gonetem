package console

import (
	"io"
	"os"
	"strings"
	"testing"
)

type fakeToolProcess struct {
	waited  bool
	waitErr error
	killed  bool
}

func (p *fakeToolProcess) Wait() error {
	p.waited = true
	return p.waitErr
}

func (p *fakeToolProcess) Kill() error {
	p.killed = true
	return nil
}

func (p *fakeToolProcess) Signal(os.Signal) error { return nil }

type fakeToolRunner struct {
	path     string
	pathErr  error
	name     string
	args     []string
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	started  int
	process  *fakeToolProcess
	startErr error
}

func (r *fakeToolRunner) LookPath(name string) (string, error) {
	return r.path, r.pathErr
}

func (r *fakeToolRunner) Start(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (ToolProcess, error) {
	r.name = name
	r.args = args
	r.stdin = stdin
	r.stdout = stdout
	r.stderr = stderr
	r.started++
	if r.startErr != nil {
		return nil, r.startErr
	}
	if r.process == nil {
		r.process = &fakeToolProcess{}
	}
	return r.process, nil
}

func TestEditFileWithRunner(t *testing.T) {
	runner := &fakeToolRunner{path: "/usr/bin/nano"}
	if err := editFileWithRunner("/tmp/network.yml", "nano", runner); err != nil {
		t.Fatalf("editFileWithRunner returned an error: %v", err)
	}
	if runner.started != 1 || runner.name != "nano" || len(runner.args) != 1 || runner.args[0] != "/tmp/network.yml" {
		t.Fatalf("runner invocation = %#v", runner)
	}
	if runner.stdin != os.Stdin || runner.stdout != os.Stdout {
		t.Fatal("interactive editor did not inherit stdio")
	}
	if !runner.process.waited {
		t.Fatal("editor process was not waited for")
	}
}

func TestEditFileWithRunnerNonInteractive(t *testing.T) {
	runner := &fakeToolRunner{path: "/usr/bin/true"}
	if err := editFileWithRunner("/tmp/network.yml", "code", runner); err != nil {
		t.Fatalf("editFileWithRunner returned an error: %v", err)
	}
	if runner.stdin != nil || runner.stdout != nil {
		t.Fatal("non-interactive editor inherited stdio")
	}
}

func TestEditFileWithRunnerPropagatesStartError(t *testing.T) {
	runner := &fakeToolRunner{startErr: os.ErrPermission}
	err := editFileWithRunner("/tmp/network.yml", "editor", runner)
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("start error = %v", err)
	}
}

func TestExecToolRunnerStartWaitAndLookPath(t *testing.T) {
	runner := execToolRunner{}

	path, err := runner.LookPath("true")
	if err != nil {
		t.Fatalf("LookPath(true): %v", err)
	}
	proc, err := runner.Start(path, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Start(true): %v", err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("Wait(true): %v", err)
	}

	proc, err = runner.Start("/bin/sh", []string{"-c", "exit 3"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Start(sh): %v", err)
	}
	if err := proc.Wait(); err == nil {
		t.Fatal("Wait did not report the non-zero exit status")
	}

	if _, err := runner.LookPath("gonetem-definitely-missing-tool"); err == nil {
		t.Fatal("LookPath accepted a missing tool")
	}
}

func TestExecProcessSignalAndKill(t *testing.T) {
	runner := execToolRunner{}
	proc, err := runner.Start("/bin/sleep", []string{"5"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Start(sleep): %v", err)
	}
	if err := proc.Signal(os.Kill); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	_ = proc.Wait()

	proc, err = runner.Start("/bin/sleep", []string{"5"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("Start(sleep): %v", err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_ = proc.Wait()
}
