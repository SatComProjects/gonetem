package ovs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	"github.com/moby/term"
	"github.com/vishvananda/netns"
)

func (o *OvsNode) ExecCommand(
	ctx context.Context,
	cmd []string,
	in io.ReadCloser,
	out io.Writer,
	tty bool,
	ttyHeight uint,
	ttyWidth uint,
	resizeCh chan term.Winsize) error {
	if !o.IsRunning() {
		return errors.New("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	client, err := o.OvsInstance.dockerClient()
	if err != nil {
		return err
	}

	return client.ExecTty(
		ctx,
		o.OvsInstance.containerId,
		cmd, in, out,
		tty, ttyHeight, ttyWidth,
		resizeCh)
}

func (o *OvsNode) RunCommand(cmd []string) ([]byte, []byte, int, error) {
	if !o.IsRunning() {
		return nil, nil, -1, errors.New("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return nil, nil, -1, err
	}

	client, err := o.OvsInstance.dockerClient()
	if err != nil {
		return nil, nil, -1, err
	}

	pid, err := client.Pid(context.Background(), o.OvsInstance.containerId)
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

func (o *OvsNode) GetConsoleCmd(shell bool) ([]string, error) {
	cmd := []string{"/bin/bash"}
	if !shell {
		cmd = []string{"/usr/bin/ovs-console.py", o.GetBridgeName()}
	}

	return cmd, nil
}

func (o *OvsNode) GetNetns() (netns.NsHandle, error) {
	if err := o.ensureInstance(); err != nil {
		return netns.None(), err
	}
	return o.OvsInstance.GetNetns()
}

func (o *OvsNode) Capture(ctx context.Context, ifIndex int, out io.Writer) error {
	if !o.IsRunning() {
		return fmt.Errorf("not running")
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	return o.OvsInstance.Capture(ctx, o.GetInterfaceName(ifIndex), out)
}
