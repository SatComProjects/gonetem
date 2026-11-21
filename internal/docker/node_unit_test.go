package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/term"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/utils"
	"github.com/vishvananda/netns"
)

// copyCall records a file transfer requested from the fake client.
type copyCall struct {
	source string
	dest   string
}

// fakeContainerClient is an in-memory ContainerNodeAPI used by unit tests. It
// records lifecycle calls so tests can assert the node's decisions without a
// Docker daemon or root privileges.
type fakeContainerClient struct {
	mu sync.Mutex

	pid          int
	state        string
	createID     string
	imagePresent bool

	createCalls   int
	startCalls    int
	stopCalls     int
	rmCalls       int
	pidCalls      int
	getStateCalls int

	createErr   error
	startErr    error
	stopErr     error
	rmErr       error
	pidErr      error
	getStateErr error

	copyFromCalls []copyCall
	copyToCalls   []copyCall
	copyFromErr   error
	copyToErr     error
}

func (f *fakeContainerClient) Create(context.Context, string, string, string, []string, bool, bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return "", f.createErr
	}
	return f.createID, nil
}

func (f *fakeContainerClient) Start(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	return f.startErr
}

func (f *fakeContainerClient) Stop(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	return f.stopErr
}

func (f *fakeContainerClient) Rm(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rmCalls++
	return f.rmErr
}

func (f *fakeContainerClient) Pid(context.Context, string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pidCalls++
	return f.pid, f.pidErr
}

func (f *fakeContainerClient) GetState(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getStateCalls++
	return f.state, f.getStateErr
}

func (f *fakeContainerClient) Get(context.Context, string) (*container.Summary, error) {
	return &container.Summary{}, nil
}

func (f *fakeContainerClient) List(context.Context, string) ([]NetemContainerList, error) {
	return nil, nil
}

func (f *fakeContainerClient) Exec(context.Context, string, []string) (string, error) {
	return "", nil
}

func (f *fakeContainerClient) ExecWithWorkingDir(context.Context, string, []string, string) (string, error) {
	return "", nil
}

func (f *fakeContainerClient) ExecOutStream(context.Context, string, []string, io.Writer) error {
	return nil
}

func (f *fakeContainerClient) ExecTty(context.Context, string, []string, io.ReadCloser, io.Writer, bool, uint, uint, chan term.Winsize) error {
	return nil
}

func (f *fakeContainerClient) IsFileExist(context.Context, string, string) bool {
	return true
}

func (f *fakeContainerClient) IsFolderExist(context.Context, string, string) bool {
	return false
}

func (f *fakeContainerClient) CopyFrom(_ context.Context, _ string, source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyFromCalls = append(f.copyFromCalls, copyCall{source: source, dest: dest})
	return f.copyFromErr
}

func (f *fakeContainerClient) CopyTo(_ context.Context, _ string, source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyToCalls = append(f.copyToCalls, copyCall{source: source, dest: dest})
	return f.copyToErr
}

func (f *fakeContainerClient) IsImagePresent(context.Context, string) (bool, error) {
	return f.imagePresent, nil
}

func (f *fakeContainerClient) ImagePull(context.Context, string) error {
	return nil
}

func (f *fakeContainerClient) counts() (create, start, stop, rm, pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls, f.startCalls, f.stopCalls, f.rmCalls, f.pidCalls
}

// newUnitDockerNode builds a lazily created node backed by fake.
func newUnitDockerNode(t *testing.T, fake ContainerNodeAPI) *DockerNode {
	t.Helper()
	options.InitServerConfig()
	node, err := NewDockerNode(fake, testRuntimeConfig(), "prj", "host", DockerNodeOptions{
		Name:       "node",
		LazyCreate: true,
	})
	if err != nil {
		t.Fatalf("NewDockerNode returned an error: %v", err)
	}
	return node
}

func TestDockerNode_StopInvalidatesPidCache(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid(), createID: "cid", imagePresent: true}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"
	node.Running = true

	ns, err := node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}
	ns.Close()

	_, _, _, _, pidCalls := fake.counts()
	if pidCalls != 1 {
		t.Fatalf("Pid called %d times after first resolution, want 1", pidCalls)
	}

	if err := node.Stop(); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}

	node.Running = true
	ns, err = node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns after Stop returned an error: %v", err)
	}
	ns.Close()

	_, _, _, _, pidCalls = fake.counts()
	if pidCalls != 2 {
		t.Fatalf("Pid called %d times after Stop, want a fresh resolution (2)", pidCalls)
	}
}

func TestDockerNode_KillInvalidatesPidCache(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid()}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"
	node.Running = true

	ns, err := node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}
	ns.Close()

	if err := node.Kill(); err != nil {
		t.Fatalf("Kill returned an error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is still running after Kill")
	}

	node.Running = true
	ns, err = node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns after Kill returned an error: %v", err)
	}
	ns.Close()

	_, _, _, _, pidCalls := fake.counts()
	if pidCalls != 2 {
		t.Fatalf("Pid called %d times after Kill, want a fresh resolution (2)", pidCalls)
	}
}

func TestDockerNode_CloseInvalidatesPidCache(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid()}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"
	node.Running = true

	ns, err := node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}
	ns.Close()

	if err := node.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	node.Running = true
	ns, err = node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns after Close returned an error: %v", err)
	}
	ns.Close()

	_, _, _, _, pidCalls := fake.counts()
	if pidCalls != 2 {
		t.Fatalf("Pid called %d times after Close, want a fresh resolution (2)", pidCalls)
	}
}

func TestDockerNode_StartInvalidatesStalePid(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid(), imagePresent: true}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"

	// Simulate a PID cached by a previous run of the container.
	if _, err := node.resolvePid(func() (int, error) { return 111, nil }); err != nil {
		t.Fatalf("seeding the PID cache failed: %v", err)
	}

	if err := node.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}

	pid, err := node.resolvePid(func() (int, error) { return 999, nil })
	if err != nil {
		t.Fatalf("resolvePid after Start returned an error: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("PID after Start = %d, want the freshly resolved %d", pid, os.Getpid())
	}
}

func TestDockerNode_ConcurrentCachedNetnsAccess(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid()}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"
	node.Running = true

	ns, err := node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}
	ns.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle, err := node.GetRunningNetns()
			if err != nil {
				t.Errorf("GetRunningNetns returned an error: %v", err)
				return
			}
			handle.Close()
		}()
	}
	wg.Wait()

	_, _, _, _, pidCalls := fake.counts()
	if pidCalls != 1 {
		t.Fatalf("Pid called %d times with a warm cache, want 1", pidCalls)
	}
}

func TestDockerNode_LazyCreationUsesClientOnStart(t *testing.T) {
	fake := &fakeContainerClient{pid: os.Getpid(), createID: "cid", imagePresent: true}
	node := newUnitDockerNode(t, fake)

	if node.ID != "" {
		t.Fatalf("lazy node has container ID %q before Start", node.ID)
	}
	create, start, _, _, _ := fake.counts()
	if create != 0 || start != 0 {
		t.Fatalf("lazy node used the client before Start: create=%d start=%d", create, start)
	}

	if err := node.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	if node.ID != "cid" {
		t.Fatalf("container ID after Start = %q, want cid", node.ID)
	}
	if !node.IsRunning() {
		t.Fatal("node is not running after Start")
	}

	create, start, _, _, _ = fake.counts()
	if create != 1 || start != 1 {
		t.Fatalf("unexpected client calls after Start: create=%d start=%d", create, start)
	}

	if err := node.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if _, _, _, rm, _ := fake.counts(); rm != 1 {
		t.Fatalf("Rm called %d times on Close, want 1", rm)
	}
}

func TestDockerNode_EagerCreationUsesClientInConstructor(t *testing.T) {
	options.InitServerConfig()
	fake := &fakeContainerClient{createID: "cid", imagePresent: true}

	node, err := NewDockerNode(fake, testRuntimeConfig(), "prj", "host", DockerNodeOptions{Name: "node"})
	if err != nil {
		t.Fatalf("NewDockerNode returned an error: %v", err)
	}
	if node.ID != "cid" {
		t.Fatalf("container ID = %q, want cid", node.ID)
	}
	if create, _, _, _, _ := fake.counts(); create != 1 {
		t.Fatalf("Create called %d times, want 1", create)
	}
}

func TestDockerNode_StartReportsMissingImage(t *testing.T) {
	fake := &fakeContainerClient{imagePresent: false}
	node := newUnitDockerNode(t, fake)

	err := node.Start()
	if err == nil {
		t.Fatal("Start succeeded without a local image")
	}
	if !strings.Contains(err.Error(), "not present") {
		t.Fatalf("unexpected Start error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is running after a failed Start")
	}
	if _, start, _, _, _ := fake.counts(); start != 0 {
		t.Fatalf("client.Start called %d times after create failure, want 0", start)
	}
}

func TestDockerNode_StopKeepsRunningOnClientError(t *testing.T) {
	fake := &fakeContainerClient{
		pid:     os.Getpid(),
		stopErr: errors.New("stop failed"),
	}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"
	node.Running = true

	if _, err := node.GetRunningNetns(); err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}

	if err := node.Stop(); err == nil {
		t.Fatal("Stop ignored the client error")
	}
	if !node.IsRunning() {
		t.Fatal("node was marked stopped after a failed Stop")
	}

	// The cached PID must stay valid because the container was not stopped.
	if _, err := node.GetRunningNetns(); err != nil {
		t.Fatalf("GetRunningNetns after failed Stop returned an error: %v", err)
	}
	_, _, _, _, pidCalls := fake.counts()
	if pidCalls != 1 {
		t.Fatalf("Pid called %d times after failed Stop, want the cache to remain valid (1)", pidCalls)
	}
}

func TestDockerNode_StoppedNodeErrors(t *testing.T) {
	node := &DockerNode{Name: "node"}

	if err := node.Capture(context.Background(), 0, io.Discard); err == nil {
		t.Fatal("Capture on a stopped node did not return an error")
	}
	if err := node.ExecCommand(context.Background(), []string{"true"}, nil, io.Discard, false, 0, 0, nil); err == nil {
		t.Fatal("ExecCommand on a stopped node did not return an error")
	}
	if _, _, _, err := node.RunCommand([]string{"true"}); err == nil {
		t.Fatal("RunCommand on a stopped node did not return an error")
	}
}

func TestDockerNode_GetStatusWithoutContainer(t *testing.T) {
	node := &DockerNode{}
	status, err := node.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus returned an error: %v", err)
	}
	if status.Running {
		t.Fatal("node without a container reports running")
	}
}

func TestDockerNode_GetStatusUsesClient(t *testing.T) {
	fake := &fakeContainerClient{state: "running"}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"

	status, err := node.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus returned an error: %v", err)
	}
	if !status.Running {
		t.Fatal("GetStatus did not report a running container")
	}

	fake.getStateErr = errors.New("inspect failed")
	if _, err := node.GetStatus(); err == nil {
		t.Fatal("GetStatus ignored the client error")
	}
}

func TestDockerNode_CopyUsesClientAndPropagatesErrors(t *testing.T) {
	fake := &fakeContainerClient{}
	node := newUnitDockerNode(t, fake)
	node.ID = "cid"

	if err := node.CopyFrom("/src", "/dst"); err != nil {
		t.Fatalf("CopyFrom returned an error: %v", err)
	}
	if len(fake.copyFromCalls) != 1 || fake.copyFromCalls[0] != (copyCall{source: "/src", dest: "/dst"}) {
		t.Fatalf("unexpected CopyFrom calls: %+v", fake.copyFromCalls)
	}

	if err := node.CopyTo("/src", "/dst"); err != nil {
		t.Fatalf("CopyTo returned an error: %v", err)
	}
	if len(fake.copyToCalls) != 1 || fake.copyToCalls[0] != (copyCall{source: "/src", dest: "/dst"}) {
		t.Fatalf("unexpected CopyTo calls: %+v", fake.copyToCalls)
	}

	fake.copyFromErr = errors.New("copy from failed")
	if err := node.CopyFrom("/src", "/dst"); err == nil {
		t.Fatal("CopyFrom ignored the client error")
	}
	fake.copyToErr = errors.New("copy to failed")
	if err := node.CopyTo("/src", "/dst"); err == nil {
		t.Fatal("CopyTo ignored the client error")
	}
}

func TestDockerNode_CopyWithoutClient(t *testing.T) {
	node := &DockerNode{Name: "node"}

	if err := node.CopyFrom("/src", "/dst"); err == nil {
		t.Fatal("CopyFrom without a client did not return an error")
	}
	if err := node.CopyTo("/src", "/dst"); err == nil {
		t.Fatal("CopyTo without a client did not return an error")
	}
}

func TestDockerNode_SetInterfaceStateNoopAndUnknown(t *testing.T) {
	node := newUnitDockerNode(t, &fakeContainerClient{})
	if err := node.AttachInterface("eth0", 0, false); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}

	// The default state is up, so no namespace access is required.
	if err := node.SetInterfaceState(0, link.IFSTATE_UP); err != nil {
		t.Fatalf("SetInterfaceState on an unchanged interface returned an error: %v", err)
	}
	if err := node.SetInterfaceState(5, link.IFSTATE_DOWN); err == nil {
		t.Fatal("SetInterfaceState accepted an unknown interface")
	}
}

func TestDockerNode_ConfigureInterfacesWhenStoppedIsNoop(t *testing.T) {
	fake := &fakeContainerClient{}
	node := newUnitDockerNode(t, fake)
	if err := node.AttachInterface("eth0", 0, true); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}

	if err := node.ConfigureInterfaces(); err != nil {
		t.Fatalf("ConfigureInterfaces on a stopped node returned an error: %v", err)
	}
	if create, start, stop, rm, pid := fake.counts(); create+start+stop+rm+pid != 0 {
		t.Fatalf("stopped node used the client: create=%d start=%d stop=%d rm=%d pid=%d",
			create, start, stop, rm, pid)
	}
}

func TestDockerNode_AttachMgntInterfaceKeepsBookkeepingOnError(t *testing.T) {
	node := newUnitDockerNode(t, &fakeContainerClient{})
	ns, err := netns.Get()
	if err != nil {
		t.Fatalf("unable to open the current network namespace: %v", err)
	}
	defer ns.Close()

	ifName := "missing-" + utils.RandString(8)
	if err := node.AttachMgntInterface(ifName, ns, "10.0.0.1/24"); err == nil {
		t.Fatal("AttachMgntInterface accepted a missing interface")
	}
	// The Docker adapter keeps the bookkeeping entry on failure, unlike the
	// namespace adapter. Preserve and document that behaviour.
	if _, found := node.GetInterfacesState()[ifName]; !found {
		t.Fatal("management interface bookkeeping was removed on failure")
	}
}

func TestDockerNode_GetInterfaceName(t *testing.T) {
	node := &DockerNode{Name: "node"}
	if got := node.GetInterfaceName(3); got != "eth3" {
		t.Fatalf("GetInterfaceName(3) = %q, want eth3", got)
	}
}

func TestDockerNode_GetInterfacesStateZeroValue(t *testing.T) {
	node := &DockerNode{}
	if states := node.GetInterfacesState(); len(states) != 0 {
		t.Fatalf("zero-value node states = %v, want none", states)
	}
	if err := node.DetachInterface("eth0"); err != nil {
		t.Fatalf("DetachInterface on a zero-value node returned an error: %v", err)
	}
}

func TestDockerNode_GetConsoleCmd(t *testing.T) {
	node := &DockerNode{Config: &options.DockerNodeConfig{}}
	node.Config.Commands.Console = "vtysh -c 'show version'"
	node.Config.Commands.Shell = "/bin/bash -l"

	cmd, err := node.GetConsoleCmd(false)
	if err != nil {
		t.Fatalf("GetConsoleCmd returned an error: %v", err)
	}
	if len(cmd) != 3 || cmd[0] != "vtysh" || cmd[2] != "show version" {
		t.Fatalf("unexpected console command: %v", cmd)
	}

	cmd, err = node.GetConsoleCmd(true)
	if err != nil {
		t.Fatalf("GetConsoleCmd(shell) returned an error: %v", err)
	}
	if len(cmd) != 2 || cmd[0] != "/bin/bash" || cmd[1] != "-l" {
		t.Fatalf("unexpected shell command: %v", cmd)
	}

	node.Config.Commands.Console = "echo 'unterminated"
	if _, err := node.GetConsoleCmd(false); err == nil {
		t.Fatal("GetConsoleCmd accepted an invalid command")
	}
}
