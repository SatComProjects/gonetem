package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	"github.com/google/shlex"
	"github.com/moby/term"
)

func (n *DockerNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !n.IsRunning() {
		return errors.New("not running")
	}

	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	ifName := n.GetInterfaceName(ifIndex)
	cmd := []string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", ifName}
	return client.ExecOutStream(ctx, n.ID, cmd, out)
}

func (n *DockerNode) ExecCommand(
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

	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	return client.ExecTty(
		ctx,
		n.ID,
		cmd, in, out,
		tty, ttyHeight, ttyWidth,
		resizeCh)
}

func (n *DockerNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !n.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}

	client, err := n.dockerClient()
	if err != nil {
		return nil, nil, -1, err
	}

	pid, err := n.runningPid(client)
	if err != nil {
		return nil, nil, -1, err
	}

	nsenterCmd := exec.Command("nsenter", append([]string{"-t", strconv.Itoa(pid), "-a", "--"}, cmd...)...)
	var stdout, stderr bytes.Buffer
	nsenterCmd.Stdout = &stdout
	nsenterCmd.Stderr = &stderr
	err = nsenterCmd.Run()

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

func (n *DockerNode) GetConsoleCmd(shell bool) ([]string, error) {
	cmd := n.Config.Commands.Console
	if shell {
		cmd = n.Config.Commands.Shell
	}

	cmds, err := shlex.Split(cmd)
	if err != nil {
		return []string{}, fmt.Errorf("unable to parse console cmd: %v", err)
	}

	return cmds, nil
}
