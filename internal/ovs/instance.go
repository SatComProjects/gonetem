package ovs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/sirupsen/logrus"
)

type State int

const (
	created State = 1 << iota
	started
)

const (
	maxAttempts = 10
)

// OvsProjectInstance is the OVS adapter for one project. The Docker client is
// injected by the composition root; the instance never constructs one.
//
// mu guards state and bridges. Start holds it for the whole transition so two
// concurrent calls cannot start the container twice; bridge methods hold it
// while they execute the corresponding ovs-vsctl command and update the
// bookkeeping. Exec and the other Docker calls do not take mu on their own, so
// callers may invoke them while the bridge lock is held.
type OvsProjectInstance struct {
	prjID       string
	containerId string
	client      docker.ContainerNodeAPI

	mu      sync.RWMutex
	state   State
	bridges []string

	Logger *logrus.Entry
}

func (o *OvsProjectInstance) dockerClient() (docker.ContainerNodeAPI, error) {
	if o.client == nil {
		return nil, fmt.Errorf("docker client is not configured for OVS instance %s", o.prjID)
	}
	return o.client, nil
}

// isStarted reports whether the OVS server container finished starting.
func (o *OvsProjectInstance) isStarted() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state == started
}

// Start starts the OVS server container and waits until ovs-vsctl responds.
// The state lock is held for the whole transition, so concurrent Start calls
// start the container once and bridge updates never observe a partially
// started instance.
func (o *OvsProjectInstance) Start() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.state == started {
		return nil
	}

	client, err := o.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if err := client.Start(ctx, o.containerId); err != nil {
		return err
	}

	// wait that ovs server has been ready before return
	attempt := 0
	infoCmd := []string{"ovs-vsctl", "show"}
	for attempt < maxAttempts {
		o.Logger.Debugf("Wait ovs-server is ready - attempt %d\n", attempt)
		if err = o.Exec(infoCmd); err == nil {
			break
		}
		attempt++
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	o.state = started

	return nil
}

func (o *OvsProjectInstance) Close() error {
	client, err := o.dockerClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Rm(ctx, o.containerId); err != nil {
		return err
	}

	return nil
}
