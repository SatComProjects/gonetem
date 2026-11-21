package server

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/satcomprojects/gonetem/internal/options"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

func writeFileAtomic(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, ".gonetem-network-*")
	if err != nil {
		return fmt.Errorf("unable to create temporary file: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("unable to write temporary file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("unable to set temporary file mode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("unable to sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("unable to close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, filename); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("unable to replace network file: %w", err)
	}

	return nil
}

func (t *NetemTopologyManager) SynchroniseTopology() error {
	topo := &NetemTopology{
		Nodes:   make(map[string]NodeConfig),
		Links:   make([]LinkConfig, 0),
		Bridges: make(map[string]BridgeConfig),
	}

	// Snapshot under the topology read lock so a concurrent add/delete/update
	// cannot change the slices while they are being serialized. Nested slices
	// are copied as well: writers mutate them in place.
	t.mu.RLock()
	for idx := range t.nodes {
		cfg := t.nodes[idx].Config
		cfg.Volumes = append([]string(nil), cfg.Volumes...)
		cfg.Vrfs = append([]string(nil), cfg.Vrfs...)
		cfg.Vrrps = append([]VrrpOptions(nil), cfg.Vrrps...)
		topo.Nodes[t.nodes[idx].Instance.GetName()] = cfg
	}

	for _, l := range t.links {
		topo.Links = append(topo.Links, l.Config)
	}

	for _, bridge := range t.bridges {
		cfg := bridge.Config
		cfg.Interfaces = append([]string(nil), cfg.Interfaces...)
		topo.Bridges[bridge.ConfigName] = cfg
	}

	if t.mgntNet != nil {
		topo.Mgntnet = MgntNetworkConfig{
			Enable:  true,
			Address: t.mgntNet.IPAddress,
		}
	}
	t.mu.RUnlock()

	data, err := yaml.Marshal(topo)
	if err != nil {
		return fmt.Errorf("unable to marshal yaml topo: %v", err)
	}

	t.syncMu.Lock()
	defer t.syncMu.Unlock()

	if bytes.Equal(data, t.lastSynced) {
		return nil
	}

	if err := writeFileAtomic(t.GetNetFilePath(), data, 0644); err != nil {
		return fmt.Errorf("unable to write network file: %v", err)
	}
	t.lastSynced = append(t.lastSynced[:0], data...)

	return nil
}

func (t *NetemTopologyManager) GetNetFilePath() string {
	return path.Join(t.path, networkFilename)
}

func (t *NetemTopologyManager) ReadNetworkFile() ([]byte, error) {
	return os.ReadFile(t.GetNetFilePath())
}

func (t *NetemTopologyManager) WriteNetworkFile(data []byte) error {
	if err := writeFileAtomic(t.GetNetFilePath(), data, 0644); err != nil {
		return err
	}

	t.syncMu.Lock()
	t.lastSynced = append(t.lastSynced[:0], data...)
	t.syncMu.Unlock()

	return nil
}

func (t *NetemTopologyManager) Save(progressCh chan TopologySaveProgressT) error {
	// create config folder if not exist
	destPath := path.Join(t.path, configDir)
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		if err := os.Mkdir(destPath, 0755); err != nil {
			return fmt.Errorf("unable to create configs dir %s: %w", destPath, err)
		}
	}

	t.mu.RLock()
	nodes := append([]NetemNode(nil), t.nodes...)
	t.mu.RUnlock()

	if progressCh != nil {
		progressCh <- TopologySaveProgressT{Code: NODE_SAVE_COUNT, Value: len(nodes)}
	}

	timeout := options.ServerConfig.Docker.Timeoutop
	g := new(errgroup.Group)
	g.SetLimit(nodeConfigLimit())

	for _, node := range nodes {
		node := node

		g.Go(func() error {
			var err error = nil

			if node.Instance.IsRunning() {
				err = node.Instance.Save(destPath, timeout)
			}

			if progressCh != nil {
				progressCh <- TopologySaveProgressT{Code: NODE_SAVE}
			}

			if err != nil {
				return fmt.Errorf("node %s: save cmd error - %v", node.Instance.GetName(), err)
			}
			return nil
		})
	}
	err := g.Wait()

	return err
}
