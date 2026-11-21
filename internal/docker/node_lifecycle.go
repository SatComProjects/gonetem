package docker

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/vishvananda/netns"
)

func (n *DockerNode) Create(imgName string, ipv6 bool) error {
	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()

	present, err := client.IsImagePresent(ctx, imgName)
	if err != nil {
		return err
	} else if !present {
		return fmt.Errorf("docker image %s not present", imgName)
	}

	containerName := fmt.Sprintf("%s%s.%s", options.NETEM_ID, n.PrjID, n.Name)
	volumes := slices.Concat(n.Config.Volumes, n.Volumes)

	if n.ID, err = client.Create(ctx, imgName, containerName, n.Name, volumes, ipv6, n.Mpls); err != nil {
		return err
	}

	return nil
}

func (n *DockerNode) Start() error {
	if n.IsRunning() {
		return nil
	}

	// A stopped container may have left a stale PID behind after an external
	// kill or a previous failed start.
	n.invalidatePid()

	n.Logger.Debug("Start Node")
	startAll := time.Now()

	if n.ID == "" {
		if err := n.Create(n.ImageName, n.IPv6); err != nil {
			return fmt.Errorf("unable to create container for node %s: %w", n.Name, err)
		}
	}

	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := client.Start(ctx, n.ID); err != nil {
		return err
	}
	if _, err := n.runningPid(client); err != nil {
		return fmt.Errorf("unable to get container pid for node %s: %w", n.Name, err)
	}
	n.Logger.Debugf("Start: docker start took %s", time.Since(startAll))

	started := false
	var currentNS, targetNS netns.NsHandle
	var ifStates map[string]link.IfState

	defer func() {
		if !started {
			if targetNS != 0 && currentNS != 0 {
				if err := link.MoveInterfacesNetns(ifStates, targetNS, currentNS); err != nil {
					n.Logger.Warnf("unable to roll back interface move after start failure: %v", err)
				}
			}
			if err := client.Stop(ctx, n.ID); err != nil {
				n.Logger.Warnf("unable to stop container after start failure: %v", err)
			}
			n.mu.Lock()
			n.Running = false
			n.mu.Unlock()
			n.pids.invalidate()
		}

		if targetNS != 0 {
			targetNS.Close()
		}
		if currentNS != 0 {
			currentNS.Close()
		}
	}()

	ifStates = n.GetInterfacesState()
	allIfNames := n.interfaces.names()
	pendingIfNames := n.interfaces.pending()

	if len(allIfNames) > 0 {
		// Attach existing interfaces. Isolated nodes do not need a local
		// namespace or any namespace switching during start/stop.
		currentNS, err = n.GetLocalNetns()
		if err != nil {
			return err
		}

		targetNS, err = n.GetRunningNetns()
		if err != nil {
			return err
		}

		moveStart := time.Now()
		if err := link.MoveInterfacesNetns(ifStates, currentNS, targetNS); err != nil {
			return fmt.Errorf("unable to attach interfaces: %v", err)
		}
		n.Logger.Debugf("Start: interface move took %s", time.Since(moveStart))

		if len(pendingIfNames) > 0 {
			prepareStart := time.Now()
			n.prepareInterfaces(pendingIfNames, targetNS)
			n.Logger.Debugf("Start: interface prepare took %s", time.Since(prepareStart))
		}
	}

	n.interfaces.markConfiguredAll(pendingIfNames)
	n.mu.Lock()
	n.Running = true
	n.ConfigLoaded = false
	n.mu.Unlock()
	started = true
	n.Logger.Debugf("Start: total took %s", time.Since(startAll))

	return nil
}

func (n *DockerNode) Stop() error {
	if !n.IsRunning() {
		return nil
	}

	n.Logger.Debug("Stop Node")

	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	// Detach interfaces when the node has any.
	ifStates := n.GetInterfacesState()
	var currentNS, targetNS netns.NsHandle
	if len(ifStates) > 0 {
		currentNS, err = n.GetRunningNetns()
		if err != nil {
			return err
		}
		defer currentNS.Close()

		targetNS, err = n.GetLocalNetns()
		if err != nil {
			return err
		}
		defer targetNS.Close()

		if err := link.MoveInterfacesNetns(ifStates, currentNS, targetNS); err != nil {
			return fmt.Errorf("unable to attach interfaces: %v", err)
		}
	}

	ctx := context.Background()
	if err := client.Stop(ctx, n.ID); err != nil {
		if currentNS != 0 && targetNS != 0 {
			if rollbackErr := link.MoveInterfacesNetns(ifStates, targetNS, currentNS); rollbackErr != nil {
				n.Logger.Errorf("unable to roll back interface move after stop failure: %v", rollbackErr)
			}
		}
		return err
	}

	n.mu.Lock()
	n.Running = false
	n.ConfigLoaded = false
	n.mu.Unlock()
	n.pids.invalidate()

	return nil
}

func (n *DockerNode) Kill() error {
	if n.ID != "" && n.IsRunning() {
		n.Logger.Info("Kill: starting")
		start := time.Now()

		client, err := n.dockerClient()
		if err != nil {
			return err
		}

		ctx := context.Background()
		if err := client.Stop(ctx, n.ID); err != nil {
			return err
		}
		n.mu.Lock()
		n.Running = false
		n.mu.Unlock()
		n.pids.invalidate()
		n.Logger.Infof("Kill: done in %s", time.Since(start))
	}
	return nil
}

func (n *DockerNode) Close() error {
	n.Logger.Info("Close: starting")
	start := time.Now()

	if n.ID != "" {
		client, err := n.dockerClient()
		if err != nil {
			return err
		}

		// Force removal also stops a running container. Deleting the container
		// destroys its netns, so there is no need for a separate kill phase.
		if err := client.Rm(context.Background(), n.ID); err != nil {
			return err
		}
	}

	n.mu.Lock()
	n.Running = false
	n.ConfigLoaded = false
	n.mu.Unlock()
	n.pids.invalidate()
	n.interfaces.reset()
	n.deleteLocalNetns()

	n.Logger.Infof("Close: total done in %s", time.Since(start))
	return nil
}
