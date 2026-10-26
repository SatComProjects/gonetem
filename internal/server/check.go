package server

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/mroy31/gonetem/internal/link"
	"gopkg.in/yaml.v3"
)

var (
	nameRE     = regexp.MustCompile(`^\w+$`)
	switchRE   = regexp.MustCompile(`^\w{1,10}$`)
	nodeTypeRE = regexp.MustCompile(`^(?:docker\.\w+|ovs|namespace)$`)
	peerRE     = regexp.MustCompile(`^\w+\.\d+$`)
	volumeRE   = regexp.MustCompile(`^[^\0]+:[^\0]+$`)
)

func checkNodeConfig(name string, nConfig NodeConfig, nodes []string) error {
	if isEntryExist(nodes, name) {
		return fmt.Errorf("node '%s' already exist", name)
	}

	if !nameRE.MatchString(name) {
		return fmt.Errorf("node: '%s' name field is not valid", name)
	}

	if !nodeTypeRE.MatchString(nConfig.Type) {
		return fmt.Errorf("node: '%s' type field is not valid", nConfig.Type)
	}

	// more check on ovs node
	if nConfig.Type == "ovs" {
		if !switchRE.MatchString(name) {
			return fmt.Errorf("switch Node: '%s' name field must have less than 10 caracters", name)
		}

		if nConfig.Mpls || len(nConfig.Vrfs) > 0 {
			return fmt.Errorf("mpls can not be enable on ovswitch")
		}
	}

	// more check on namespace node
	if nConfig.Type == "namespace" {
		if nConfig.Image != "" {
			return fmt.Errorf("image can not be set on namespace node")
		}
		if len(nConfig.Volumes) > 0 {
			return fmt.Errorf("volumes can not be set on namespace node")
		}
		if nConfig.Mpls {
			return fmt.Errorf("mpls is not supported on namespace node")
		}
		if len(nConfig.Vrfs) > 0 {
			return fmt.Errorf("vrfs are not supported on namespace node")
		}
	}

	// check management network configuration
	if nConfig.Mgnt.Enable {
		if nConfig.Type == "ovs" {
			return fmt.Errorf("management network is not supported on ovswitch nodes")
		}
		if _, _, err := net.ParseCIDR(nConfig.Mgnt.Address); err != nil {
			return fmt.Errorf("management address '%s' is not valid: %s", nConfig.Mgnt.Address, err)
		}
	}

	// check vrrp configuration
	if len(nConfig.Vrrps) > 0 {
		if nConfig.Type != "docker.router" {
			return fmt.Errorf("vrrp can only be enable on docker.router node")
		}

		seenGroups := make(map[[2]int]struct{}, len(nConfig.Vrrps))
		for _, vrrpCnf := range nConfig.Vrrps {
			if vrrpCnf.Interface < 0 {
				return fmt.Errorf("vrrp interface must be >= 0")
			}
			if vrrpCnf.Group < 1 || vrrpCnf.Group > 255 {
				return fmt.Errorf("vrrp group must be in [1, 255]")
			}

			key := [2]int{vrrpCnf.Interface, vrrpCnf.Group}
			if _, exists := seenGroups[key]; exists {
				return fmt.Errorf("vrrp group %d is configured twice on interface %d", vrrpCnf.Group, vrrpCnf.Interface)
			}
			seenGroups[key] = struct{}{}

			_, _, err := net.ParseCIDR(vrrpCnf.Address)
			if err != nil {
				return fmt.Errorf("vrrp address '%s' is not valid: %s", vrrpCnf.Address, err)
			}
		}
	}

	// check volumes configuration
	for _, vBind := range nConfig.Volumes {
		// only hostPath:containerPath syntax is allowed
		if !volumeRE.MatchString(vBind) {
			return fmt.Errorf("[%s/volumes] volume bind '%s' is not valid", name, vBind)
		}

		// check that host path exists
		split := strings.Split(vBind, ":")
		if _, err := os.Stat(split[0]); os.IsNotExist(err) {
			return fmt.Errorf("[%s/volumes] host path '%s' does not exist", name, split[0])
		}
	}

	return nil
}

func checkBridgeConfig(name string, bConfig BridgeConfig, bridges []string) error {
	if isEntryExist(bridges, name) {
		return fmt.Errorf("bridge '%s' already exist", name)
	}

	if !nameRE.MatchString(name) {
		return fmt.Errorf("bridge: '%s' name field is not valid", name)
	}

	ns := link.GetRootNetns()
	defer ns.Close()

	if !link.IsLinkExist(bConfig.Host, ns) {
		return fmt.Errorf("bridge %s: host interface %s not found", name, bConfig.Host)
	}

	return nil
}

func isEntryExist(nodes []string, node string) bool {
	for _, n := range nodes {
		if node == n {
			return true
		}
	}
	return false
}

func isPeerValid(nodes, peers []string, peer string) error {
	if !peerRE.MatchString(peer) {
		return fmt.Errorf("link: invalid format for peer '%s' (<node>.<ifIndex> required)", peer)
	}

	for _, p := range peers {
		if peer == p {
			return fmt.Errorf("link: peer '%s' is already used", peer)
		}
	}

	node := strings.Split(peer, ".")[0]
	if !isEntryExist(nodes, node) {
		return fmt.Errorf("link: node '%s' not exist", node)
	}

	return nil
}

func CheckTopology(filepath string) (*NetemTopology, []error) {
	var errors []error
	var nodes []string
	var bridges []string
	var peers []string
	var topology NetemTopology

	data, err := os.ReadFile(filepath)
	if err != nil {
		errors = append(errors, fmt.Errorf("unable to read topology file '%s':\n\t%w", filepath, err))
		return nil, errors
	}

	err = yaml.Unmarshal(data, &topology)
	if err != nil {
		errors = append(errors, fmt.Errorf("unable to parse topology file '%s':\n\t%w", filepath, err))
		return nil, errors
	}

	// check nodes
	for name, nConfig := range topology.Nodes {
		if err := checkNodeConfig(name, nConfig, nodes); err != nil {
			errors = append(errors, err)
		}
		nodes = append(nodes, name)
	}

	if topology.Mgntnet.Enable {
		if _, _, err := net.ParseCIDR(topology.Mgntnet.Address); err != nil {
			errors = append(errors, fmt.Errorf("management network address '%s' is not valid: %w", topology.Mgntnet.Address, err))
		}
	}

	// check links
	for _, link := range topology.Links {
		if err := isPeerValid(nodes, peers, link.Peer1); err != nil {
			errors = append(errors, err)
			continue
		}
		if err := isPeerValid(nodes, peers, link.Peer2); err != nil {
			errors = append(errors, err)
			continue
		}

		if link.Peer1 == link.Peer2 {
			errors = append(errors, fmt.Errorf("a link can not have the same peer"))
		}

		peers = append(peers, link.Peer1, link.Peer2)

		if err := link.Validate(); err != nil {
			errors = append(errors, err)
		}
	}

	// check bridges
	for bName, bConfig := range topology.Bridges {
		if err := checkBridgeConfig(bName, bConfig, bridges); err != nil {
			errors = append(errors, err)
		}

		for _, peer := range bConfig.Interfaces {
			if err := isPeerValid(nodes, peers, peer); err != nil {
				errors = append(errors, err)
				continue
			}
			peers = append(peers, peer)
		}

		bridges = append(bridges, bName)
	}

	return &topology, errors
}
