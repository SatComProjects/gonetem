package server

import (
	"fmt"
	"path"
	"time"

	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"golang.org/x/sync/errgroup"
)

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
	g.SetLimit(t.createLimit())

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
			node, err := t.createNode(name, shortName, nConfig)

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
	t.linkIndex = nil
	t.ensureLinkIndexLocked()
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
