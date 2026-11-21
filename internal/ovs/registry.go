package ovs

import (
	"context"
	"fmt"
	"sync"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

// Registry owns the OVS instance created for each project. It is the only
// shared mutable state in this package and is injectable through NewRegistry.
type Registry struct {
	mu        sync.Mutex
	instances map[string]*OvsProjectInstance
}

// NewRegistry returns an empty instance registry.
func NewRegistry() *Registry {
	return &Registry{instances: make(map[string]*OvsProjectInstance)}
}

// Get returns the instance registered for prjID, or nil.
func (r *Registry) Get(prjID string) *OvsProjectInstance {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.instances[prjID]
}

// Create validates the image, creates the OVS container and registers the
// instance for prjID. Creation is serialized so two callers cannot race for
// the same project.
func (r *Registry) Create(client docker.ContainerNodeAPI, prjID string) (*OvsProjectInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.instances[prjID]; ok {
		return nil, fmt.Errorf("ovswitch container already exists")
	}

	if client == nil {
		return nil, fmt.Errorf("docker client is required to create an OVS instance")
	}

	imgName := options.GetDockerImageId(options.ServerConfig.Docker.OvsImage)
	present, err := client.IsImagePresent(context.Background(), imgName)
	if err != nil {
		return nil, err
	} else if !present {
		return nil, fmt.Errorf("ovswitch image %s not present", imgName)
	}

	containerName := fmt.Sprintf("%s%s.ovs", options.NETEM_ID, prjID)
	containerId, err := client.Create(
		context.Background(),
		imgName,
		containerName,
		"ovs",
		[]string{},
		false,
		false)
	if err != nil {
		return nil, err
	}

	instance := &OvsProjectInstance{
		prjID:       prjID,
		containerId: containerId,
		client:      client,
		state:       created,
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    "ovs-instance",
		}),
	}
	if r.instances == nil {
		r.instances = make(map[string]*OvsProjectInstance)
	}
	r.instances[prjID] = instance

	return instance, nil
}

// Close removes prjID from the registry and removes its container. The entry
// is forgotten even when container removal fails, matching the historical
// behaviour: the project close path must not keep a dead instance registered.
func (r *Registry) Close(prjID string) error {
	r.mu.Lock()
	ovs, ok := r.instances[prjID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("ovswitch container not found")
	}
	delete(r.instances, prjID)
	r.mu.Unlock()

	return ovs.Close()
}

// defaultRegistry backs the package-level helpers. It is the single documented
// package-level mutable value; a later phase injects a Registry through the
// server composition root and the wrappers will be removed.
var defaultRegistry = NewRegistry()

// NewOvsInstance creates an OVS instance in the default registry.
func NewOvsInstance(client docker.ContainerNodeAPI, prjID string) (*OvsProjectInstance, error) {
	return defaultRegistry.Create(client, prjID)
}

// GetOvsInstance returns the OVS instance registered for prjID, or nil.
func GetOvsInstance(prjID string) *OvsProjectInstance {
	return defaultRegistry.Get(prjID)
}

// CloseOvsInstance removes the OVS instance for prjID and its container.
func CloseOvsInstance(prjID string) error {
	return defaultRegistry.Close(prjID)
}
