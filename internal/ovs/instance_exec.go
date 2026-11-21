package ovs

import (
	"context"
	"fmt"
	"io"

	"github.com/vishvananda/netns"
)

func (o *OvsProjectInstance) GetNetns() (netns.NsHandle, error) {
	if !o.isStarted() {
		return netns.NsHandle(0), fmt.Errorf("ovswitch instance not running")
	}

	client, err := o.dockerClient()
	if err != nil {
		return netns.NsHandle(0), err
	}

	pid, err := client.Pid(context.Background(), o.containerId)
	if err != nil {
		return netns.NsHandle(0), err
	}
	return netns.GetFromPid(pid)
}

func (o *OvsProjectInstance) Capture(ctx context.Context, ifName string, out io.Writer) error {
	if !o.isStarted() {
		return fmt.Errorf("ovswitch instance not running")
	}

	client, err := o.dockerClient()
	if err != nil {
		return err
	}

	cmd := []string{"tcpdump", "-w", "-", "-s", "0", "-U", "-i", ifName}
	return client.ExecOutStream(ctx, o.containerId, cmd, out)
}

func (o *OvsProjectInstance) Exec(cmd []string) error {
	client, err := o.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if _, err := client.Exec(ctx, o.containerId, cmd); err != nil {
		return err
	}

	return nil
}
