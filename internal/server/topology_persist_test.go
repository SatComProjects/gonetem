package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicReplacesAndSetsMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "network.yml")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := writeFileAtomic(target, []byte("new-content"), 0600); err != nil {
		t.Fatalf("writeFileAtomic returned an error: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-content" {
		t.Fatalf("content = %q, want new-content", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %#o, want 0600", info.Mode().Perm())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "network.yml" {
		t.Fatalf("temporary files leaked: %#v", entries)
	}
}

func TestWriteFileAtomicErrors(t *testing.T) {
	if err := writeFileAtomic(filepath.Join(t.TempDir(), "missing", "network.yml"), []byte("x"), 0644); err == nil {
		t.Fatal("writeFileAtomic accepted a missing directory")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, []byte("x"), 0644); err == nil {
		t.Fatal("writeFileAtomic accepted a directory target")
	}
}

func TestNetworkFileReadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	manager := &NetemTopologyManager{path: dir}
	data := []byte("nodes:\n  n1:\n    type: namespace\n")

	if err := manager.WriteNetworkFile(data); err != nil {
		t.Fatalf("WriteNetworkFile returned an error: %v", err)
	}
	got, err := manager.ReadNetworkFile()
	if err != nil {
		t.Fatalf("ReadNetworkFile returned an error: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("read = %q, want %q", got, data)
	}
	if manager.GetNetFilePath() != filepath.Join(dir, networkFilename) {
		t.Fatalf("unexpected network path: %s", manager.GetNetFilePath())
	}

	missing := &NetemTopologyManager{path: filepath.Join(dir, "missing")}
	if _, err := missing.ReadNetworkFile(); err == nil {
		t.Fatal("ReadNetworkFile accepted a missing file")
	}
	if err := missing.WriteNetworkFile(data); err == nil {
		t.Fatal("WriteNetworkFile accepted a missing directory")
	}
}

func TestSynchroniseTopologyGoldenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	network := `
nodes:
  n1:
    type: namespace
  n2:
    type: namespace
links:
- peer1: n1.0
  peer2: n2.0
  delay: 5
mgntnet:
  enable: true
  address: 172.16.0.1/24
`
	if err := os.WriteFile(filepath.Join(dir, networkFilename), []byte(network), 0644); err != nil {
		t.Fatal(err)
	}

	first, err := loadTestTopology("RT1", dir)
	if err != nil {
		t.Fatalf("first LoadTopology returned an error: %v", err)
	}
	t.Cleanup(func() { _ = first.Close(nil) })
	if err := first.SynchroniseTopology(); err != nil {
		t.Fatalf("first SynchroniseTopology returned an error: %v", err)
	}
	firstData, err := os.ReadFile(filepath.Join(dir, networkFilename))
	if err != nil {
		t.Fatal(err)
	}

	second, err := loadTestTopology("RT2", dir)
	if err != nil {
		t.Fatalf("second LoadTopology returned an error: %v", err)
	}
	t.Cleanup(func() { _ = second.Close(nil) })
	if err := second.SynchroniseTopology(); err != nil {
		t.Fatalf("second SynchroniseTopology returned an error: %v", err)
	}
	secondData, err := os.ReadFile(filepath.Join(dir, networkFilename))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(firstData, secondData) {
		t.Fatalf("round-trip changed network.yml:\nfirst:\n%s\nsecond:\n%s", firstData, secondData)
	}
	if _, errors := CheckTopology(filepath.Join(dir, networkFilename)); len(errors) > 0 {
		t.Fatalf("round-tripped topology is invalid: %v", errors)
	}
}

type fakeSaveNode struct {
	fakeStartNode
	saveCalled bool
	saveErr    error
}

func (n *fakeSaveNode) IsRunning() bool { return true }
func (n *fakeSaveNode) Save(string, int) error {
	n.saveCalled = true
	return n.saveErr
}

func TestSynchroniseTopologySecondCallIsNoop(t *testing.T) {
	manager := &NetemTopologyManager{
		prjID:   "ST",
		path:    t.TempDir(),
		nodes:   []NetemNode{},
		links:   []*NetemLink{},
		bridges: []*NetemBridge{},
	}
	if err := manager.SynchroniseTopology(); err != nil {
		t.Fatalf("first SynchroniseTopology returned an error: %v", err)
	}
	first, err := os.ReadFile(manager.GetNetFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SynchroniseTopology(); err != nil {
		t.Fatalf("second SynchroniseTopology returned an error: %v", err)
	}
	second, err := os.ReadFile(manager.GetNetFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("second sync changed the file:\n%s\n%s", first, second)
	}
}

func TestSaveCreatesConfigDirAndReportsProgress(t *testing.T) {
	manager := &NetemTopologyManager{
		path:  t.TempDir(),
		nodes: []NetemNode{},
	}
	progress := make(chan TopologySaveProgressT, 10)
	if err := manager.Save(progress); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}
	close(progress)
	var codes []SaveProgressCode
	for p := range progress {
		codes = append(codes, p.Code)
	}
	if len(codes) != 1 || codes[0] != NODE_SAVE_COUNT {
		t.Fatalf("progress codes = %#v, want NODE_SAVE_COUNT", codes)
	}
	if _, err := os.Stat(filepath.Join(manager.path, configDir)); err != nil {
		t.Fatalf("config dir was not created: %v", err)
	}
}

func TestSaveRunningNodeReportsNodeProgress(t *testing.T) {
	node := &fakeSaveNode{fakeStartNode: fakeStartNode{name: "n1"}}
	manager := &NetemTopologyManager{
		path:  t.TempDir(),
		nodes: []NetemNode{{Instance: node, Config: NodeConfig{Type: "docker.host"}}},
	}
	progress := make(chan TopologySaveProgressT, 10)
	if err := manager.Save(progress); err != nil {
		t.Fatalf("Save returned an error: %v", err)
	}
	close(progress)
	count, saved := 0, 0
	for p := range progress {
		switch p.Code {
		case NODE_SAVE_COUNT:
			count++
		case NODE_SAVE:
			saved++
		}
	}
	if count != 1 || saved != 1 {
		t.Fatalf("progress = count %d saved %d, want 1/1", count, saved)
	}
	if !node.saveCalled {
		t.Fatal("running node Save was not called")
	}
}

func TestSaveNodeErrorIsReported(t *testing.T) {
	node := &fakeSaveNode{
		fakeStartNode: fakeStartNode{name: "n1"},
		saveErr:       errTestSave,
	}
	manager := &NetemTopologyManager{
		path:  t.TempDir(),
		nodes: []NetemNode{{Instance: node, Config: NodeConfig{Type: "docker.host"}}},
	}
	if err := manager.Save(nil); err == nil {
		t.Fatal("Save accepted a node save failure")
	}
}

var errTestSave = errors.New("synthetic save failure")

func TestSaveConfigDirCreationError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based test is skipped as root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	manager := &NetemTopologyManager{path: dir, nodes: []NetemNode{}}
	if err := manager.Save(nil); err == nil {
		t.Fatal("Save accepted an unwritable config directory")
	}
}
