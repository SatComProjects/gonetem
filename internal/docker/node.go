package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

type VrrpOptions struct {
	Interface int
	Group     int
	Address   string
}

type DockerNodeOptions struct {
	Name       string
	ShortName  string
	Ipv6       bool
	Mpls       bool
	Vrfs       []string
	Vrrps      []VrrpOptions
	Volumes    []string
	Image      string
	LazyCreate bool
}

type DockerNodeStatus struct {
	Running bool
}

type DockerInterface struct {
	Configured bool
	State      link.IfState
}

type DockerNode struct {
	PrjID          string
	ID             string
	Name           string
	ShortName      string
	Config         *options.DockerNodeConfig
	interfaces     interfaceTable
	mu             sync.RWMutex
	nsMu           sync.Mutex
	configMu       sync.Mutex
	LocalNetnsName string
	client         ContainerNodeAPI
	Running        bool
	pids           pidCache
	ConfigLoaded   bool
	ImageName      string
	IPv6           bool
	LazyCreate     bool
	Mpls           bool
	Vrfs           []string
	Vrrps          []VrrpOptions
	Volumes        []string
	Logger         *logrus.Entry
}

func (n *DockerNode) GetName() string {
	return n.Name
}

func (n *DockerNode) GetShortName() string {
	if n.ShortName == "" {
		return n.Name
	}
	return n.ShortName
}

func (n *DockerNode) GetType() string {
	return "docker"
}

func (n *DockerNode) GetFullType() string {
	return fmt.Sprintf("docker.%s", n.Config.Type)
}

func (n *DockerNode) GetStatus() (DockerNodeStatus, error) {
	if n.ID == "" {
		return DockerNodeStatus{Running: false}, nil
	}

	client, err := n.dockerClient()
	if err != nil {
		return DockerNodeStatus{}, err
	}

	state, err := client.GetState(context.Background(), n.ID)
	if err != nil {
		return DockerNodeStatus{}, err
	}

	return DockerNodeStatus{
		Running: state == "running",
	}, nil
}

func (n *DockerNode) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Running
}

func getDockerConfigFromType(nType string) (*options.DockerNodeConfig, error) {
	switch nType {
	case "router":
		return &options.ServerConfig.Docker.Nodes.Router, nil
	case "host":
		return &options.ServerConfig.Docker.Nodes.Host, nil
	case "server":
		return &options.ServerConfig.Docker.Nodes.Server, nil
	case "p4sw":
		return &options.ServerConfig.Docker.Nodes.P4sw, nil
	default:
		for _, nConfig := range options.ServerConfig.Docker.ExtraNodes {
			if nConfig.Type == nType {
				return &nConfig, nil
			}
		}
		return nil, fmt.Errorf("docker node of type %s does not exist in the configuration", nType)
	}
}

// dockerClient returns the injected Docker API or a clear error when a node
// was built directly without one (some unit tests inspect pure state).
func (n *DockerNode) dockerClient() (ContainerNodeAPI, error) {
	if n.client == nil {
		return nil, errors.New("docker client is not configured for node " + n.Name)
	}
	return n.client, nil
}

func NewDockerNode(client ContainerNodeAPI, prjID string, nType string, dockerOpts DockerNodeOptions) (*DockerNode, error) {
	nConfig, err := getDockerConfigFromType(nType)
	if err != nil {
		return nil, err
	}

	node := &DockerNode{
		PrjID:      prjID,
		client:     client,
		ID:         "",
		Name:       dockerOpts.Name,
		ShortName:  dockerOpts.ShortName,
		Config:     nConfig,
		Mpls:       dockerOpts.Mpls,
		Vrfs:       dockerOpts.Vrfs,
		Vrrps:      dockerOpts.Vrrps,
		Volumes:    dockerOpts.Volumes,
		interfaces: interfaceTable{entries: make(map[string]*DockerInterface)},
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    dockerOpts.Name,
		}),
	}

	imgName := options.GetDockerImageId(nConfig.Image)
	if dockerOpts.Image != "" {
		imgName = options.GetDockerImageId(dockerOpts.Image)
	}
	node.ImageName = imgName
	node.IPv6 = dockerOpts.Ipv6
	node.LazyCreate = dockerOpts.LazyCreate

	if !node.LazyCreate {
		if err := node.Create(imgName, dockerOpts.Ipv6); err != nil {
			return node, err
		}
	}
	return node, nil
}
