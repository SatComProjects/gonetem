package server

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creasty/defaults"
	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/ovs"
	"github.com/mroy31/gonetem/internal/proto"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"gopkg.in/yaml.v3"
)

const (
	networkFilename       = "network.yml"
	configDir             = "configs"
	maxConcurrentNodeTask = 30
)

func concurrencyLimit(configured int) int {
	if configured > 0 {
		return configured
	}
	return maxConcurrentNodeTask
}

func nodeCreateLimit() int { return concurrencyLimit(options.ServerConfig.Docker.CreateConcurrency) }
func nodeStartLimit() int  { return concurrencyLimit(options.ServerConfig.Docker.StartConcurrency) }
func nodeRemoveLimit() int { return concurrencyLimit(options.ServerConfig.Docker.RemoveConcurrency) }
func nodeConfigLimit() int { return concurrencyLimit(options.ServerConfig.Docker.ConfigConcurrency) }
func linkCreateLimit() int { return concurrencyLimit(options.ServerConfig.Docker.LinkConcurrency) }

type QoSConfig struct {
	Loss   float64 `yaml:",omitempty"` // percent
	Delay  int     `yaml:",omitempty"` // ms
	Jitter int     `yaml:",omitempty"` // ms
	Rate   int     `yaml:",omitempty"` // kbps
	Buffer float64 `yaml:",omitempty"` // BDP scale factor
	Burst  int     `yaml:",omitempty"` // bytes
}

type LinkConfig struct {
	Peer1    string
	Peer2    string
	Loss     float64 `yaml:",omitempty"` // percent
	Delay    int     `yaml:",omitempty"` // ms
	Jitter   int     `yaml:",omitempty"` // ms
	Rate     int     `yaml:",omitempty"` // kbps
	Buffer   float64 `yaml:",omitempty"` // BDP scale factor
	Burst    int     `yaml:",omitempty"` // bytes
	Peer1QoS QoSConfig
	Peer2QoS QoSConfig
}

const (
	maxDelayMS = 274_000 // keeps tick conversion inside uint32
	// 17_000_000 * 125 = 2.125e9, which also fits a 32-bit signed int.
	// If higher rates are needed, compute rate*125 in uint64 in tc.go.
	maxRateKbps = 17_000_000
)

func (q QoSConfig) WithDefaults() QoSConfig {
	if q.Rate > 0 && q.Buffer == 0 {
		q.Buffer = 1.0
	}
	return q
}

func (q QoSConfig) validate(prefix string) error {
	if q.Delay < 0 || q.Delay > maxDelayMS {
		return fmt.Errorf("%s delay must be in [0, %d] ms", prefix, maxDelayMS)
	}
	if q.Jitter < 0 || q.Jitter > maxDelayMS {
		return fmt.Errorf("%s jitter must be in [0, %d] ms", prefix, maxDelayMS)
	}
	if q.Jitter > 0 && q.Delay == 0 {
		return fmt.Errorf("%s jitter requires a non-zero delay", prefix)
	}
	if q.Loss < 0 || q.Loss > 100 {
		return fmt.Errorf("%s loss must be in [0, 100] percent", prefix)
	}
	if q.Rate < 0 || q.Rate > maxRateKbps {
		return fmt.Errorf("%s rate must be in [0, %d] kbps", prefix, maxRateKbps)
	}
	if q.Rate > 0 && q.Delay == 0 {
		return fmt.Errorf("%s rate requires a non-zero delay", prefix)
	}
	if q.Buffer < 0 {
		return fmt.Errorf("%s buffer must be >= 0", prefix)
	}
	if q.Buffer > 0 && q.Rate == 0 {
		return fmt.Errorf("%s buffer requires a non-zero rate", prefix)
	}
	if q.Burst < 0 {
		return fmt.Errorf("%s burst must be >= 0", prefix)
	}
	if q.Burst > 0 && q.Rate == 0 {
		return fmt.Errorf("%s burst requires a non-zero rate", prefix)
	}
	return nil
}

func (l LinkConfig) Validate() error {
	top := QoSConfig{
		Loss:   l.Loss,
		Delay:  l.Delay,
		Jitter: l.Jitter,
		Rate:   l.Rate,
		Buffer: l.Buffer,
		Burst:  l.Burst,
	}
	if err := top.validate("link"); err != nil {
		return err
	}
	if err := l.Peer1QoS.validate("link peer1"); err != nil {
		return err
	}
	return l.Peer2QoS.validate("link peer2")
}

func (l *LinkConfig) GetPeer1QoS() QoSConfig {
	if l.Peer1QoS.Loss > 0 ||
		l.Peer1QoS.Delay > 0 ||
		l.Peer1QoS.Jitter > 0 ||
		l.Peer1QoS.Rate > 0 ||
		l.Peer1QoS.Buffer > 0 ||
		l.Peer1QoS.Burst > 0 {
		return l.Peer1QoS.WithDefaults()
	}

	return QoSConfig{
		Loss:   l.Loss,
		Delay:  l.Delay,
		Jitter: l.Jitter,
		Rate:   l.Rate,
		Buffer: l.Buffer,
		Burst:  l.Burst,
	}.WithDefaults()
}

func (l *LinkConfig) GetPeer2QoS() QoSConfig {
	if l.Peer2QoS.Loss > 0 ||
		l.Peer2QoS.Delay > 0 ||
		l.Peer2QoS.Jitter > 0 ||
		l.Peer2QoS.Rate > 0 ||
		l.Peer2QoS.Buffer > 0 ||
		l.Peer2QoS.Burst > 0 {
		return l.Peer2QoS.WithDefaults()
	}

	return QoSConfig{
		Loss:   l.Loss,
		Delay:  l.Delay,
		Jitter: l.Jitter,
		Rate:   l.Rate,
		Buffer: l.Buffer,
		Burst:  l.Burst,
	}.WithDefaults()
}

type BridgeConfig struct {
	Host       string
	Interfaces []string `yaml:",omitempty"`
}

type MgntNetworkConfig struct {
	Enable  bool   `yaml:",omitempty" default:"false"`
	Address string `yaml:",omitempty"`
}
type VrrpOptions struct {
	Interface int
	Group     int
	Address   string
}

type MgntOptions struct {
	Enable  bool   `yaml:",omitempty" default:"false"`
	Address string `yaml:",omitempty"`
}

type NodeConfig struct {
	Type    string
	IPv6    bool          `yaml:",omitempty" default:"true"`
	Mpls    bool          `yaml:",omitempty" default:"false"`
	Vrfs    []string      `yaml:",omitempty"`
	Vrrps   []VrrpOptions `yaml:",omitempty"`
	Volumes []string      `yaml:",omitempty"`
	Image   string        `yaml:",omitempty"`
	Launch  bool          `default:"true"`
	Mgnt    MgntOptions   `yaml:",omitempty"`
}

func (n *NodeConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	defaults.Set(n)

	type plain NodeConfig
	if err := unmarshal((*plain)(n)); err != nil {
		return err
	}

	return nil
}

type RunCloseProgressCode int
type SaveProgressCode int
type CloseProgressCode int

const (
	NODE_COUNT      RunCloseProgressCode = 1
	BRIDGE_COUNT    RunCloseProgressCode = 3
	LINK_COUNT      RunCloseProgressCode = 4
	LOAD_TOPO       RunCloseProgressCode = 5
	START_NODE      RunCloseProgressCode = 6
	SETUP_LINK      RunCloseProgressCode = 7
	START_BRIDGE    RunCloseProgressCode = 8
	LOADCONFIG_NODE RunCloseProgressCode = 10
	CLOSE_NODE      RunCloseProgressCode = 11
	CLOSE_BRIDGE    RunCloseProgressCode = 12
)

type TopologyRunCloseProgressT struct {
	Code  RunCloseProgressCode
	Value int
}

type progressBatcher struct {
	ch       chan TopologyRunCloseProgressT
	mu       sync.Mutex
	pending  map[RunCloseProgressCode]int
	lastSent time.Time
	interval time.Duration
}

func newProgressBatcher(ch chan TopologyRunCloseProgressT) *progressBatcher {
	return &progressBatcher{
		ch:       ch,
		pending:  make(map[RunCloseProgressCode]int),
		lastSent: time.Now(),
		interval: 100 * time.Millisecond,
	}
}

func (b *progressBatcher) Add(code RunCloseProgressCode) {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.pending[code]++
	if time.Since(b.lastSent) >= b.interval {
		b.flushLocked()
	}
}

func (b *progressBatcher) Set(code RunCloseProgressCode, value int) {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.flushLocked()
	b.ch <- TopologyRunCloseProgressT{Code: code, Value: value}
	b.lastSent = time.Now()
}

func (b *progressBatcher) Flush() {
	if b == nil || b.ch == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.flushLocked()
}

func (b *progressBatcher) flushLocked() {
	for code, count := range b.pending {
		if count > 0 {
			b.ch <- TopologyRunCloseProgressT{Code: code, Value: count}
		}
		delete(b.pending, code)
	}
	b.lastSent = time.Now()
}

const (
	NODE_SAVE_COUNT SaveProgressCode = 1
	NODE_SAVE       SaveProgressCode = 2
)

type TopologySaveProgressT struct {
	Code  SaveProgressCode
	Value int
}

type NetemTopology struct {
	Nodes   map[string]NodeConfig   `yaml:",omitempty"`
	Links   []LinkConfig            `yaml:",omitempty"`
	Bridges map[string]BridgeConfig `yaml:",omitempty"`
	Mgntnet MgntNetworkConfig       `yaml:",omitempty"`
}

type NetemLinkPeer struct {
	Node    INetemNode
	IfIndex int
}

type NetemLink struct {
	Peer1         NetemLinkPeer
	Peer2         NetemLinkPeer
	Config        LinkConfig
	HasPeer1Netem bool
	HasPeer2Netem bool
	HasPeer1Tbf   bool
	HasPeer2Tbf   bool
	mu            sync.Mutex // serializes updates to the fields above
}

func netemRequired(q QoSConfig) bool {
	return q.Delay > 0 || q.Jitter > 0 || q.Loss > 0
}

func qosDiff(old, next QoSConfig, hasTbf, hasNetem bool) (reset, changeTbf, changeNetem, changed bool) {
	if old == next {
		return false, false, false, false
	}

	oldTbf := old.Rate > 0
	newTbf := next.Rate > 0
	oldNetem := netemRequired(old)
	newNetem := netemRequired(next)

	reset = oldTbf != newTbf || oldNetem != newNetem ||
		hasTbf != oldTbf || hasNetem != oldNetem

	changeTbf = !reset && newTbf
	changeNetem = !reset && newNetem
	return reset, changeTbf, changeNetem, true
}

func applyPeerQoSDiff(ifName string, ns netns.NsHandle, old, next QoSConfig, hasTbf, hasNetem *bool) error {
	reset, changeTbf, changeNetem, changed := qosDiff(old, next, *hasTbf, *hasNetem)
	if !changed {
		return nil
	}

	updatedTbf, updatedNetem, err := link.ApplyQoS(
		ifName, ns, toLinkQoS(next), reset, changeTbf, changeNetem,
	)
	if err != nil {
		return err
	}

	*hasTbf = updatedTbf
	*hasNetem = updatedNetem
	return nil
}

func toLinkQoS(q QoSConfig) link.QoS {
	return link.QoS{
		Delay:  q.Delay,
		Jitter: q.Jitter,
		Loss:   q.Loss,
		Rate:   q.Rate,
		Buffer: q.Buffer,
		Burst:  q.Burst,
	}
}

func (l *NetemLink) ApplyPeer1QoS(ifName string, ns netns.NsHandle, reset bool) error {
	q := l.Config.GetPeer1QoS()
	hasTbf, hasNetem, err := link.ApplyQoS(
		ifName, ns, toLinkQoS(q), reset, l.HasPeer1Tbf, l.HasPeer1Netem,
	)
	if err != nil {
		return err
	}
	l.HasPeer1Tbf = hasTbf
	l.HasPeer1Netem = hasNetem
	return nil
}

func (l *NetemLink) ApplyPeer2QoS(ifName string, ns netns.NsHandle, reset bool) error {
	q := l.Config.GetPeer2QoS()
	hasTbf, hasNetem, err := link.ApplyQoS(
		ifName, ns, toLinkQoS(q), reset, l.HasPeer2Tbf, l.HasPeer2Netem,
	)
	if err != nil {
		return err
	}
	l.HasPeer2Tbf = hasTbf
	l.HasPeer2Netem = hasNetem
	return nil
}

type NetemBridge struct {
	Name          string // generated kernel bridge name
	ConfigName    string // original key in network.yml
	HostInterface string
	Peers         []NetemLinkPeer
	Config        BridgeConfig
}

func splitPeer(peer string) (string, int, error) {
	parts := strings.Split(peer, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, fmt.Errorf("invalid peer %q: <node>.<ifIndex> required", peer)
	}

	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return "", 0, fmt.Errorf("invalid interface index in peer %q: %v", peer, err)
	}

	return parts[0], idx, nil
}

type NetemNode struct {
	Instance        INetemNode
	LaunchAtStartup bool
	Config          NodeConfig
}

type nodeNetnsCache struct {
	mu      sync.Mutex
	handles map[string]netns.NsHandle
	group   singleflight.Group
}

func newNodeNetnsCache() *nodeNetnsCache {
	return &nodeNetnsCache{handles: make(map[string]netns.NsHandle)}
}

func (c *nodeNetnsCache) get(node INetemNode) (netns.NsHandle, error) {
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

type NetemTopologyManager struct {
	prjID string
	path  string

	IdGenerator *NodeIdentifierGenerator
	nodes       []NetemNode
	ovsInstance *ovs.OvsProjectInstance
	needsOvs    bool
	links       []*NetemLink
	bridges     []*NetemBridge
	mgntNet     *MgntNetwork
	running     bool
	logger      *logrus.Entry
	mu          sync.RWMutex
	opMu        sync.RWMutex
	// qosBatchApplier is injectable for tests. Production uses link.ApplyQoSBatch.
	qosBatchApplier func(netns.NsHandle, []link.QoSOperation) ([]link.QoSOperationResult, error)
	syncMu          sync.Mutex
	lastSynced      []byte
}

func (t *NetemTopologyManager) Check() error {
	filepath := path.Join(t.path, networkFilename)
	_, errors := CheckTopology(filepath)
	if len(errors) > 0 {
		msg := ""
		for _, err := range errors {
			msg += "\n\t" + err.Error()
		}
		return fmt.Errorf("topology is not valid:%s", msg)
	}

	return nil
}

func writeFileAtomic(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".gonetem-network-*")
	if err != nil {
		return fmt.Errorf("unable to create temporary file: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("unable to write temporary file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("unable to set temporary file mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("unable to sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("unable to close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, filename); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("unable to replace network file: %w", err)
	}

	return nil
}

func (t *NetemTopologyManager) SynchroniseTopology() error {
	topo := &NetemTopology{
		Nodes:   make(map[string]NodeConfig),
		Links:   make([]LinkConfig, 0),
		Bridges: make(map[string]BridgeConfig),
	}

	for _, node := range t.nodes {
		topo.Nodes[node.Instance.GetName()] = node.Config
	}

	for _, link := range t.links {
		topo.Links = append(topo.Links, link.Config)
	}

	for _, bridge := range t.bridges {
		topo.Bridges[bridge.ConfigName] = bridge.Config
	}

	if t.mgntNet != nil {
		topo.Mgntnet = MgntNetworkConfig{
			Enable:  true,
			Address: t.mgntNet.IPAddress,
		}
	}

	data, err := yaml.Marshal(topo)
	if err != nil {
		return fmt.Errorf("unable to marshal yaml topo: %v", err)
	}

	t.syncMu.Lock()
	defer t.syncMu.Unlock()

	if bytes.Equal(data, t.lastSynced) {
		return nil
	}

	if err := writeFileAtomic(t.GetNetFilePath(), data, 0644); err != nil {
		return fmt.Errorf("unable to write network file: %v", err)
	}
	t.lastSynced = append(t.lastSynced[:0], data...)

	return nil
}

func (t *NetemTopologyManager) Load() error {
	loadStart := time.Now()
	filepath := path.Join(t.path, networkFilename)
	checkStart := time.Now()
	topology, errors := CheckTopology(filepath)
	if len(errors) > 0 {
		msg := ""
		for _, err := range errors {
			msg += "\n\t" + err.Error()
		}
		return fmt.Errorf("topology if not valid:%s", msg)
	}
	t.logger.Debugf("Load: CheckTopology took %s", time.Since(checkStart))

	t.needsOvs = false
	for _, node := range topology.Nodes {
		if node.Type == "ovs" {
			t.needsOvs = true
			break
		}
	}

	// The OVS container is created lazily by Run (or when an OVS node is added).
	t.ovsInstance = nil

	// Create nodes
	nodeCreateStart := time.Now()
	t.mu.Lock()
	t.nodes = make([]NetemNode, 0)
	t.mu.Unlock()
	g := new(errgroup.Group)
	g.SetLimit(nodeCreateLimit())

	for name, nConfig := range topology.Nodes {
		name := name
		nConfig := nConfig

		g.Go(func() error {
			t.logger.Debugf("Create node %s", name)
			nodeStart := time.Now()

			shortName, err := t.IdGenerator.GetId(name)
			if err != nil {
				return err
			}
			node, err := CreateNode(t.prjID, name, shortName, nConfig)

			if err != nil {
				if node != nil {
					node.Close()
				}

				return fmt.Errorf("unable to create node %s: %w", name, err)
			}

			t.mu.Lock()
			t.nodes = append(t.nodes, NetemNode{
				Instance:        node,
				LaunchAtStartup: nConfig.Launch,
				Config:          nConfig,
			})
			t.mu.Unlock()
			t.logger.Debugf("Load: node %s created in %s", name, time.Since(nodeStart))

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	t.logger.Debugf("Load: created %d nodes in %s", len(t.nodes), time.Since(nodeCreateStart))

	// Create links
	linkBuildStart := time.Now()
	newLinks := make([]*NetemLink, len(topology.Links))
	for idx, lConfig := range topology.Links {
		peer1Name, peer1Idx, err := splitPeer(lConfig.Peer1)
		if err != nil {
			return fmt.Errorf("invalid link %q-%q: %w", lConfig.Peer1, lConfig.Peer2, err)
		}
		peer2Name, peer2Idx, err := splitPeer(lConfig.Peer2)
		if err != nil {
			return fmt.Errorf("invalid link %q-%q: %w", lConfig.Peer1, lConfig.Peer2, err)
		}

		if lConfig.Buffer == 0.0 && lConfig.Rate > 0 {
			// by default set limit buffer to 1.0 * BDP
			lConfig.Buffer = 1.0
		}

		newLinks[idx] = &NetemLink{
			Peer1: NetemLinkPeer{
				Node:    t.GetNode(peer1Name),
				IfIndex: peer1Idx,
			},
			Peer2: NetemLinkPeer{
				Node:    t.GetNode(peer2Name),
				IfIndex: peer2Idx,
			},
			HasPeer1Netem: false,
			HasPeer2Netem: false,
			HasPeer1Tbf:   false,
			HasPeer2Tbf:   false,
			Config:        lConfig,
		}
	}
	t.mu.Lock()
	t.links = newLinks
	t.mu.Unlock()
	t.logger.Debugf("Load: built %d links in %s", len(newLinks), time.Since(linkBuildStart))

	// Create bridges
	bridgeBuildStart := time.Now()
	bIdx := 0
	newBridges := make([]*NetemBridge, len(topology.Bridges))
	for bName, bConfig := range topology.Bridges {
		shortName, err := t.IdGenerator.GetId(bName)
		if err != nil {
			return err
		}

		newBridges[bIdx] = &NetemBridge{
			Name:          options.NETEM_ID + t.prjID + "." + shortName,
			ConfigName:    bName,
			HostInterface: bConfig.Host,
			Peers:         make([]NetemLinkPeer, len(bConfig.Interfaces)),
			Config:        bConfig,
		}

		for pIdx, ifName := range bConfig.Interfaces {
			peerName, peerIdx, err := splitPeer(ifName)
			if err != nil {
				return fmt.Errorf("invalid bridge interface %q: %w", ifName, err)
			}

			node := t.GetNode(peerName)
			if node == nil {
				return fmt.Errorf("bridge interface %q references unknown node %s", ifName, peerName)
			}

			newBridges[bIdx].Peers[pIdx] = NetemLinkPeer{
				Node:    node,
				IfIndex: peerIdx,
			}
		}

		bIdx++
	}
	t.mu.Lock()
	t.bridges = newBridges
	t.mu.Unlock()
	t.logger.Debugf("Load: built %d bridges in %s", len(newBridges), time.Since(bridgeBuildStart))

	// create mgnt network if necessary
	var newMgntNet *MgntNetwork
	if topology.Mgntnet.Enable {
		newMgntNet = &MgntNetwork{
			NetId:     fmt.Sprintf("%s.mgnt", t.prjID),
			IPAddress: topology.Mgntnet.Address,
			NetNs:     link.GetRootNetns(),
			Logger:    t.logger,
		}
	}
	t.mu.Lock()
	t.mgntNet = newMgntNet
	t.mu.Unlock()

	t.syncMu.Lock()
	t.lastSynced = nil
	t.syncMu.Unlock()
	t.logger.Debugf("Load: total took %s", time.Since(loadStart))

	return nil
}

func (t *NetemTopologyManager) ensureOvsInstance(start bool) error {
	if t.ovsInstance == nil {
		instance, err := ovs.NewOvsInstance(t.prjID)
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

func (t *NetemTopologyManager) Reload(progressCh chan TopologyRunCloseProgressT) ([]*proto.TopologyRunMsg_NodeMessages, error) {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	t.logger.Debug("Topo/Reload")

	var err error
	var nodeMessages []*proto.TopologyRunMsg_NodeMessages

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

func (t *NetemTopologyManager) Run(progressCh chan TopologyRunCloseProgressT) ([]*proto.TopologyRunMsg_NodeMessages, error) {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	return t.runLocked(progressCh)
}

func (t *NetemTopologyManager) runLocked(progressCh chan TopologyRunCloseProgressT) (nodeMessages []*proto.TopologyRunMsg_NodeMessages, err error) {
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
				nodeMessages = append(nodeMessages, &proto.TopologyRunMsg_NodeMessages{
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
	if err := l.ApplyPeer1QoS(peer1IfName, peer1Netns, false); err != nil {
		return err
	}
	if err := l.ApplyPeer2QoS(peer2IfName, peer2Netns, false); err != nil {
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

func (t *NetemTopologyManager) GetLink(peer1V string, peer2V string) (*NetemLink, int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getLinkLocked(peer1V, peer2V)
}

func (t *NetemTopologyManager) getLinkLocked(peer1V string, peer2V string) (*NetemLink, int, error) {
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

func (t *NetemTopologyManager) LinkAdd(linkCfg LinkConfig, sync bool) error {
	if err := linkCfg.Validate(); err != nil {
		return err
	}

	t.opMu.Lock()
	defer t.opMu.Unlock()

	var link *NetemLink
	var running bool

	err := func() error {
		t.mu.Lock()
		defer t.mu.Unlock()

		if _, _, err := t.getLinkLocked(linkCfg.Peer1, linkCfg.Peer2); err == nil {
			return fmt.Errorf("this link already exist")
		}

		peer1Name, peer1Idx, err := splitPeer(linkCfg.Peer1)
		if err != nil {
			return err
		}
		peer2Name, peer2Idx, err := splitPeer(linkCfg.Peer2)
		if err != nil {
			return err
		}

		peer1Node := t.getNodeLocked(peer1Name)
		if peer1Node == nil {
			return fmt.Errorf("node %s not found in the topology", peer1Name)
		}
		peer2Node := t.getNodeLocked(peer2Name)
		if peer2Node == nil {
			return fmt.Errorf("node %s not found in the topology", peer2Name)
		}

		if linkCfg.Buffer == 0.0 && linkCfg.Rate > 0 {
			// by default set limit buffer to 1.0 * BDP
			linkCfg.Buffer = 1.0
		}

		link = &NetemLink{
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
		running = t.running
		return nil
	}()
	if err != nil {
		return err
	}

	if running {
		netnsCache := newNodeNetnsCache()
		defer netnsCache.closeAll()
		if err := t.setupLink(link, true, netnsCache); err != nil {
			return err
		}
	}

	t.mu.Lock()
	t.links = append(t.links, link)
	t.mu.Unlock()

	if sync {
		return t.SynchroniseTopology()
	}

	return nil
}

func (t *NetemTopologyManager) LinkDel(linkCfg LinkConfig, sync bool) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	var l *NetemLink
	var idx int
	var running bool

	err := func() error {
		t.mu.Lock()
		defer t.mu.Unlock()

		var err error
		l, idx, err = t.getLinkLocked(linkCfg.Peer1, linkCfg.Peer2)
		if err != nil {
			return err
		}
		running = t.running
		return nil
	}()
	if err != nil {
		return err
	}

	peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
	peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)

	if running {
		netnsCache := newNodeNetnsCache()
		defer netnsCache.closeAll()
		if err := t.deleteLink(l, netnsCache); err != nil {
			return err
		}
	}

	if err := l.Peer1.Node.DetachInterface(peer1IfName); err != nil {
		return err
	}
	if err := l.Peer2.Node.DetachInterface(peer2IfName); err != nil {
		return err
	}

	t.mu.Lock()
	t.links = append(t.links[:idx], t.links[idx+1:]...)
	t.mu.Unlock()

	if sync {
		return t.SynchroniseTopology()
	}
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
		linkCfg.Rate != 0 || linkCfg.Buffer != 0.0 || linkCfg.Burst != 0

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
	}
}

func (t *NetemTopologyManager) LinkUpdate(linkCfg LinkConfig, sync bool) error {
	if err := linkCfg.Validate(); err != nil {
		return err
	}

	// Non-sync link updates only mutate per-link state and independent kernel
	// interfaces, so they can run concurrently. Structural operations
	// (LinkAdd/Del, NodeAdd/Del, Run/Reload) still take the write side.
	// Sync updates rewrite the topology file, so keep those exclusive.
	if sync {
		t.opMu.Lock()
		defer t.opMu.Unlock()
	} else {
		t.opMu.RLock()
		defer t.opMu.RUnlock()
	}

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
		if sync {
			return t.SynchroniseTopology()
		}
		return nil
	}

	if oldPeer1 != newPeer1 {
		peer1Netns, err := l.Peer1.Node.GetNetns()
		if err != nil {
			return err
		}
		defer peer1Netns.Close()

		peer1IfName := l.Peer1.Node.GetInterfaceName(l.Peer1.IfIndex)
		if err := applyPeerQoSDiff(
			peer1IfName, peer1Netns, oldPeer1, newPeer1,
			&l.HasPeer1Tbf, &l.HasPeer1Netem,
		); err != nil {
			return err
		}
	}

	if oldPeer2 != newPeer2 {
		peer2Netns, err := l.Peer2.Node.GetNetns()
		if err != nil {
			return err
		}
		defer peer2Netns.Close()

		peer2IfName := l.Peer2.Node.GetInterfaceName(l.Peer2.IfIndex)
		if err := applyPeerQoSDiff(
			peer2IfName, peer2Netns, oldPeer2, newPeer2,
			&l.HasPeer2Tbf, &l.HasPeer2Netem,
		); err != nil {
			return err
		}
	}

	if sync {
		return t.SynchroniseTopology()
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

func (t *NetemTopologyManager) NodeAdd(name string, config NodeConfig, sync bool) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()

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

	node, err := CreateNode(t.prjID, name, shortName, config)
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
					node.Stop()
				}
				node.Close()
				t.nodes = t.nodes[:len(t.nodes)-1]
				return fmt.Errorf("unable to setup mgnt link for node %s: %w", name, err)
			}
		}
	}

	if sync {
		return t.SynchroniseTopology()
	}
	return nil
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

	if running {
		type deleteBatch struct {
			node  INetemNode
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

	netnsCache.closeAll()

	// Commit link/bridge removals before closing the node. This preserves the
	// previous partial-failure behavior if the container cannot be removed.
	t.mu.Lock()
	keptLinks := t.links[:0]
	for _, l := range t.links {
		if l.Peer1.Node.GetName() != name && l.Peer2.Node.GetName() != name {
			keptLinks = append(keptLinks, l)
		}
	}
	t.links = keptLinks

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

func (t *NetemTopologyManager) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.running
}

func (t *NetemTopologyManager) GetNetFilePath() string {
	return path.Join(t.path, networkFilename)
}

func (t *NetemTopologyManager) ReadNetworkFile() ([]byte, error) {
	return os.ReadFile(t.GetNetFilePath())
}

func (t *NetemTopologyManager) WriteNetworkFile(data []byte) error {
	if err := writeFileAtomic(t.GetNetFilePath(), data, 0644); err != nil {
		return err
	}

	t.syncMu.Lock()
	t.lastSynced = append(t.lastSynced[:0], data...)
	t.syncMu.Unlock()

	return nil
}

func (t *NetemTopologyManager) GetAllNodes() []INetemNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	nodeInstances := make([]INetemNode, len(t.nodes))
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

func (t *NetemTopologyManager) GetNode(name string) INetemNode {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getNodeLocked(name)
}

func (t *NetemTopologyManager) getNodeLocked(name string) INetemNode {
	for _, node := range t.nodes {
		if node.Instance.GetName() == name {
			return node.Instance
		}
	}
	return nil
}

func (t *NetemTopologyManager) startNode(node INetemNode) ([]string, error) {
	if err := node.Start(); err != nil {
		return []string{}, fmt.Errorf("unable to start node %s: %w", node.GetName(), err)
	}

	if err := node.ConfigureInterfaces(); err != nil {
		return []string{}, fmt.Errorf("unable to configure interfaces of node %s: %w", node.GetName(), err)
	}

	configPath := path.Join(t.path, configDir)
	timeout := options.ServerConfig.Docker.Timeoutop
	messages, err := node.LoadConfig(configPath, timeout)
	if err != nil {
		return messages, fmt.Errorf("unable to load config of node %s: %w", node.GetName(), err)
	}

	return messages, nil
}

func (t *NetemTopologyManager) stopNode(node INetemNode) error {
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
	timeout := options.ServerConfig.Docker.Timeoutop

	return node.ReadConfigFiles(confPath, timeout)
}

func (t *NetemTopologyManager) Save(progressCh chan TopologySaveProgressT) error {
	// create config folder if not exist
	destPath := path.Join(t.path, configDir)
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		if err := os.Mkdir(destPath, 0755); err != nil {
			return fmt.Errorf("unable to create configs dir %s: %w", destPath, err)
		}
	}

	t.mu.RLock()
	nodes := append([]NetemNode(nil), t.nodes...)
	t.mu.RUnlock()

	if progressCh != nil {
		progressCh <- TopologySaveProgressT{Code: NODE_SAVE_COUNT, Value: len(nodes)}
	}

	timeout := options.ServerConfig.Docker.Timeoutop
	g := new(errgroup.Group)
	g.SetLimit(nodeConfigLimit())

	for _, node := range nodes {
		node := node

		g.Go(func() error {
			var err error = nil

			if node.Instance.IsRunning() {
				err = node.Instance.Save(destPath, timeout)
			}

			if progressCh != nil {
				progressCh <- TopologySaveProgressT{Code: NODE_SAVE}
			}

			if err != nil {
				return fmt.Errorf("node %s: save cmd error - %v", node.Instance.GetName(), err)
			}
			return nil
		})
	}
	err := g.Wait()

	return err
}

func (t *NetemTopologyManager) Close(progressCh chan TopologyRunCloseProgressT) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	return t.closeLocked(progressCh)
}

// nodeDestroysNetnsOnClose reports whether closing a node also removes the
// network namespace that owns its link interfaces.
func nodeDestroysNetnsOnClose(node INetemNode) bool {
	switch node.GetType() {
	case "docker", "namespace":
		return true
	default:
		return false
	}
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

func LoadTopology(prjID, prjPath string) (*NetemTopologyManager, error) {
	topo := &NetemTopologyManager{
		prjID:  prjID,
		path:   prjPath,
		nodes:  make([]NetemNode, 0),
		logger: logrus.WithField("project", prjID),
		IdGenerator: &NodeIdentifierGenerator{
			lock: &sync.Mutex{},
		},
	}
	if err := topo.Load(); err != nil {
		return topo, fmt.Errorf("unable to load the topology:\n\t%w", err)
	}
	return topo, nil
}
