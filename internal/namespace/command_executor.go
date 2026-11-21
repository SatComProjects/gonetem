package namespace

import (
	"context"
	"io"
	"os/exec"
)

// commandExecutor runs the external commands used by a namespace node.
// Production uses os/exec; unit tests inject a fake to assert argv, stdio and
// error handling without entering a network namespace.
type commandExecutor interface {
	Run(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
	RunContext(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// execCommandExecutor keeps the exact os/exec semantics used before the seam
// was introduced: no context for RunCommand, a context for Capture and
// ExecCommand.
type execCommandExecutor struct{}

func (execCommandExecutor) Run(name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (execCommandExecutor) RunContext(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (n *NamespaceNode) commandExecutor() commandExecutor {
	if n.executor == nil {
		return execCommandExecutor{}
	}
	return n.executor
}
