package server

import (
	"fmt"

	"github.com/satcomprojects/gonetem/internal/link"
)

func (t *NetemTopologyManager) LinkAdd(linkCfg LinkConfig, sync bool) error {
	if err := linkCfg.Validate(); err != nil {
		return err
	}

	// Independent adds only mutate their own kernel interfaces and link state.
	// Take the write side only when the topology file must be rewritten.
	if sync {
		t.opMu.Lock()
		defer t.opMu.Unlock()
	} else {
		t.opMu.RLock()
		defer t.opMu.RUnlock()
	}

	netnsCache := newNodeNetnsCache()
	defer netnsCache.closeAll()

	if err := t.linkAdd(linkCfg, netnsCache); err != nil {
		return err
	}

	if sync {
		return t.SynchroniseTopology()
	}
	return nil
}

// linkAdd creates one link. The caller must hold opMu (read or write side) and
// pass a cache that remains valid for the whole operation.
func (t *NetemTopologyManager) linkAdd(linkCfg LinkConfig, netnsCache *nodeNetnsCache) error {
	t.mu.Lock()
	t.ensureLinkIndexLocked()

	key, err := linkKeyFor(linkCfg.Peer1, linkCfg.Peer2)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	if t.linkIndex[key] != nil {
		t.mu.Unlock()
		return fmt.Errorf("this link already exist")
	}
	if t.pendingLinks == nil {
		t.pendingLinks = make(map[linkKey]struct{})
	}
	if _, pending := t.pendingLinks[key]; pending {
		t.mu.Unlock()
		return fmt.Errorf("link %s - %s is already being modified", linkCfg.Peer1, linkCfg.Peer2)
	}

	peer1Name, peer1Idx, err := splitPeer(linkCfg.Peer1)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	peer2Name, peer2Idx, err := splitPeer(linkCfg.Peer2)
	if err != nil {
		t.mu.Unlock()
		return err
	}

	peer1Node := t.getNodeLocked(peer1Name)
	if peer1Node == nil {
		t.mu.Unlock()
		return fmt.Errorf("node %s not found in the topology", peer1Name)
	}
	peer2Node := t.getNodeLocked(peer2Name)
	if peer2Node == nil {
		t.mu.Unlock()
		return fmt.Errorf("node %s not found in the topology", peer2Name)
	}

	if linkCfg.Buffer == 0.0 && linkCfg.Rate > 0 {
		// by default set limit buffer to 1.0 * BDP
		linkCfg.Buffer = 1.0
	}

	l := &NetemLink{
		Peer1: NetemLinkPeer{
			Node:    peer1Node,
			IfIndex: peer1Idx,
		},
		Peer2: NetemLinkPeer{
			Node:    peer2Node,
			IfIndex: peer2Idx,
		},
		HasPeer1Netem: false,
		HasPeer2Netem: false,
		HasPeer1Tbf:   false,
		HasPeer2Tbf:   false,
		Config:        linkCfg,
	}
	t.pendingLinks[key] = struct{}{}
	running := t.running
	t.mu.Unlock()

	if running {
		if err := t.setupLink(l, true, netnsCache); err != nil {
			t.mu.Lock()
			delete(t.pendingLinks, key)
			t.mu.Unlock()
			return err
		}
	}

	t.mu.Lock()
	delete(t.pendingLinks, key)
	t.links = append(t.links, l)
	t.linkIndex[key] = l
	t.mu.Unlock()

	return nil
}

func (t *NetemTopologyManager) LinkDel(linkCfg LinkConfig, sync bool) error {
	// Independent deletes can run concurrently; the write side is only needed
	// when the topology file is rewritten.
	if sync {
		t.opMu.Lock()
		defer t.opMu.Unlock()
	} else {
		t.opMu.RLock()
		defer t.opMu.RUnlock()
	}

	netnsCache := newNodeNetnsCache()
	defer netnsCache.closeAll()

	if err := t.linkDel(linkCfg, netnsCache); err != nil {
		return err
	}

	if sync {
		return t.SynchroniseTopology()
	}
	return nil
}

// linkDel removes one link. The caller must hold opMu (read or write side).
func (t *NetemTopologyManager) linkDel(linkCfg LinkConfig, netnsCache *nodeNetnsCache) (err error) {
	t.mu.Lock()
	t.ensureLinkIndexLocked()

	l, _, err := t.getLinkLocked(linkCfg.Peer1, linkCfg.Peer2)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	key, err := linkKeyOf(l)
	if err != nil {
		t.mu.Unlock()
		return err
	}

	// Remove the link from the lookup index and reserve its key before doing
	// kernel work. A concurrent add/delete/update must not pick the same link.
	delete(t.linkIndex, key)
	if t.pendingLinks == nil {
		t.pendingLinks = make(map[linkKey]struct{})
	}
	t.pendingLinks[key] = struct{}{}
	running := t.running
	t.mu.Unlock()

	l.mu.Lock()
	defer l.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pendingLinks, key)
		if err != nil && t.linkInListLocked(l) {
			// The kernel teardown failed: keep the link visible as before.
			t.linkIndex[key] = l
		}
		t.mu.Unlock()
	}()

	peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
	peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)

	if running {
		if err = t.deleteLink(l, netnsCache); err != nil {
			return err
		}
	}

	if err = l.Peer1.Node.DetachInterface(peer1IfName); err != nil {
		return err
	}
	if err = l.Peer2.Node.DetachInterface(peer2IfName); err != nil {
		return err
	}

	t.mu.Lock()
	t.removeLinkLocked(l)
	t.mu.Unlock()

	return nil
}

func applyLinkUpdateConfig(l *NetemLink, linkCfg LinkConfig) {
	// Normalize request orientation to the stored link orientation.
	storedPeer1 := fmt.Sprintf("%s.%d", l.Peer1.Node.GetName(), l.Peer1.IfIndex)
	if linkCfg.Peer1 != storedPeer1 {
		linkCfg.Peer1, linkCfg.Peer2 = linkCfg.Peer2, linkCfg.Peer1
		linkCfg.Peer1QoS, linkCfg.Peer2QoS = linkCfg.Peer2QoS, linkCfg.Peer1QoS
	}

	hasTopLevel := linkCfg.Delay != 0 || linkCfg.Jitter != 0 || linkCfg.Loss != 0.0 ||
		linkCfg.Rate != 0 || linkCfg.Buffer != 0.0 || linkCfg.Burst != 0 ||
		linkCfg.Limit != 0 || linkCfg.TxQueuelen != 0

	linkCfg.Peer1QoS = linkCfg.Peer1QoS.WithDefaults()
	linkCfg.Peer2QoS = linkCfg.Peer2QoS.WithDefaults()

	// Always replace all QoS fields, including zeroes, so a zero request clears
	// the previous configuration.
	l.Config.Peer1QoS = linkCfg.Peer1QoS
	l.Config.Peer2QoS = linkCfg.Peer2QoS
	l.Config.Delay = linkCfg.Delay
	l.Config.Jitter = linkCfg.Jitter
	l.Config.Loss = linkCfg.Loss
	l.Config.Rate = linkCfg.Rate
	l.Config.Buffer = linkCfg.Buffer
	l.Config.Burst = linkCfg.Burst
	l.Config.Limit = linkCfg.Limit
	l.Config.TxQueuelen = linkCfg.TxQueuelen

	// Preserve the historical behavior used by the tests: when the request has
	// no top-level QoS and both directions are identical, mirror the peer values
	// into the top-level fields.
	if !hasTopLevel && linkCfg.Peer1QoS == linkCfg.Peer2QoS {
		q := linkCfg.Peer1QoS
		l.Config.Delay = q.Delay
		l.Config.Jitter = q.Jitter
		l.Config.Loss = q.Loss
		l.Config.Rate = q.Rate
		l.Config.Buffer = q.Buffer
		l.Config.Burst = q.Burst
		l.Config.Limit = q.Limit
		l.Config.TxQueuelen = q.TxQueuelen
	}
}

func (t *NetemTopologyManager) LinkUpdate(linkCfg LinkConfig, sync bool) error {
	if err := linkCfg.Validate(); err != nil {
		return err
	}

	// Non-sync link updates only mutate per-link state and independent kernel
	// interfaces, so they can run concurrently with other link operations.
	// Sync updates rewrite the topology file, so keep those exclusive.
	if sync {
		t.opMu.Lock()
		defer t.opMu.Unlock()
	} else {
		t.opMu.RLock()
		defer t.opMu.RUnlock()
	}

	netnsCache := newNodeNetnsCache()
	defer netnsCache.closeAll()

	if err := t.linkUpdate(linkCfg, netnsCache); err != nil {
		return err
	}

	if sync {
		return t.SynchroniseTopology()
	}
	return nil
}

// linkUpdate applies one QoS change. The caller must hold opMu (read or write
// side) and pass a cache that remains valid for the whole operation.
func (t *NetemTopologyManager) linkUpdate(linkCfg LinkConfig, netnsCache *nodeNetnsCache) error {
	t.mu.RLock()
	l, _, err := t.getLinkLocked(linkCfg.Peer1, linkCfg.Peer2)
	t.mu.RUnlock()
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	var oldPeer1, oldPeer2, newPeer1, newPeer2 QoSConfig
	var running bool

	t.mu.Lock()
	oldPeer1 = l.Config.GetPeer1QoS()
	oldPeer2 = l.Config.GetPeer2QoS()
	applyLinkUpdateConfig(l, linkCfg)
	newPeer1 = l.Config.GetPeer1QoS()
	newPeer2 = l.Config.GetPeer2QoS()
	running = t.running
	t.mu.Unlock()

	if !running {
		return nil
	}

	if oldPeer1 != newPeer1 {
		peer1Netns, err := netnsCache.get(l.Peer1.Node)
		if err != nil {
			return err
		}

		peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
		if err := applyPeerQoSDiff(
			t.applyQoS,
			peer1IfName, peer1Netns, oldPeer1, newPeer1,
			&l.HasPeer1Tbf, &l.HasPeer1Netem,
		); err != nil {
			return err
		}
	}

	if oldPeer2 != newPeer2 {
		peer2Netns, err := netnsCache.get(l.Peer2.Node)
		if err != nil {
			return err
		}

		peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)
		if err := applyPeerQoSDiff(
			t.applyQoS,
			peer2IfName, peer2Netns, oldPeer2, newPeer2,
			&l.HasPeer2Tbf, &l.HasPeer2Netem,
		); err != nil {
			return err
		}
	}

	return nil
}

func (t *NetemTopologyManager) deleteLink(l *NetemLink, netnsCache *nodeNetnsCache) error {
	peer1Netns, err := netnsCache.get(l.Peer1.Node)
	if err != nil {
		return err
	}

	peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
	return link.DeleteLink(peer1IfName, peer1Netns)
}
