package server

import (
	"fmt"
	"regexp"
	"sync"

	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/namespace"
	"github.com/satcomprojects/gonetem/internal/ovs"
)

var (
	characterList = []string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "x", "y", "z",
	}
)

type NodeNotFoundError struct {
	prjId string
	name  string
}

func (e *NodeNotFoundError) Error() string {
	return fmt.Sprintf("Node %s not found in project %s", e.name, e.prjId)
}

type NodeIdentifierGenerator struct {
	lock    *sync.Mutex
	usedIds []string
}

func (nIdGen *NodeIdentifierGenerator) isIdExist(id string) bool {
	for _, nId := range nIdGen.usedIds {
		if nId == id {
			return true
		}
	}
	return false
}

func (nIdGen *NodeIdentifierGenerator) GetId(name string) (string, error) {
	nIdGen.lock.Lock()
	defer nIdGen.lock.Unlock()

	genId := ""
	if len(name) <= 5 {
		genId = name
		if !nIdGen.isIdExist(genId) {
			nIdGen.usedIds = append(nIdGen.usedIds, genId)
			return genId, nil
		}
	}

	for _, idx := range [3]int{2, 3, 1} {
		genId = name[:idx] + name[len(name)-(4-idx):]
		for _, char := range characterList {
			genId := fmt.Sprintf("%s%s", char, genId)
			if !nIdGen.isIdExist(genId) {
				nIdGen.usedIds = append(nIdGen.usedIds, genId)
				return genId, nil
			}
		}
	}

	return "", fmt.Errorf("unable to generate a short id for node %s: all attempts fail", name)
}

func (nIdGen *NodeIdentifierGenerator) Close() {
	nIdGen.lock.Lock()
	defer nIdGen.lock.Unlock()

	nIdGen.usedIds = make([]string, 0)
}

// NodeFactory builds node adapters with their explicit dependencies. The
// composition root (Phase 8) will construct it once and pass it down.
type NodeFactory struct {
	docker docker.ContainerNodeAPI
	config docker.RuntimeConfig
}

// NewNodeFactory builds a factory from the injected Docker client and adapter
// configuration. It does not contact the daemon.
func NewNodeFactory(client docker.ContainerNodeAPI, config docker.RuntimeConfig) *NodeFactory {
	return &NodeFactory{docker: client, config: config}
}

// CreateOvsInstance creates the project OVS instance with the factory client.
func (f *NodeFactory) CreateOvsInstance(prjID string) (*ovs.OvsProjectInstance, error) {
	if f.docker == nil {
		return nil, fmt.Errorf("docker client is not configured for OVS instance")
	}
	return ovs.NewOvsInstance(f.docker, f.config, prjID)
}

// Create builds one node from its topology configuration.
func (f *NodeFactory) Create(prjID string, name string, shortName string, config NodeConfig) (Node, error) {
	// first test if it is a docker node
	re := regexp.MustCompile(`^docker\.(\w+)$`)
	groups := re.FindStringSubmatch(config.Type)
	if len(groups) == 2 {
		// Create docker node
		options := docker.DockerNodeOptions{
			Name:       name,
			ShortName:  shortName,
			Ipv6:       config.IPv6,
			Mpls:       config.Mpls,
			Vrfs:       config.Vrfs,
			Volumes:    config.Volumes,
			Image:      config.Image,
			LazyCreate: !config.Launch,
		}
		for _, group := range config.Vrrps {
			options.Vrrps = append(options.Vrrps, docker.VrrpOptions{
				Interface: group.Interface,
				Group:     group.Group,
				Address:   group.Address,
			})
		}

		if f.docker == nil {
			return nil, fmt.Errorf("docker client is not configured for node %s", name)
		}
		return docker.NewDockerNode(f.docker, f.config, prjID, groups[1], options)
	}

	// then test if it is a switch
	if config.Type == "ovs" {
		return ovs.NewOvsNode(prjID, name, shortName, f.config.Workdir)
	}

	// finally test if it is a pure network namespace
	if config.Type == "namespace" {
		return namespace.NewNamespaceNode(prjID, name, shortName), nil
	}

	return nil, fmt.Errorf("unknown node type '%s'", config.Type)
}
