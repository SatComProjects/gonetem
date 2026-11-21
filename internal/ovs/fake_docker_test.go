package ovs

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/term"
	"github.com/satcomprojects/gonetem/internal/docker"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/sirupsen/logrus"
)

// fakeContainerAPI is a deterministic, concurrency-safe stand-in for the
// Docker surface used by the OVS adapters. It records calls so tests can
// assert the adapter's behaviour without a Docker daemon.
type fakeContainerAPI struct {
	mu sync.Mutex

	createID     string
	createErr    error
	imagePresent bool
	imageErr     error
	startErr     error
	rmErr        error
	execErr      error
	execOutErr   error
	execTtyErr   error
	copyToErr    error
	copyFromErr  error
	pid          int
	pidErr       error
	execOutput   string
	copyFromData []byte

	created     []string
	started     []string
	removed     []string
	execCmds    [][]string
	streamCmds  [][]string
	ttyCmds     [][]string
	copyToCalls [][2]string
	copyFrom    [][2]string
	imageChecks []string
	pidCount    int
}

var _ docker.ContainerNodeAPI = (*fakeContainerAPI)(nil)

func newFakeContainerAPI() *fakeContainerAPI {
	return &fakeContainerAPI{
		createID:     "cid",
		imagePresent: true,
		pid:          1,
	}
}

func (f *fakeContainerAPI) Create(_ context.Context, _, containerName, _ string, _ []string, _, _ bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, containerName)
	return f.createID, f.createErr
}

func (f *fakeContainerAPI) Start(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, containerID)
	return f.startErr
}

func (f *fakeContainerAPI) Stop(_ context.Context, _ string) error {
	return nil
}

func (f *fakeContainerAPI) Rm(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, containerID)
	return f.rmErr
}

func (f *fakeContainerAPI) Pid(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pidCount++
	return f.pid, f.pidErr
}

func (f *fakeContainerAPI) GetState(_ context.Context, _ string) (string, error) {
	return "created", nil
}

func (f *fakeContainerAPI) Get(_ context.Context, _ string) (*container.Summary, error) {
	return nil, nil
}

func (f *fakeContainerAPI) List(_ context.Context, _ string) ([]docker.NetemContainerList, error) {
	return nil, nil
}

func (f *fakeContainerAPI) Exec(_ context.Context, _ string, cmd []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCmds = append(f.execCmds, append([]string(nil), cmd...))
	return f.execOutput, f.execErr
}

func (f *fakeContainerAPI) ExecWithWorkingDir(ctx context.Context, containerID string, cmd []string, _ string) (string, error) {
	return f.Exec(ctx, containerID, cmd)
}

func (f *fakeContainerAPI) ExecOutStream(_ context.Context, _ string, cmd []string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streamCmds = append(f.streamCmds, append([]string(nil), cmd...))
	if f.execOutErr != nil {
		return f.execOutErr
	}
	if out != nil {
		_, _ = out.Write([]byte("capture"))
	}
	return nil
}

func (f *fakeContainerAPI) ExecTty(_ context.Context, _ string, cmd []string, _ io.ReadCloser, _ io.Writer, _ bool, _, _ uint, _ chan term.Winsize) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ttyCmds = append(f.ttyCmds, append([]string(nil), cmd...))
	return f.execTtyErr
}

func (f *fakeContainerAPI) IsFileExist(_ context.Context, _, _ string) bool {
	return false
}

func (f *fakeContainerAPI) IsFolderExist(_ context.Context, _, _ string) bool {
	return false
}

func (f *fakeContainerAPI) CopyFrom(_ context.Context, _, source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyFrom = append(f.copyFrom, [2]string{source, dest})
	if f.copyFromErr != nil {
		return f.copyFromErr
	}
	if f.copyFromData != nil {
		return writeFakeFile(dest, f.copyFromData)
	}
	return nil
}

func (f *fakeContainerAPI) CopyTo(_ context.Context, _, source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyToCalls = append(f.copyToCalls, [2]string{source, dest})
	return f.copyToErr
}

func (f *fakeContainerAPI) IsImagePresent(_ context.Context, imgName string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageChecks = append(f.imageChecks, imgName)
	return f.imagePresent, f.imageErr
}

func (f *fakeContainerAPI) ImagePull(_ context.Context, _ string) error {
	return nil
}

func (f *fakeContainerAPI) execCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.execCmds...)
}

func (f *fakeContainerAPI) streamCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.streamCmds...)
}

func (f *fakeContainerAPI) ttyCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.ttyCmds...)
}

func (f *fakeContainerAPI) startCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func (f *fakeContainerAPI) removedContainers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

func (f *fakeContainerAPI) createdContainers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.created...)
}

func (f *fakeContainerAPI) imageCheckCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.imageChecks...)
}

func (f *fakeContainerAPI) copyToCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.copyToCalls)
}

func (f *fakeContainerAPI) copyFromCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.copyFrom)
}

func (f *fakeContainerAPI) pidCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pidCount
}

// newTestInstance builds an OVS instance with an injected fake client.
func newTestInstance(client docker.ContainerNodeAPI) *OvsProjectInstance {
	return &OvsProjectInstance{
		prjID:       "prj1",
		containerId: "cid",
		client:      client,
		state:       created,
		Logger:      logrus.WithField("test", "ovs-instance"),
	}
}

// newTestNode builds an OVS node without touching the package registry.
func newTestNode() *OvsNode {
	return &OvsNode{
		PrjID:      "prj1",
		Name:       "sw1",
		ShortName:  "sw1",
		Interfaces: make(map[string]link.IfState),
		Logger:     logrus.WithField("test", "ovs-node"),
	}
}

func writeFakeFile(dest string, data []byte) error {
	return os.WriteFile(dest, data, 0o644)
}
