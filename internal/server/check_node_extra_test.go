package server

import (
	"testing"
)

func TestCheckNodeConfigRejectsInvalidCombinations(t *testing.T) {
	cases := []struct {
		name  string
		node  string
		cfg   NodeConfig
		nodes []string
	}{
		{"duplicate", "n1", NodeConfig{Type: "namespace"}, []string{"n1"}},
		{"invalid name", "bad-name", NodeConfig{Type: "namespace"}, nil},
		{"invalid type", "n1", NodeConfig{Type: "unknown"}, nil},
		{"ovs long name", "switchlonger", NodeConfig{Type: "ovs"}, nil},
		{"ovs mpls", "sw1", NodeConfig{Type: "ovs", Mpls: true}, nil},
		{"namespace image", "n1", NodeConfig{Type: "namespace", Image: "img"}, nil},
		{"namespace volumes", "n1", NodeConfig{Type: "namespace", Volumes: []string{"/tmp:/tmp"}}, nil},
		{"namespace mpls", "n1", NodeConfig{Type: "namespace", Mpls: true}, nil},
		{"namespace vrfs", "n1", NodeConfig{Type: "namespace", Vrfs: []string{"vrf"}}, nil},
		{"mgmt on ovs", "sw1", NodeConfig{Type: "ovs", Mgnt: MgntOptions{Enable: true, Address: "172.16.0.1/24"}}, nil},
		{"bad mgmt address", "n1", NodeConfig{Type: "namespace", Mgnt: MgntOptions{Enable: true, Address: "bad"}}, nil},
		{"vrrp wrong type", "n1", NodeConfig{Type: "namespace", Vrrps: []VrrpOptions{{Interface: 0, Group: 1, Address: "10.0.0.1/24"}}}, nil},
		{"vrrp negative interface", "r1", NodeConfig{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: -1, Group: 1, Address: "10.0.0.1/24"}}}, nil},
		{"vrrp bad group", "r1", NodeConfig{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: 0, Group: 0, Address: "10.0.0.1/24"}}}, nil},
		{"vrrp duplicate", "r1", NodeConfig{Type: "docker.router", Vrrps: []VrrpOptions{
			{Interface: 0, Group: 1, Address: "10.0.0.1/24"},
			{Interface: 0, Group: 1, Address: "10.0.0.2/24"},
		}}, nil},
		{"vrrp bad address", "r1", NodeConfig{Type: "docker.router", Vrrps: []VrrpOptions{{Interface: 0, Group: 1, Address: "bad"}}}, nil},
		{"bad volume syntax", "n1", NodeConfig{Type: "docker.host", Volumes: []string{"noseparator"}}, nil},
		{"missing volume path", "n1", NodeConfig{Type: "docker.host", Volumes: []string{"/does/not/exist:/tmp"}}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkNodeConfig(tc.node, tc.cfg, tc.nodes); err == nil {
				t.Fatalf("checkNodeConfig accepted %#v", tc.cfg)
			}
		})
	}
}

func TestCheckBridgeConfigRejectsDuplicatesAndMissingHost(t *testing.T) {
	if err := checkBridgeConfig("br1", BridgeConfig{Host: "lo"}, []string{"br1"}); err == nil {
		t.Fatal("checkBridgeConfig accepted a duplicate bridge")
	}
	if err := checkBridgeConfig("br1", BridgeConfig{Host: "definitely-not-an-interface"}, nil); err == nil {
		t.Fatal("checkBridgeConfig accepted a missing host interface")
	}
}
