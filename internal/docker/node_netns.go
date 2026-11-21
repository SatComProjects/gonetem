package docker

import (
	"context"
	"fmt"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

func (n *DockerNode) GetNetns() (netns.NsHandle, error) {
	if n.IsRunning() {
		return n.GetRunningNetns()
	}

	return n.GetLocalNetns()
}

// resolvePid returns the cached container PID, invoking resolve only when no
// PID is cached. The PID is stable for the lifetime of a running container.
func (n *DockerNode) resolvePid(resolve func() (int, error)) (int, error) {
	return n.pids.get(resolve)
}

func (n *DockerNode) runningPid(client ContainerNodeAPI) (int, error) {
	return n.resolvePid(func() (int, error) {
		return client.Pid(context.Background(), n.ID)
	})
}

func (n *DockerNode) invalidatePid() {
	n.pids.invalidate()
}

func (n *DockerNode) GetRunningNetns() (netns.NsHandle, error) {
	// Fast path: the PID is cached for the lifetime of the running container.
	// Avoid opening a Docker client (and taking its global lock) on every
	// route installation.
	if pid, ok := n.pids.cached(); ok {
		return netns.GetFromPid(pid)
	}

	// Docker is consulted only on the first call after Start(); Stop(), Kill()
	// and Close() invalidate the cached PID.
	client, err := n.dockerClient()
	if err != nil {
		return netns.NsHandle(0), err
	}

	pid, err := n.runningPid(client)
	if err != nil {
		return netns.NsHandle(0), err
	}
	return netns.GetFromPid(pid)
}

func (n *DockerNode) GetLocalNetns() (netns.NsHandle, error) {
	n.nsMu.Lock()
	defer n.nsMu.Unlock()

	if n.LocalNetnsName == "" {
		// first created local named ns to detach interface without delete it
		name := fmt.Sprintf("%s%s", n.PrjID, n.Name)
		ns, err := link.CreateNetns(name)
		if err != nil {
			return netns.NsHandle(0),
				fmt.Errorf("error when creating node netns '%s': %v", name, err)
		}
		n.LocalNetnsName = name

		return ns, nil
	}

	localNS, err := netns.GetFromName(n.LocalNetnsName)
	if err != nil {
		return netns.NsHandle(0), fmt.Errorf("unable to get netns associated to node %s: %v", n.Name, err)
	}

	return localNS, nil
}

// deleteLocalNetns removes the local namespace used to park interfaces while
// the container is stopped. Cleanup is best effort: the container itself is
// already gone, so a leak is reported but does not fail Close.
func (n *DockerNode) deleteLocalNetns() {
	n.nsMu.Lock()
	defer n.nsMu.Unlock()

	if n.LocalNetnsName == "" {
		return
	}
	if err := link.DeleteNetns(n.LocalNetnsName); err != nil {
		n.Logger.Warnf("unable to delete netns %s: %v", n.LocalNetnsName, err)
	}
	n.LocalNetnsName = ""
}
