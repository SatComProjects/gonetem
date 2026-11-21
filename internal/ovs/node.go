package ovs

import (
	"fmt"
	"sync"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
)

// OvsNode is the OVS switch node adapter. OvsInstance is resolved lazily
// through the package registry so a node can be created before its project
// instance starts.
type OvsNode struct {
	PrjID       string
	Name        string
	ShortName   string
	Running     bool
	OvsInstance *OvsProjectInstance
	Interfaces  map[string]link.IfState
	mu          sync.RWMutex
	instanceMu  sync.Mutex
	workdir     string
	// lifecycleMu serializes Start, Stop and Close so a running transition
	// cannot overlap another one. mu still guards the interface map.
	lifecycleMu sync.Mutex
	Logger      *logrus.Entry
}

func (o *OvsNode) ensureInstance() error {
	o.instanceMu.Lock()
	defer o.instanceMu.Unlock()

	if o.OvsInstance != nil {
		return nil
	}

	instance := GetOvsInstance(o.PrjID)
	if instance == nil {
		return fmt.Errorf("ovswitch instance for project %s is not initialized", o.PrjID)
	}
	o.OvsInstance = instance
	return nil
}

func (s *OvsNode) GetName() string {
	return s.Name
}

func (s *OvsNode) GetShortName() string {
	if s.ShortName == "" {
		return s.Name
	}
	return s.ShortName
}

func (s *OvsNode) GetType() string {
	return "ovs"
}

func (s *OvsNode) GetFullType() string {
	return "ovs"
}

func (o *OvsNode) IsRunning() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.Running
}

func (o *OvsNode) GetBridgeName() string {
	return o.Name
}

func (o *OvsNode) GetInterfaceName(ifIndex int) string {
	return fmt.Sprintf("%s.%d", o.GetBridgeName(), ifIndex)
}

func (o *OvsNode) Start() error {
	o.lifecycleMu.Lock()
	defer o.lifecycleMu.Unlock()

	if o.IsRunning() {
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	o.mu.RLock()
	interfaces := make([]string, 0, len(o.Interfaces))
	for ifName := range o.Interfaces {
		interfaces = append(interfaces, ifName)
	}
	o.mu.RUnlock()

	if err := o.OvsInstance.AddBridgeWithPorts(o.GetBridgeName(), interfaces); err != nil {
		_ = o.OvsInstance.DelBr(o.GetBridgeName())
		return err
	}

	o.mu.Lock()
	o.Running = true
	o.mu.Unlock()

	return nil
}

func (o *OvsNode) Stop() error {
	o.lifecycleMu.Lock()
	defer o.lifecycleMu.Unlock()

	if !o.IsRunning() {
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	// Deleting the bridge removes all of its ports from the OVS database.
	// Keep the interface map so Start can re-add the ports on restart.
	if err := o.OvsInstance.DelBr(o.GetBridgeName()); err != nil {
		return err
	}

	o.mu.Lock()
	o.Running = false
	o.mu.Unlock()

	return nil
}

func (o *OvsNode) CopyFrom(srcPath, destPath string) error {
	return fmt.Errorf("CopyFrom action not supported for ovswitch node")
}

func (o *OvsNode) CopyTo(srcPath, destPath string) error {
	return fmt.Errorf("CopyTo action not supported for ovswitch node")
}

func (o *OvsNode) AttachMgntInterface(ifName string, ns netns.NsHandle, ipAddress string) error {
	return fmt.Errorf("unable to attach ovs switch to management network")
}

func (o *OvsNode) Kill() error {
	return nil
}

func (o *OvsNode) Close() error {
	o.lifecycleMu.Lock()
	defer o.lifecycleMu.Unlock()

	if o.OvsInstance != nil {
		return o.OvsInstance.DelBr(o.GetBridgeName())
	}
	return nil
}

func NewOvsNode(prjID, name, shortName, workdir string) (*OvsNode, error) {
	node := &OvsNode{
		PrjID:      prjID,
		workdir:    workdir,
		Name:       name,
		ShortName:  shortName,
		Interfaces: make(map[string]link.IfState),
		Logger: logrus.WithFields(logrus.Fields{
			"project": prjID,
			"node":    "ovs-" + name,
		}),
	}

	node.OvsInstance = GetOvsInstance(prjID)
	return node, nil
}
