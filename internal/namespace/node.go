package namespace

import (
	"fmt"
	"sync"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/sirupsen/logrus"
)

// NamespaceNode is a node that only provides a Linux network namespace. It
// shares the host filesystem and process namespace, so it is intended for
// lightweight L2/L3 topologies that do not need a container.
type NamespaceNode struct {
	PrjID      string
	Name       string
	ShortName  string
	Interfaces map[string]link.IfState
	Running    bool

	nsName      string
	initialized bool
	mu          sync.RWMutex
	nsMu        sync.Mutex
	executor    commandExecutor
	Logger      *logrus.Entry
}

func NewNamespaceNode(prjID, name, shortName string) *NamespaceNode {
	return &NamespaceNode{
		PrjID:      prjID,
		Name:       name,
		ShortName:  shortName,
		Interfaces: make(map[string]link.IfState),
		nsName:     fmt.Sprintf("%s%s.%s", options.NETEM_ID, prjID, name),
		executor:   execCommandExecutor{},
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    name,
		}),
	}
}

func (n *NamespaceNode) GetName() string {
	return n.Name
}

func (n *NamespaceNode) GetShortName() string {
	if n.ShortName == "" {
		return n.Name
	}
	return n.ShortName
}

func (n *NamespaceNode) GetType() string {
	return "namespace"
}

func (n *NamespaceNode) GetFullType() string {
	return "namespace"
}

func (n *NamespaceNode) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.Running
}
