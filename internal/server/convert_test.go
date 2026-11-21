package server

import (
	"testing"

	"github.com/satcomprojects/gonetem/internal/proto"
)

func TestGetNodeConfigFromRequest(t *testing.T) {
	config := getNodeConfigFromRequest(&proto.NodeAddRequest{
		Config: &proto.NodeConfigMsg{
			Type:    "docker.router",
			Ipv6:    true,
			Mpls:    true,
			Vrfs:    []string{"red"},
			Volumes: []string{"/tmp:/tmp"},
			Image:   "example/router:1",
			Launch:  true,
			Mgnt:    &proto.MgntOptions{Enable: true, Address: "172.16.0.1/24"},
			Vrrps: []*proto.VrrpOptions{{
				Interface: 2,
				Group:     10,
				Address:   "192.0.2.1/24",
			}},
		},
	})
	if config.Type != "docker.router" || !config.IPv6 || !config.Mpls || !config.Launch {
		t.Fatalf("unexpected flags/type: %#v", config)
	}
	if len(config.Vrfs) != 1 || len(config.Volumes) != 1 || config.Image != "example/router:1" {
		t.Fatalf("unexpected collections: %#v", config)
	}
	if !config.Mgnt.Enable || config.Mgnt.Address != "172.16.0.1/24" {
		t.Fatalf("unexpected management config: %#v", config.Mgnt)
	}
	if len(config.Vrrps) != 1 || config.Vrrps[0].Interface != 2 || config.Vrrps[0].Group != 10 {
		t.Fatalf("unexpected VRRP config: %#v", config.Vrrps)
	}
}

func TestGetNodeConfigFromRequestHandlesNilOptionals(t *testing.T) {
	config := getNodeConfigFromRequest(&proto.NodeAddRequest{
		Config: &proto.NodeConfigMsg{Type: "namespace"},
	})
	if config.Mgnt.Enable || config.Mgnt.Address != "" || len(config.Vrrps) != 0 {
		t.Fatalf("nil optional messages produced config: %#v", config)
	}
}

func TestGetLinkConfigFromProto(t *testing.T) {
	config := getLinkConfig(&proto.LinkConfig{
		Peer1: "R1.0",
		Peer2: "R2.1",
		Peer1Qos: &proto.LinkConfig_QoSConfig{
			Delay: 10, Jitter: 2, Loss: 0.5, Rate: 1000,
			Buffer: 1.5, Burst: 900, Limit: 2500, Txqueuelen: 1500,
		},
		Peer2Qos: &proto.LinkConfig_QoSConfig{Loss: 1},
	})
	if config.Peer1 != "R1.0" || config.Peer2 != "R2.1" {
		t.Fatalf("unexpected peers: %#v", config)
	}
	if config.Peer1QoS.Delay != 10 || config.Peer1QoS.Jitter != 2 ||
		config.Peer1QoS.Rate != 1000 || config.Peer1QoS.Buffer != 1.5 ||
		config.Peer1QoS.Burst != 900 || config.Peer1QoS.Limit != 2500 ||
		config.Peer1QoS.TxQueuelen != 1500 {
		t.Fatalf("unexpected peer1 QoS: %#v", config.Peer1QoS)
	}
	if config.Peer2QoS.Loss != 1 {
		t.Fatalf("unexpected peer2 QoS: %#v", config.Peer2QoS)
	}
}

func TestLinkSyncActionConversions(t *testing.T) {
	cases := []struct {
		action LinkSyncAction
		proto  proto.LinkSyncAction
	}{
		{LinkSyncAdd, proto.LinkSyncAction_LINK_SYNC_ADD},
		{LinkSyncDel, proto.LinkSyncAction_LINK_SYNC_DEL},
		{LinkSyncUpdate, proto.LinkSyncAction_LINK_SYNC_UPDATE},
	}
	for _, tc := range cases {
		got, err := linkSyncActionFromProto(tc.proto)
		if err != nil || got != tc.action {
			t.Errorf("linkSyncActionFromProto(%v) = %v, %v", tc.proto, got, err)
		}
		if back := linkSyncActionToProto(tc.action); back != tc.proto {
			t.Errorf("linkSyncActionToProto(%v) = %v, want %v", tc.action, back, tc.proto)
		}
	}
	if _, err := linkSyncActionFromProto(proto.LinkSyncAction(99)); err == nil {
		t.Fatal("unspecified action was accepted")
	}
}
