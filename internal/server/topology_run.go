package server

import (
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/ovs"
	"golang.org/x/sync/errgroup"
)

func (t *NetemTopologyManager) ensureOvsInstance(start bool) error {
	if t.ovsInstance == nil {
		factory := t.ovsFactory
		if factory == nil {
			nodeFactory, err := NewNodeFactory()
			if err != nil {
				return err
			}
			factory = nodeFactory.CreateOvsInstance
		}
		instance, err := factory(t.prjID)
		if err != nil {
			return err
		}
		t.ovsInstance = instance
	}

	if start {
		return t.ovsInstance.Start()
	}
	return nil
}

func (t *NetemTopologyManager) Reload(progressCh chan TopologyRunCloseProgressT) ([]NodeRunMessages, error) {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	t.logger.Debug("Topo/Reload")

	var err error
	var nodeMessages []NodeRunMessages

	t.mu.RLock()
	wasRunning := t.running
	t.mu.RUnlock()

	if err = t.closeLocked(progressCh); err != nil {
		return nodeMessages, err
	}

	if err = t.Load(); err != nil {
		return nodeMessages, err
	}

	if wasRunning {
		return t.runLocked(progressCh)
	}

	return nodeMessages, nil
}

func (t *NetemTopologyManager) Run(progressCh chan TopologyRunCloseProgressT) ([]NodeRunMessages, error) {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	return t.runLocked(progressCh)
}

func (t *NetemTopologyManager) runLocked(progressCh chan TopologyRunCloseProgressT) (nodeMessages []NodeRunMessages, err error) {
	runStart := time.Now()
	t.logger.Debug("Topo/Run")
	progress := newProgressBatcher(progressCh)
	defer progress.Flush()
	progress.Set(NODE_COUNT, len(t.nodes))
	progress.Set(BRIDGE_COUNT, len(t.bridges))
	progress.Set(LINK_COUNT, len(t.links))

	t.mu.RLock()
	alreadyRunning := t.running
	t.mu.RUnlock()
	if alreadyRunning {
		t.logger.Warn("Topology is already running")
		return nodeMessages, nil
	}

	runSucceeded := false
	// The early "already running" return above must not trigger rollback, so
	// start the deferred cleanup only after that check.
	defer func() {
		if runSucceeded {
			return
		}

		t.logger.Warn("Topology run failed, rolling back runtime state")
		if closeErr := t.closeLocked(nil); closeErr != nil {
			t.logger.Errorf("unable to roll back runtime state: %v", closeErr)
		}
		if loadErr := t.Load(); loadErr != nil {
			t.logger.Errorf("unable to reload topology after failed run: %v", loadErr)
		}
	}()

	netnsCache := newNodeNetnsCache()
	defer netnsCache.closeAll()

	// 1 - create and start the ovswitch container when the topology contains a switch
	if t.needsOvs {
		ovsStart := time.Now()
		t.logger.Debug("Topo/Run: start ovswitch instance")
		if err := t.ensureOvsInstance(true); err != nil {
			return nodeMessages, err
		}
		t.logger.Debugf("Run: OVS start took %s", time.Since(ovsStart))
	}

	// 2 - optionnal - create mgnt network
	if t.mgntNet != nil {
		mgntStart := time.Now()
		if err := t.mgntNet.Create(); err != nil {
			return nodeMessages, err
		}
		t.logger.Debugf("Run: management network setup took %s", time.Since(mgntStart))
	}

	// 3 - start all required nodes
	nodeStartStart := time.Now()
	startGroup := new(errgroup.Group)
	startGroup.SetLimit(nodeStartLimit())

	t.logger.Debug("Topo/Run: start nodes")
	for _, node := range t.nodes {
		node := node
		startGroup.Go(func() error {
			nodeStart := time.Now()
			var err error = nil

			if node.LaunchAtStartup {
				err = node.Instance.Start()
			}

			if err == nil && node.Config.Mgnt.Enable {
				err = t.setupMgntLink(&node, netnsCache)
			}

			t.logger.Debugf("Run: node %s start took %s", node.Instance.GetName(), time.Since(nodeStart))

			if progressCh != nil {
				progress.Add(START_NODE)
			}
			return err
		})
	}
	if err := startGroup.Wait(); err != nil {
		return nodeMessages, err
	}
	t.logger.Debugf("Run: all node starts took %s", time.Since(nodeStartStart))

	// 3 - create links concurrently. Operations use thread-local namespaces
	// and restore them afterwards, so links can be scheduled independently
	// instead of being serialized by their source node.
	linkGroup := new(errgroup.Group)
	linkGroup.SetLimit(linkCreateLimit())
	linkStart := time.Now()

	t.logger.Debug("Topo/Run: setup links")
	for _, l := range t.links {
		l := l
		linkGroup.Go(func() error {
			setupStart := time.Now()
			if err := t.setupLink(l, false, netnsCache); err != nil {
				return err
			}
			t.logger.Debugf(
				"Run: link %s.%d-%s.%d setup took %s",
				l.Peer1.Node.GetName(), l.Peer1.IfIndex,
				l.Peer2.Node.GetName(), l.Peer2.IfIndex,
				time.Since(setupStart),
			)
			if progressCh != nil {
				progress.Add(SETUP_LINK)
			}
			return nil
		})
	}
	if err := linkGroup.Wait(); err != nil {
		return nodeMessages, err
	}
	t.logger.Debugf("Run: all link setups took %s", time.Since(linkStart))

	// 4 - create bridges
	bridgeGroup := new(errgroup.Group)
	bridgeGroup.SetLimit(linkCreateLimit())
	bridgeStart := time.Now()

	t.logger.Debug("Topo/Run: setup bridges")
	for _, br := range t.bridges {
		br := br
		bridgeGroup.Go(func() error {
			setupStart := time.Now()
			err := t.setupBridge(br, netnsCache)
			t.logger.Debugf("Run: bridge %s setup took %s", br.Name, time.Since(setupStart))

			if progressCh != nil {
				progress.Add(START_BRIDGE)
			}
			return err
		})
	}
	if err := bridgeGroup.Wait(); err != nil {
		return nodeMessages, err
	}
	t.logger.Debugf("Run: all bridge setups took %s", time.Since(bridgeStart))
	netnsCache.closeAll()

	// 5 - configure interfaces and load node configuration
	t.logger.Debug("Topo/Run: load configuration")
	timeout := options.ServerConfig.Docker.Timeoutop
	configPath := path.Join(t.path, configDir)

	configGroup := new(errgroup.Group)
	configGroup.SetLimit(nodeConfigLimit())
	configStart := time.Now()

	var msgMu sync.Mutex
	for _, node := range t.nodes {
		node := node
		configGroup.Go(func() error {
			loadStart := time.Now()
			defer func() {
				t.logger.Debugf("Run: node %s config took %s", node.Instance.GetName(), time.Since(loadStart))
			}()
			if !node.Instance.IsRunning() {
				if progressCh != nil {
					progress.Add(LOADCONFIG_NODE)
				}
				return nil
			}

			if err := node.Instance.ConfigureInterfaces(); err != nil {
				return fmt.Errorf("unable to configure interfaces of node %s: %w", node.Instance.GetName(), err)
			}

			messages, err := node.Instance.LoadConfig(configPath, timeout)
			if err != nil {
				return fmt.Errorf("unable to load config of node %s: %w", node.Instance.GetName(), err)
			}

			if len(messages) > 0 {
				msgMu.Lock()
				nodeMessages = append(nodeMessages, NodeRunMessages{
					Name:     node.Instance.GetName(),
					Messages: messages,
				})
				msgMu.Unlock()
			}

			if progressCh != nil {
				progress.Add(LOADCONFIG_NODE)
			}
			return nil
		})
	}
	if err := configGroup.Wait(); err != nil {
		return nodeMessages, err
	}
	t.logger.Debugf("Run: all node configs took %s", time.Since(configStart))

	t.mu.Lock()
	t.running = true
	t.mu.Unlock()
	runSucceeded = true
	t.logger.Debugf("Run: total took %s", time.Since(runStart))
	return nodeMessages, nil
}

func (t *NetemTopologyManager) setupBridge(br *NetemBridge, netnsCache *nodeNetnsCache) error {
	rootNs := link.GetRootNetns()
	defer rootNs.Close()

	brId, err := link.CreateBridge(br.Name, rootNs)
	if err != nil {
		return err
	}

	if err := link.AttachToBridge(brId, br.HostInterface, rootNs); err != nil {
		return fmt.Errorf("unable to attach HostIf to bridge %s: %v", br.Name, err)
	}

	for _, peer := range br.Peers {
		peerNetns, err := netnsCache.get(peer.Node)
		if err != nil {
			return err
		}

		ifName := fmt.Sprintf("%s%s%s.%d", options.NETEM_ID, t.prjID, peer.Node.GetShortName(), peer.IfIndex)
		peerIfName := peer.Node.GetInterfaceName(peer.IfIndex)
		veth, err := link.CreateVethLink(
			ifName, rootNs,
			peerIfName, peerNetns,
		)
		if err != nil {
			return fmt.Errorf(
				"unable to create link %s-%s.%d: %v",
				br.Name, peer.Node.GetName(), peer.IfIndex, err,
			)
		}

		// set both sides up: the root veth is attached to the bridge, the peer
		// lives in the node namespace and would otherwise stay DOWN
		if err := link.SetInterfaceState(veth.Name, rootNs, link.IFSTATE_UP); err != nil {
			return err
		}
		if err := link.SetInterfaceState(veth.PeerName, peerNetns, link.IFSTATE_UP); err != nil {
			return fmt.Errorf("unable to set node-side interface %s up: %w", veth.PeerName, err)
		}

		if err := link.AttachToBridge(brId, veth.Name, rootNs); err != nil {
			return err
		}

		if err := peer.Node.AttachInterface(peerIfName, peer.IfIndex, false); err != nil {
			return err
		}
	}

	return nil
}

func (t *NetemTopologyManager) setupMgntLink(node *NetemNode, netnsCache *nodeNetnsCache) error {
	if t.mgntNet == nil {
		return nil
	}

	rootNs := link.GetRootNetns()
	defer rootNs.Close()

	peerNetns, err := netnsCache.get(node.Instance)
	if err != nil {
		return err
	}

	mgntIfname := fmt.Sprintf("%s%s%s.m", options.NETEM_ID, t.prjID, node.Instance.GetShortName())
	peerIfname := "mgnt"
	veth, err := link.CreateVethLink(
		mgntIfname, rootNs,
		peerIfname, peerNetns,
	)
	if err != nil {
		return fmt.Errorf(
			"unable to create mgnt link for node %s: %v",
			node.Instance.GetName(), err,
		)
	}

	// set interface up
	if err := link.SetInterfaceState(veth.Name, rootNs, link.IFSTATE_UP); err != nil {
		return err
	}
	if err := link.SetInterfaceState(veth.PeerName, peerNetns, link.IFSTATE_UP); err != nil {
		return err
	}

	if err := t.mgntNet.AttachInterface(veth.Name); err != nil {
		return err
	}
	if err := node.Instance.AttachMgntInterface(peerIfname, peerNetns, node.Config.Mgnt.Address); err != nil {
		return fmt.Errorf(
			"unable to attach management interface for node %s: %w",
			node.Instance.GetName(), err,
		)
	}

	return nil
}

func (t *NetemTopologyManager) setupLink(l *NetemLink, configure bool, netnsCache *nodeNetnsCache) error {
	peer1Netns, err := netnsCache.get(l.Peer1.Node)
	if err != nil {
		return err
	}

	peer2Netns, err := netnsCache.get(l.Peer2.Node)
	if err != nil {
		return err
	}

	peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
	peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)
	_, err = link.CreateVethLink(peer1IfName, peer1Netns, peer2IfName, peer2Netns)
	if err != nil {
		return fmt.Errorf(
			"unable to create link %s.%d-%s.%d: %v",
			l.Peer1.Node.GetName(), l.Peer1.IfIndex,
			l.Peer2.Node.GetName(), l.Peer2.IfIndex,
			err,
		)
	}

	// apply both qdiscs with one namespace transition/socket per peer
	if err := l.ApplyPeer1QoS(t.applyQoS, peer1IfName, peer1Netns, false); err != nil {
		return err
	}
	if err := l.ApplyPeer2QoS(t.applyQoS, peer2IfName, peer2Netns, false); err != nil {
		return err
	}

	// set interface up
	if err := link.SetInterfaceState(peer1IfName, peer1Netns, link.IFSTATE_UP); err != nil {
		return err
	}
	if err := link.SetInterfaceState(peer2IfName, peer2Netns, link.IFSTATE_UP); err != nil {
		return err
	}

	// attach interfaces to nodes
	if err := l.Peer1.Node.AttachInterface(peer1IfName, l.Peer1.IfIndex, configure); err != nil {
		return err
	}
	if err := l.Peer2.Node.AttachInterface(peer2IfName, l.Peer2.IfIndex, configure); err != nil {
		return err
	}

	return nil
}

func (t *NetemTopologyManager) Close(progressCh chan TopologyRunCloseProgressT) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	return t.closeLocked(progressCh)
}

func (t *NetemTopologyManager) closeLocked(progressCh chan TopologyRunCloseProgressT) error {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()

	progress := newProgressBatcher(progressCh)
	defer progress.Flush()
	progress.Set(NODE_COUNT, len(t.nodes))
	progress.Set(BRIDGE_COUNT, len(t.bridges))

	closeStart := time.Now()
	t.logger.Infof("Close: starting (nodes=%d, bridges=%d)", len(t.nodes), len(t.bridges))

	// Remove all nodes concurrently. DockerNode.Close force-removes the
	// container; there is no separate kill phase anymore.
	t.logger.Infof("Close: removing %d nodes", len(t.nodes))
	removeStart := time.Now()
	g := new(errgroup.Group)
	g.SetLimit(nodeRemoveLimit())
	var failedNodes sync.Map
	for _, node := range t.nodes {
		node := node
		g.Go(func() error {
			nodeCloseStart := time.Now()
			err := node.Instance.Close()
			t.logger.Debugf("Close: node %s removal took %s", node.Instance.GetName(), time.Since(nodeCloseStart))
			if progressCh != nil {
				progress.Add(CLOSE_NODE)
			}
			if err != nil {
				failedNodes.Store(node.Instance.GetName(), struct{}{})
				return fmt.Errorf("node %s: %v", node.Instance.GetName(), err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.logger.Errorf("Error when closing nodes: %v", err)
	}
	t.logger.Infof("Close: node removal done in %s", time.Since(removeStart))

	// close all bridges
	t.logger.Infof("Close: phase 3 - removing %d bridges", len(t.bridges))
	bridgeStart := time.Now()
	rootNs := link.GetRootNetns()
	defer rootNs.Close()

	var bridgePeerNames []string
	for _, br := range t.bridges {
		for _, peer := range br.Peers {
			// A successfully removed Docker or namespace node already
			// destroyed its veth peers with its netns. Only clean up failed
			// nodes and OVS peers (the shared OVS container is removed later).
			_, failed := failedNodes.Load(peer.Node.GetName())
			if nodeDestroysNetnsOnClose(peer.Node) && !failed {
				continue
			}

			bridgePeerNames = append(bridgePeerNames, fmt.Sprintf(
				"%s%s%s.%d", options.NETEM_ID, t.prjID,
				peer.Node.GetShortName(), peer.IfIndex,
			))
		}
	}

	bridgeNames := make([]string, 0, len(t.bridges))
	for _, br := range t.bridges {
		bridgeNames = append(bridgeNames, br.Name)
	}

	if err := link.DeleteLinks(append(bridgePeerNames, bridgeNames...), rootNs); err != nil {
		t.logger.Warnf("Error when deleting bridge links: %v", err)
	}

	for range t.bridges {
		if progressCh != nil {
			progress.Add(CLOSE_BRIDGE)
		}
	}
	t.logger.Infof("Close: phase 3 done in %s", time.Since(bridgeStart))

	t.mu.Lock()
	t.nodes = make([]NetemNode, 0)
	t.links = make([]*NetemLink, 0)
	t.linkIndex = nil
	t.pendingLinks = nil
	t.bridges = make([]*NetemBridge, 0)
	t.needsOvs = false
	t.mu.Unlock()
	t.IdGenerator.Close()

	// close OVS instance if this project created one
	if t.ovsInstance != nil {
		t.logger.Info("Close: closing OVS instance")
		ovsStart := time.Now()
		if err := ovs.CloseOvsInstance(t.prjID); err != nil {
			t.logger.Warnf("Error when closing ovswitch instance: %v", err)
		}
		t.ovsInstance = nil
		t.logger.Infof("Close: OVS done in %s", time.Since(ovsStart))
	}

	// close mgnt network
	if t.mgntNet != nil {
		t.logger.Info("Close: closing mgnt network")
		mgntStart := time.Now()
		if err := t.mgntNet.Close(); err != nil {
			t.logger.Warnf("Error when closing mgnt network: %v", err)
		}
		t.mgntNet = nil
		t.logger.Infof("Close: mgnt network done in %s", time.Since(mgntStart))
	}

	t.logger.Infof("Close: total done in %s", time.Since(closeStart))
	return nil
}
