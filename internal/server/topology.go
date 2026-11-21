package server

import (
	"fmt"
	"path"
	"sync"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/ovs"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
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

// Lock order, outermost first:
//
//	updateBatchMu -> opMu -> NetemLink.mu -> mu
//
// Keep it acyclic: never take opMu while holding a link/topology lock, never
// take updateBatchMu after opMu, and never acquire mu before NetemLink.mu.
// Never call node/kernel operations while holding mu. syncMu is a leaf lock
// used only around the network-file write; it is taken after mu is released
// and must not be held while acquiring any other topology lock.
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
	// linkIndex maps an unordered peer key to a link. It mirrors t.links and
	// is guarded by t.mu. A nil index means the manager was built directly in
	// tests; getLinkLocked falls back to the linear scan in that case.
	linkIndex map[linkKey]*NetemLink
	// pendingLinks reserves peer keys for in-flight add/delete operations so
	// two concurrent operations cannot create or remove the same link.
	pendingLinks map[linkKey]struct{}
	// updateBatchMu serializes LinkUpdateMany batches between themselves. A
	// batch holds several NetemLink.mu locks at once, so two batches touching
	// the same links in opposite order could deadlock. Adds/deletes do not take
	// this mutex and remain concurrent with updates.
	updateBatchMu sync.Mutex
	// qosApplier is injectable for tests. Production uses link.ApplyQoS.
	qosApplier QoSApplier
	// qosBatchApplier is injectable for tests. Production uses link.ApplyQoSBatch.
	qosBatchApplier func(netns.NsHandle, []link.QoSOperation) ([]link.QoSOperationResult, error)
	// nodeFactory is injectable for tests. Production uses NodeFactory.
	nodeFactory func(prjID, name, shortName string, config NodeConfig) (Node, error)
	ovsFactory  func(prjID string) (*ovs.OvsProjectInstance, error)
	syncMu      sync.Mutex
	lastSynced  []byte
}

// QoSApplier applies qdisc changes for one link direction. It is declared by
// the server so tests can replace the kernel-facing implementation.
type QoSApplier func(ifName string, ns netns.NsHandle, q link.QoS, reset, changeTbf, changeNetem bool) (hasTbf, hasNetem bool, err error)

// applyQoS uses the injected applier, falling back to the link package.
func (t *NetemTopologyManager) applyQoS(ifName string, ns netns.NsHandle, q link.QoS, reset, changeTbf, changeNetem bool) (bool, bool, error) {
	applier := t.qosApplier
	if applier == nil {
		applier = link.ApplyQoS
	}
	return applier(ifName, ns, q, reset, changeTbf, changeNetem)
}

// createNode builds a node through the injected factory when one is set.
// Tests use this seam to exercise topology loading without Docker or root.
func (t *NetemTopologyManager) createNode(name, shortName string, config NodeConfig) (Node, error) {
	if t.nodeFactory != nil {
		return t.nodeFactory(t.prjID, name, shortName, config)
	}
	factory, err := NewNodeFactory()
	if err != nil {
		return nil, err
	}
	return factory.Create(t.prjID, name, shortName, config)
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

func LoadTopology(prjID, prjPath string) (*NetemTopologyManager, error) {
	factory, err := NewNodeFactory()
	if err != nil {
		return nil, err
	}

	topo := &NetemTopologyManager{
		prjID:       prjID,
		path:        prjPath,
		nodes:       make([]NetemNode, 0),
		logger:      logrus.WithField("project", prjID),
		nodeFactory: factory.Create,
		ovsFactory:  factory.CreateOvsInstance,
		IdGenerator: &NodeIdentifierGenerator{
			lock: &sync.Mutex{},
		},
	}
	if err := topo.Load(); err != nil {
		return topo, fmt.Errorf("unable to load the topology:\n\t%w", err)
	}
	return topo, nil
}
