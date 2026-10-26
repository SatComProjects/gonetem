package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPeerRegex(t *testing.T) {
	valid := []string{"R1.0", "router_1.12", "a.999"}
	for _, peer := range valid {
		if !peerRE.MatchString(peer) {
			t.Errorf("peerRE rejected valid peer %q", peer)
		}
	}

	invalid := []string{"R1", "R1x0", "R1_0", "R1.", ".0", "R1.-1"}
	for _, peer := range invalid {
		if peerRE.MatchString(peer) {
			t.Errorf("peerRE accepted invalid peer %q", peer)
		}
	}
}

func TestNodeTypeRegex(t *testing.T) {
	valid := []string{"docker.router", "docker.host", "docker.my_node", "ovs", "namespace"}
	for _, typ := range valid {
		if !nodeTypeRE.MatchString(typ) {
			t.Errorf("nodeTypeRE rejected valid type %q", typ)
		}
	}

	invalid := []string{"docker.router!", "docker.router-extra", "dockerxrouter", "ovsx", "namespace.foo", "namespaces", ""}
	for _, typ := range invalid {
		if nodeTypeRE.MatchString(typ) {
			t.Errorf("nodeTypeRE accepted invalid type %q", typ)
		}
	}
}

func writeTopology(t *testing.T, data string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "network.yml")
	if err := os.WriteFile(file, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestCheckNodeConfigValidatesManagement(t *testing.T) {
	valid := NodeConfig{
		Type: "docker.host",
		Mgnt: MgntOptions{Enable: true, Address: "192.168.0.1/24"},
	}
	if err := checkNodeConfig("R1", valid, nil); err != nil {
		t.Fatalf("valid management config was rejected: %v", err)
	}

	invalidAddr := valid
	invalidAddr.Mgnt.Address = "not-an-address"
	if err := checkNodeConfig("R1", invalidAddr, nil); err == nil {
		t.Fatal("invalid management address was accepted")
	}

	ovs := NodeConfig{
		Type: "ovs",
		Mgnt: MgntOptions{Enable: true, Address: "192.168.0.1/24"},
	}
	if err := checkNodeConfig("sw1", ovs, nil); err == nil {
		t.Fatal("management network was accepted on an OVS node")
	}
}

func TestCheckNodeConfigValidatesNamespaceOptions(t *testing.T) {
	valid := NodeConfig{
		Type: "namespace",
		Mgnt: MgntOptions{Enable: true, Address: "192.168.0.1/24"},
	}
	if err := checkNodeConfig("ns1", valid, nil); err != nil {
		t.Fatalf("valid namespace config was rejected: %v", err)
	}

	for name, mutate := range map[string]func(*NodeConfig){
		"image":   func(c *NodeConfig) { c.Image = "alpine" },
		"volumes": func(c *NodeConfig) { c.Volumes = []string{"/tmp:/tmp"} },
		"mpls":    func(c *NodeConfig) { c.Mpls = true },
		"vrfs":    func(c *NodeConfig) { c.Vrfs = []string{"VRF1"} },
	} {
		config := valid
		mutate(&config)
		if err := checkNodeConfig("ns1", config, nil); err == nil {
			t.Fatalf("namespace config with %s was accepted", name)
		}
	}
}

func TestQoSValidation(t *testing.T) {
	valid := LinkConfig{
		Delay:  10,
		Jitter: 2,
		Loss:   1,
		Rate:   1000,
		Buffer: 1,
		Burst:  10000,
		Peer1QoS: QoSConfig{
			Delay: 10,
			Rate:  1000,
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid link config was rejected: %v", err)
	}

	invalid := []LinkConfig{
		{Loss: -1},
		{Loss: 101},
		{Delay: -1},
		{Jitter: 1},
		{Rate: 1000},
		{Delay: 10, Rate: -1},
		{Delay: 10, Rate: maxRateKbps + 1},
		{Delay: 10, Rate: 1000, Buffer: -1},
		{Buffer: 1},
		{Delay: 10, Rate: 1000, Burst: -1},
		{Burst: 1},
		{Peer1QoS: QoSConfig{Delay: -1}},
		{Peer2QoS: QoSConfig{Rate: 1000}},
	}

	for i, cfg := range invalid {
		if err := cfg.Validate(); err == nil {
			t.Errorf("invalid config %d was accepted: %+v", i, cfg)
		}
	}
}

func TestCheckNodeConfigValidatesVRRP(t *testing.T) {
	valid := NodeConfig{
		Type: "docker.router",
		Vrrps: []VrrpOptions{
			{Interface: 0, Group: 1, Address: "192.168.1.252/24"},
			{Interface: 0, Group: 2, Address: "192.168.1.253/24"},
		},
	}
	if err := checkNodeConfig("R1", valid, nil); err != nil {
		t.Fatalf("valid VRRP config was rejected: %v", err)
	}

	tests := []NodeConfig{
		{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: -1, Group: 1, Address: "192.168.1.1/24"}}},
		{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: 0, Group: 0, Address: "192.168.1.1/24"}}},
		{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: 0, Group: 256, Address: "192.168.1.1/24"}}},
		{Type: "docker.router", Vrrps: []VrrpOptions{
			{Interface: 0, Group: 1, Address: "192.168.1.1/24"},
			{Interface: 0, Group: 1, Address: "192.168.1.2/24"},
		}},
	}
	for i, cfg := range tests {
		if err := checkNodeConfig("R1", cfg, nil); err == nil {
			t.Errorf("invalid VRRP config %d was accepted: %+v", i, cfg)
		}
	}
}

func TestCheckTopologyRejectsMalformedPeer(t *testing.T) {
	file := writeTopology(t, `
nodes:
  R1x0:
    type: docker.host
  R2:
    type: docker.host
links:
- peer1: R1x0
  peer2: R2.0
`)

	if _, errors := CheckTopology(file); len(errors) == 0 {
		t.Fatal("CheckTopology accepted a peer without a valid interface suffix")
	}
}

func TestCheckTopologyRejectsMalformedNodeType(t *testing.T) {
	for _, typ := range []string{"docker.router!", "docker.router-extra"} {
		t.Run(typ, func(t *testing.T) {
			file := writeTopology(t, `
nodes:
  R1:
    type: `+typ+`
`)
			if _, errors := CheckTopology(file); len(errors) == 0 {
				t.Fatalf("CheckTopology accepted invalid node type %q", typ)
			}
		})
	}
}
