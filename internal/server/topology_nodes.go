package server

import (
	"fmt"
	"path"
	"strings"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

func (t *NetemTopologyManager) NodeAdd(name string, config NodeConfig, sync bool) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	t.mu.Lock()
	locked := true
	defer func() {
		if locked {
			t.mu.Unlock()
		}
	}()

	if t.getNodeLocked(name) != nil {
		return fmt.Errorf("node %s already exists in the topology", name)
	}

	existingNodes := make([]string, 0, len(t.nodes))
	for _, node := range t.nodes {
		existingNodes = append(existingNodes, node.Instance.GetName())
	}

	if err := checkNodeConfig(name, config, existingNodes); err != nil {
		return err
	}

	shortName, err := t.IdGenerator.GetId(name)
	if err != nil {
		return err
	}

	if config.Type == "ovs" {
		if err := t.ensureOvsInstance(t.running); err != nil {
			return fmt.Errorf("unable to initialize ovswitch instance: %w", err)
		}
	}

	node, err := t.createNode(name, shortName, config)
	if err != nil {
		if node != nil {
			node.Close()
		}
		return fmt.Errorf("unable to create node %s: %w", name, err)
	}

	netemNode := NetemNode{
		Instance:        node,
		LaunchAtStartup: config.Launch,
		Config:          config,
	}
	t.nodes = append(t.nodes, netemNode)

	if t.running {
		if config.Launch {
			if err := node.Start(); err != nil {
				node.Close()
				t.nodes = t.nodes[:len(t.nodes)-1]
				return fmt.Errorf("unable to start node %s: %w", name, err)
			}
		}
		if config.Mgnt.Enable {
			netnsCache := newNodeNetnsCache()
			err := t.setupMgntLink(&netemNode, netnsCache)
			netnsCache.closeAll()
			if err != nil {
				if config.Launch {
					if stopErr := node.Stop(); stopErr != nil {
						// Rollback is best effort; report it without hiding
						// the management-link error.
						logrus.Warnf("unable to stop node %s during rollback: %v", name, stopErr)
					}
				}
				node.Close()
				t.nodes = t.nodes[:len(t.nodes)-1]
				return fmt.Errorf("unable to setup mgnt link for node %s: %w", name, err)
			}
		}
	}

	if sync {
		// SynchroniseTopology takes the topology read lock; release the write
		// lock first.
		locked = false
		t.mu.Unlock()
		return t.SynchroniseTopology()
	}
	return nil
}

// deleteNodeLinks removes the kernel links and bridge peers attached to the
// node being deleted, then detaches the peer interfaces.
func deleteNodeLinks(
	name string,
	pending []*NetemLink,
	bridgePeerNames []string,
	running bool,
	netnsCache *nodeNetnsCache,
) error {
	if running {
		type deleteBatch struct {
			node  LinkNode
			names []string
		}
		batches := make(map[string]*deleteBatch)
		for _, l := range pending {
			node := l.Peer1.Node
			key := node.GetName()
			batch, ok := batches[key]
			if !ok {
				batch = &deleteBatch{node: node}
				batches[key] = batch
			}
			batch.names = append(batch.names, node.GetInterfaceName(l.Peer1.IfIndex))
		}

		for _, batch := range batches {
			ns, err := netnsCache.get(batch.node)
			if err != nil {
				return err
			}
			if err := link.DeleteLinks(batch.names, ns); err != nil {
				return fmt.Errorf("unable to delete links for node %s: %w", name, err)
			}
		}

		if len(bridgePeerNames) > 0 {
			rootNs := link.GetRootNetns()
			err := link.DeleteLinks(bridgePeerNames, rootNs)
			rootNs.Close()
			if err != nil {
				return fmt.Errorf("unable to delete bridge links for node %s: %w", name, err)
			}
		}
	}

	for _, l := range pending {
		peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
		peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)
		if err := l.Peer1.Node.DetachInterface(peer1IfName); err != nil {
			return fmt.Errorf("unable to detach interface %s from node %s: %w", peer1IfName, l.Peer1.Node.GetName(), err)
		}
		if err := l.Peer2.Node.DetachInterface(peer2IfName); err != nil {
			return fmt.Errorf("unable to detach interface %s from node %s: %w", peer2IfName, l.Peer2.Node.GetName(), err)
		}
	}

	return nil
}

// removeNodeLinksLocked drops the links and bridge peers/interfaces that refer
// to the deleted node. The caller must hold the topology write lock.
func (t *NetemTopologyManager) removeNodeLinksLocked(name string) {
	keptLinks := t.links[:0]
	for _, l := range t.links {
		if l.Peer1.Node.GetName() != name && l.Peer2.Node.GetName() != name {
			keptLinks = append(keptLinks, l)
		}
	}
	t.links = keptLinks
	t.linkIndex = nil
	t.ensureLinkIndexLocked()

	for _, br := range t.bridges {
		keptPeers := br.Peers[:0]
		for _, peer := range br.Peers {
			if peer.Node.GetName() != name {
				keptPeers = append(keptPeers, peer)
			}
		}
		br.Peers = keptPeers

		keptInterfaces := br.Config.Interfaces[:0]
		for _, ifName := range br.Config.Interfaces {
			if strings.Split(ifName, ".")[0] != name {
				keptInterfaces = append(keptInterfaces, ifName)
			}
		}
		br.Config.Interfaces = keptInterfaces
	}
}

func (t *NetemTopologyManager) NodeDel(name string, sync bool) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	var targetNode NetemNode
	var pending []*NetemLink
	var bridgePeerNames []string
	var running bool
	var mgntIfName string
	var mgntNet *MgntNetwork

	err := func() error {
		t.mu.Lock()
		defer t.mu.Unlock()

		found := false
		for _, node := range t.nodes {
			if node.Instance.GetName() == name {
				targetNode = node
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("node %s not found in the topology", name)
		}

		running = t.running
		mgntNet = t.mgntNet
		if targetNode.Config.Mgnt.Enable {
			mgntIfName = fmt.Sprintf("%s%s%s.m", options.NETEM_ID, t.prjID, targetNode.Instance.GetShortName())
		}

		for _, l := range t.links {
			if l.Peer1.Node.GetName() == name || l.Peer2.Node.GetName() == name {
				pending = append(pending, l)
			}
		}

		for _, br := range t.bridges {
			for _, peer := range br.Peers {
				if peer.Node.GetName() == name {
					bridgePeerNames = append(bridgePeerNames, fmt.Sprintf(
						"%s%s%s.%d", options.NETEM_ID, t.prjID,
						peer.Node.GetShortName(), peer.IfIndex,
					))
				}
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}

	netnsCache := newNodeNetnsCache()
	defer netnsCache.closeAll()

	if err := deleteNodeLinks(name, pending, bridgePeerNames, running, netnsCache); err != nil {
		return err
	}

	netnsCache.closeAll()

	// Commit link/bridge removals before closing the node. This preserves the
	// previous partial-failure behavior if the container cannot be removed.
	t.mu.Lock()
	t.removeNodeLinksLocked(name)
	t.mu.Unlock()

	if running && mgntNet != nil && mgntIfName != "" {
		if err := mgntNet.DetachInterface(mgntIfName); err != nil {
			return fmt.Errorf("unable to detach mgnt interface for node %s: %w", name, err)
		}
	}

	// Close the node outside the topology lock: force-removing a container can
	// take a while and must not block status/read operations.
	if err := targetNode.Instance.Close(); err != nil {
		return fmt.Errorf("unable to close node %s: %w", name, err)
	}

	t.mu.Lock()
	for i, node := range t.nodes {
		if node.Instance.GetName() == name {
			t.nodes = append(t.nodes[:i], t.nodes[i+1:]...)
			break
		}
	}
	t.mu.Unlock()

	if sync {
		return t.SynchroniseTopology()
	}
	return nil
}

func (t *NetemTopologyManager) startNode(node StartableNode) ([]string, error) {
	if err := node.Start(); err != nil {
		return []string{}, fmt.Errorf("unable to start node %s: %w", node.GetName(), err)
	}

	if err := node.ConfigureInterfaces(); err != nil {
		return []string{}, fmt.Errorf("unable to configure interfaces of node %s: %w", node.GetName(), err)
	}

	configPath := path.Join(t.path, configDir)
	timeout := t.config.DockerTimeout
	messages, err := node.LoadConfig(configPath, timeout)
	if err != nil {
		return messages, fmt.Errorf("unable to load config of node %s: %w", node.GetName(), err)
	}

	return messages, nil
}

func (t *NetemTopologyManager) stopNode(node StopableNode) error {
	if err := node.Stop(); err != nil {
		return fmt.Errorf("unable to stop node %s: %w", node.GetName(), err)
	}
	return nil
}

func (t *NetemTopologyManager) Start(nodeName string) ([]string, error) {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	if !t.IsRunning() {
		return []string{}, fmt.Errorf("topology is not running; start the topology first")
	}

	node := t.GetNode(nodeName)
	if node == nil {
		return []string{}, fmt.Errorf("node %s not found in the topology", nodeName)
	}

	return t.startNode(node)
}

func (t *NetemTopologyManager) Stop(nodeName string) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	if !t.IsRunning() {
		return fmt.Errorf("topology is not running")
	}

	node := t.GetNode(nodeName)
	if node == nil {
		return fmt.Errorf("node %s not found in the topology", nodeName)
	}

	return t.stopNode(node)
}

func (t *NetemTopologyManager) ReadConfigFiles(nodeName string) (map[string][]byte, error) {
	node := t.GetNode(nodeName)
	if node == nil {
		return map[string][]byte{}, fmt.Errorf("node %s not found in the topology", nodeName)
	}

	confPath := path.Join(t.path, configDir)
	timeout := t.config.DockerTimeout

	return node.ReadConfigFiles(confPath, timeout)
}
