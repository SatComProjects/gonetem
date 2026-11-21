package console

import (
	"io"
	"os"
	"os/exec"
)

// ToolProcess is the lifecycle surface of an external tool. Production wraps
// *exec.Cmd; tests provide a fake so no GUI/editor is spawned.
type ToolProcess interface {
	Wait() error
	Kill() error
	Signal(os.Signal) error
}

// ToolRunner locates and starts external tools (Wireshark, terminal, editor).
// It is declared here so commands can be tested without a TTY or process tree.
type ToolRunner interface {
	LookPath(name string) (string, error)
	Start(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (ToolProcess, error)
}

type execToolRunner struct{}

func (execToolRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execToolRunner) Start(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (ToolProcess, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct {
	cmd *exec.Cmd
}

func (p *execProcess) Wait() error {
	return p.cmd.Wait()
}

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *execProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}
