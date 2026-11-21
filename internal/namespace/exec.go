package namespace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/moby/term"
)

func (n *NamespaceNode) nsenterArgs(cmd []string) []string {
	args := []string{"--net=" + n.netnsPath(), "--"}
	return append(args, cmd...)
}

func (n *NamespaceNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !n.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}

	var stdout, stderr bytes.Buffer
	err := n.commandExecutor().Run("nsenter", n.nsenterArgs(cmd), nil, &stdout, &stderr)
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, nil, -1, err
		}
	}

	return stdout.Bytes(), stderr.Bytes(), exitCode, nil
}

func (n *NamespaceNode) ExecCommand(
	ctx context.Context,
	cmd []string,
	in io.ReadCloser,
	out io.Writer,
	tty bool,
	ttyHeight uint,
	ttyWidth uint,
	resizeCh chan term.Winsize) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	args := n.nsenterArgs(cmd)
	if !tty {
		return n.commandExecutor().RunContext(ctx, "nsenter", args, in, out, out)
	}

	nsenterCmd := exec.CommandContext(ctx, "nsenter", args...)
	ptmx, err := pty.Start(nsenterCmd)
	if err != nil {
		return fmt.Errorf("unable to start command in namespace: %w", err)
	}
	defer ptmx.Close()

	if ttyHeight > 0 && ttyWidth > 0 {
		_ = pty.Setsize(ptmx, &pty.Winsize{
			Rows: uint16(ttyHeight),
			Cols: uint16(ttyWidth),
		})
	}

	if in != nil {
		go func() {
			_, _ = io.Copy(ptmx, in)
		}()
	}

	if resizeCh != nil {
		go func() {
			for size := range resizeCh {
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: size.Height,
					Cols: size.Width,
				})
			}
		}()
	}

	_, copyErr := io.Copy(out, ptmx)
	if errors.Is(copyErr, syscall.EIO) {
		// Linux returns EIO on the master side once the child closes the PTY.
		copyErr = nil
	}
	waitErr := nsenterCmd.Wait()
	if waitErr != nil {
		return waitErr
	}
	return copyErr
}

func (n *NamespaceNode) GetConsoleCmd(shell bool) ([]string, error) {
	shellPath := "/bin/bash"
	if _, err := os.Stat(shellPath); err != nil {
		shellPath = "/bin/sh"
	}
	return []string{shellPath}, nil
}

func (n *NamespaceNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	ifName := n.GetInterfaceName(ifIndex)
	var stderr bytes.Buffer
	err := n.commandExecutor().RunContext(
		ctx, "nsenter",
		n.nsenterArgs([]string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", ifName}),
		nil, out, &stderr,
	)
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("%v: %s", err, message)
		}
		return err
	}
	return nil
}
