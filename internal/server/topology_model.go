package server

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/creasty/defaults"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

type QoSConfig struct {
	Loss       float64 `yaml:",omitempty"` // percent
	Delay      int     `yaml:",omitempty"` // ms
	Jitter     int     `yaml:",omitempty"` // ms
	Rate       int     `yaml:",omitempty"` // kbps
	Buffer     float64 `yaml:",omitempty"` // BDP scale factor
	Burst      int     `yaml:",omitempty"` // bytes
	Limit      int     `yaml:",omitempty"` // netem queue limit in packets
	TxQueuelen int     `yaml:",omitempty"` // interface tx queue length in packets
}

type LinkConfig struct {
	Peer1      string
	Peer2      string
	Loss       float64 `yaml:",omitempty"` // percent
	Delay      int     `yaml:",omitempty"` // ms
	Jitter     int     `yaml:",omitempty"` // ms
	Rate       int     `yaml:",omitempty"` // kbps
	Buffer     float64 `yaml:",omitempty"` // BDP scale factor
	Burst      int     `yaml:",omitempty"` // bytes
	Limit      int     `yaml:",omitempty"` // netem queue limit in packets
	TxQueuelen int     `yaml:",omitempty"` // interface tx queue length in packets
	Peer1QoS   QoSConfig
	Peer2QoS   QoSConfig
}

const (
	maxDelayMS = 274_000 // keeps tick conversion inside uint32
	// 17_000_000 * 125 = 2.125e9, which also fits a 32-bit signed int.
	// If higher rates are needed, compute rate*125 in uint64 in tc.go.
	maxRateKbps = 17_000_000
	// maxQueuePackets bounds netem limit and txqueuelen. It is far above any
	// realistic emulated queue and keeps the values safe for tc and netlink.
	maxQueuePackets = 1_000_000
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
	if q.Limit < 0 || q.Limit > maxQueuePackets {
		return fmt.Errorf("%s limit must be in [0, %d] packets", prefix, maxQueuePackets)
	}
	if q.TxQueuelen < 0 || q.TxQueuelen > maxQueuePackets {
		return fmt.Errorf("%s txqueuelen must be in [0, %d] packets", prefix, maxQueuePackets)
	}
	return nil
}

func (l LinkConfig) Validate() error {
	top := QoSConfig{
		Loss:       l.Loss,
		Delay:      l.Delay,
		Jitter:     l.Jitter,
		Rate:       l.Rate,
		Buffer:     l.Buffer,
		Burst:      l.Burst,
		Limit:      l.Limit,
		TxQueuelen: l.TxQueuelen,
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
		l.Peer1QoS.Burst > 0 ||
		l.Peer1QoS.Limit > 0 ||
		l.Peer1QoS.TxQueuelen > 0 {
		return l.Peer1QoS.WithDefaults()
	}

	return QoSConfig{
		Loss:       l.Loss,
		Delay:      l.Delay,
		Jitter:     l.Jitter,
		Rate:       l.Rate,
		Buffer:     l.Buffer,
		Burst:      l.Burst,
		Limit:      l.Limit,
		TxQueuelen: l.TxQueuelen,
	}.WithDefaults()
}

func (l *LinkConfig) GetPeer2QoS() QoSConfig {
	if l.Peer2QoS.Loss > 0 ||
		l.Peer2QoS.Delay > 0 ||
		l.Peer2QoS.Jitter > 0 ||
		l.Peer2QoS.Rate > 0 ||
		l.Peer2QoS.Buffer > 0 ||
		l.Peer2QoS.Burst > 0 ||
		l.Peer2QoS.Limit > 0 ||
		l.Peer2QoS.TxQueuelen > 0 {
		return l.Peer2QoS.WithDefaults()
	}

	return QoSConfig{
		Loss:       l.Loss,
		Delay:      l.Delay,
		Jitter:     l.Jitter,
		Rate:       l.Rate,
		Buffer:     l.Buffer,
		Burst:      l.Burst,
		Limit:      l.Limit,
		TxQueuelen: l.TxQueuelen,
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
	if err := defaults.Set(n); err != nil {
		return err
	}

	type plain NodeConfig
	if err := unmarshal((*plain)(n)); err != nil {
		return err
	}

	return nil
}

type NetemTopology struct {
	Nodes   map[string]NodeConfig   `yaml:",omitempty"`
	Links   []LinkConfig            `yaml:",omitempty"`
	Bridges map[string]BridgeConfig `yaml:",omitempty"`
	Mgntnet MgntNetworkConfig       `yaml:",omitempty"`
}

type NetemLinkPeer struct {
	Node    LinkNode
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

func applyPeerQoSDiff(applier QoSApplier, ifName string, ns netns.NsHandle, old, next QoSConfig, hasTbf, hasNetem *bool) error {
	reset, changeTbf, changeNetem, changed := qosDiff(old, next, *hasTbf, *hasNetem)
	if !changed {
		return nil
	}

	if applier == nil {
		applier = link.ApplyQoS
	}
	updatedTbf, updatedNetem, err := applier(
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
		Delay:      q.Delay,
		Jitter:     q.Jitter,
		Loss:       q.Loss,
		Rate:       q.Rate,
		Buffer:     q.Buffer,
		Burst:      q.Burst,
		Limit:      q.Limit,
		TxQueuelen: q.TxQueuelen,
	}
}

func (l *NetemLink) ApplyPeer1QoS(applier QoSApplier, ifName string, ns netns.NsHandle, reset bool) error {
	q := l.Config.GetPeer1QoS()
	hasTbf, hasNetem, err := applier(
		ifName, ns, toLinkQoS(q), reset, l.HasPeer1Tbf, l.HasPeer1Netem,
	)
	if err != nil {
		return err
	}
	l.HasPeer1Tbf = hasTbf
	l.HasPeer1Netem = hasNetem
	return nil
}

func (l *NetemLink) ApplyPeer2QoS(applier QoSApplier, ifName string, ns netns.NsHandle, reset bool) error {
	q := l.Config.GetPeer2QoS()
	hasTbf, hasNetem, err := applier(
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
	Instance        Node
	LaunchAtStartup bool
	Config          NodeConfig
}
