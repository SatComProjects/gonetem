package server

import "github.com/satcomprojects/gonetem/internal/proto"

func getLinkConfigFromRequest(request *proto.LinkRequest) LinkConfig {
	return getLinkConfig(request.GetLink())
}

func getLinkConfig(rLink *proto.LinkConfig) LinkConfig {
	p1qos := rLink.GetPeer1Qos()
	p2qos := rLink.GetPeer2Qos()
	return LinkConfig{
		Peer1: rLink.GetPeer1(),
		Peer2: rLink.GetPeer2(),
		Peer1QoS: QoSConfig{
			Loss:       float64(p1qos.GetLoss()),
			Delay:      int(p1qos.GetDelay()),
			Jitter:     int(p1qos.GetJitter()),
			Rate:       int(p1qos.GetRate()),
			Buffer:     float64(p1qos.GetBuffer()),
			Burst:      int(p1qos.GetBurst()),
			Limit:      int(p1qos.GetLimit()),
			TxQueuelen: int(p1qos.GetTxqueuelen()),
		},
		Peer2QoS: QoSConfig{
			Loss:       float64(p2qos.GetLoss()),
			Delay:      int(p2qos.GetDelay()),
			Jitter:     int(p2qos.GetJitter()),
			Rate:       int(p2qos.GetRate()),
			Buffer:     float64(p2qos.GetBuffer()),
			Burst:      int(p2qos.GetBurst()),
			Limit:      int(p2qos.GetLimit()),
			TxQueuelen: int(p2qos.GetTxqueuelen()),
		},
	}
}

func getNodeConfigFromRequest(request *proto.NodeAddRequest) NodeConfig {
	rConfig := request.GetConfig()
	config := NodeConfig{
		Type:    rConfig.GetType(),
		IPv6:    rConfig.GetIpv6(),
		Mpls:    rConfig.GetMpls(),
		Vrfs:    rConfig.GetVrfs(),
		Volumes: rConfig.GetVolumes(),
		Image:   rConfig.GetImage(),
		Launch:  rConfig.GetLaunch(),
		Mgnt: MgntOptions{
			Enable:  rConfig.GetMgnt().GetEnable(),
			Address: rConfig.GetMgnt().GetAddress(),
		},
	}

	for _, vrrp := range rConfig.GetVrrps() {
		config.Vrrps = append(config.Vrrps, VrrpOptions{
			Interface: int(vrrp.GetInterface()),
			Group:     int(vrrp.GetGroup()),
			Address:   vrrp.GetAddress(),
		})
	}

	return config
}
