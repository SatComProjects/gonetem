package server

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
	"golang.org/x/sync/singleflight"
)

type nodeNetnsCache struct {
	mu      sync.Mutex
	handles map[string]netns.NsHandle
	group   singleflight.Group
}

func newNodeNetnsCache() *nodeNetnsCache {
	return &nodeNetnsCache{handles: make(map[string]netns.NsHandle)}
}

func (c *nodeNetnsCache) get(node NodeNetns) (netns.NsHandle, error) {
	name := node.GetName()

	c.mu.Lock()
	if handle, ok := c.handles[name]; ok {
		c.mu.Unlock()
		return handle, nil
	}
	c.mu.Unlock()

	value, err, _ := c.group.Do(name, func() (any, error) {
		c.mu.Lock()
		if handle, ok := c.handles[name]; ok {
			c.mu.Unlock()
			return handle, nil
		}
		c.mu.Unlock()

		handle, err := node.GetNetns()
		if err != nil {
			return netns.None(), err
		}

		c.mu.Lock()
		c.handles[name] = handle
		c.mu.Unlock()
		return handle, nil
	})
	if err != nil {
		return netns.None(), err
	}
	return value.(netns.NsHandle), nil
}

func (c *nodeNetnsCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for name, handle := range c.handles {
		if err := handle.Close(); err != nil {
			logrus.Warnf("unable to close netns of node %s: %v", name, err)
		}
		delete(c.handles, name)
	}
}

func (t *NetemTopologyManager) GetLink(peer1V string, peer2V string) (*NetemLink, int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getLinkLocked(peer1V, peer2V)
}

// linkKey is an order-independent identity for a link. Peer strings are
// normalized to "<node>.<ifIndex>" so "A.00" and "A.0" cannot create two
// entries for the same interface.
type linkKey struct {
	peer1 string
	peer2 string
}

func normalizePeer(peer string) (string, error) {
	name, idx, err := splitPeer(peer)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%d", name, idx), nil
}

func linkKeyFor(peer1V string, peer2V string) (linkKey, error) {
	peer1, err := normalizePeer(peer1V)
	if err != nil {
		return linkKey{}, err
	}
	peer2, err := normalizePeer(peer2V)
	if err != nil {
		return linkKey{}, err
	}
	if peer1 > peer2 {
		peer1, peer2 = peer2, peer1
	}
	return linkKey{peer1: peer1, peer2: peer2}, nil
}

func linkKeyOf(l *NetemLink) (linkKey, error) {
	if l.Peer1.Node == nil || l.Peer2.Node == nil {
		return linkKey{}, fmt.Errorf("link has a peer without a node")
	}
	return linkKeyFor(
		fmt.Sprintf("%s.%d", l.Peer1.Node.GetName(), l.Peer1.IfIndex),
		fmt.Sprintf("%s.%d", l.Peer2.Node.GetName(), l.Peer2.IfIndex),
	)
}

// ensureLinkIndexLocked builds the peer index from t.links when it is absent.
// Callers must hold t.mu for writing.
func (t *NetemTopologyManager) ensureLinkIndexLocked() {
	if t.linkIndex != nil {
		return
	}
	t.linkIndex = make(map[linkKey]*NetemLink, len(t.links))
	for _, l := range t.links {
		key, err := linkKeyOf(l)
		if err != nil {
			continue
		}
		t.linkIndex[key] = l
	}
}

// removeLinkLocked removes target from t.links by pointer identity and drops
// its index entry. Callers must hold t.mu for writing.
func (t *NetemTopologyManager) removeLinkLocked(target *NetemLink) bool {
	oldLen := len(t.links)
	kept := t.links[:0]
	found := false
	for _, l := range t.links {
		if l == target {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	t.links = kept
	// Drop references in the tail so a removed link can be collected.
	for idx := len(kept); idx < oldLen; idx++ {
		t.links[:oldLen][idx] = nil
	}

	if t.linkIndex != nil {
		if key, err := linkKeyOf(target); err == nil && t.linkIndex[key] == target {
			delete(t.linkIndex, key)
		}
	}
	return found
}

func (t *NetemTopologyManager) linkInListLocked(target *NetemLink) bool {
	for _, l := range t.links {
		if l == target {
			return true
		}
	}
	return false
}

func (t *NetemTopologyManager) getLinkLocked(peer1V string, peer2V string) (*NetemLink, int, error) {
	key, err := linkKeyFor(peer1V, peer2V)
	if err != nil {
		return nil, -1, err
	}

	if t.linkIndex != nil {
		if l := t.linkIndex[key]; l != nil {
			return l, -1, nil
		}
		return nil, -1, fmt.Errorf(
			"link %s - %s not found in the topology",
			peer1V, peer2V,
		)
	}

	// Compatibility fallback for managers built directly in tests. Production
	// managers always have an index built by Load or the link operations.
	peer1Name, peer1Idx, err := splitPeer(peer1V)
	if err != nil {
		return nil, -1, err
	}
	peer2Name, peer2Idx, err := splitPeer(peer2V)
	if err != nil {
		return nil, -1, err
	}

	for idx, l := range t.links {
		if l.Peer1.IfIndex == peer1Idx &&
			l.Peer1.Node.GetName() == peer1Name &&
			l.Peer2.IfIndex == peer2Idx &&
			l.Peer2.Node.GetName() == peer2Name {
			return l, idx, nil
		}
	}

	// check for inverse link
	for idx, l := range t.links {
		if l.Peer1.IfIndex == peer2Idx &&
			l.Peer1.Node.GetName() == peer2Name &&
			l.Peer2.IfIndex == peer1Idx &&
			l.Peer2.Node.GetName() == peer1Name {
			return l, idx, nil
		}
	}

	return nil, -1, fmt.Errorf(
		"link %s - %s not found in the topology",
		peer1V, peer2V,
	)
}

func (t *NetemTopologyManager) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

func (t *NetemTopologyManager) GetAllNodeStatuses() []NodeStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	statuses := make([]NodeStatus, len(t.nodes))
	for idx, node := range t.nodes {
		statuses[idx] = node.Instance
	}
	return statuses
}

func (t *NetemTopologyManager) GetAllNodes() []Node {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nodeInstances := make([]Node, len(t.nodes))
	for i := range t.nodes {
		nodeInstances[i] = t.nodes[i].Instance
	}
	return nodeInstances
}

func (t *NetemTopologyManager) IsNodeLaunchAtStartup(name string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for _, node := range t.nodes {
		if node.Instance.GetName() == name {
			return node.LaunchAtStartup
		}
	}
	return false
}

func (t *NetemTopologyManager) GetNode(name string) Node {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getNodeLocked(name)
}

func (t *NetemTopologyManager) getNodeLocked(name string) Node {
	for _, node := range t.nodes {
		if node.Instance.GetName() == name {
			return node.Instance
		}
	}
	return nil
}

// nodeDestroysNetnsOnClose reports whether closing a node also removes the
// network namespace that owns its link interfaces.
func nodeDestroysNetnsOnClose(node NodeIdentity) bool {
	switch node.GetType() {
	case "docker", "namespace":
		return true
	default:
		return false
	}
}
