package docker

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/utils"
)

const (
	NODE_TIMEOUT_OP = 10
)

func skipUnlessRoot(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}
}

func TestDockerNode_DetachInterface(t *testing.T) {
	node := &DockerNode{Interfaces: make(map[string]*DockerInterface)}
	node.Interfaces["eth0"] = &DockerInterface{State: link.IFSTATE_UP}

	if err := node.DetachInterface("eth0"); err != nil {
		t.Fatalf("DetachInterface returned an error: %v", err)
	}
	if _, ok := node.Interfaces["eth0"]; ok {
		t.Fatal("interface was not removed from bookkeeping")
	}
}

func TestDockerNode_InterfaceMapConcurrentAccess(t *testing.T) {
	node := &DockerNode{Interfaces: make(map[string]*DockerInterface)}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = node.AttachInterface("eth0", 0, false)
		}()
		go func() {
			defer wg.Done()
			_ = node.GetInterfacesState()
			_ = node.IsRunning()
		}()
	}
	wg.Wait()
}

func TestDockerNode_ResolvePidCachesResult(t *testing.T) {
	node := &DockerNode{}
	calls := 0
	resolver := func() (int, error) {
		calls++
		return 4242, nil
	}

	pid, err := node.resolvePid(resolver)
	if err != nil {
		t.Fatalf("first resolvePid returned an error: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("unexpected pid: %d", pid)
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times, want 1", calls)
	}

	pid, err = node.resolvePid(resolver)
	if err != nil {
		t.Fatalf("second resolvePid returned an error: %v", err)
	}
	if pid != 4242 {
		t.Fatalf("unexpected cached pid: %d", pid)
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times after cache hit, want 1", calls)
	}
}

func TestDockerNode_InvalidatePid(t *testing.T) {
	node := &DockerNode{pid: 4242}

	node.invalidatePid()

	if node.pid != 0 {
		t.Fatalf("pid was not invalidated: %d", node.pid)
	}
}

func TestDockerNode_GetRunningNetnsUsesCachedPid(t *testing.T) {
	// A cached PID must be used without falling back to Docker inspection.
	// Using the test process PID proves the cached path resolves a namespace
	// while the node has no container ID.
	node := &DockerNode{pid: os.Getpid()}

	ns, err := node.GetRunningNetns()
	if err != nil {
		t.Fatalf("GetRunningNetns returned an error: %v", err)
	}
	defer ns.Close()
}

func BenchmarkDockerNode_RunCommandPidCache(b *testing.B) {
	if os.Getuid() != 0 {
		b.Skip("Benchmark requires root privileges.")
	}

	options.InitServerConfig()
	node, err := NewDockerNode(utils.RandString(4), "host", DockerNodeOptions{
		Name: utils.RandString(6),
	})
	if err != nil {
		b.Fatalf("Unable to create docker node: %v", err)
	}
	defer node.Close()

	if err := node.Start(); err != nil {
		b.Fatalf("Unable to start docker node: %v", err)
	}

	cmd := []string{"sh", "-c", "true"}

	b.Run("cached_pid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, code, err := node.RunCommand(cmd); err != nil || code != 0 {
				b.Fatalf("RunCommand failed: code=%d err=%v", code, err)
			}
		}
	})

	b.Run("inspect_each_call", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			node.invalidatePid()
			if _, _, code, err := node.RunCommand(cmd); err != nil || code != 0 {
				b.Fatalf("RunCommand failed: code=%d err=%v", code, err)
			}
		}
	})
}

func TestInterfacePreparationScriptBatchesInterfaces(t *testing.T) {
	script, ok := interfacePreparationScript([]string{"eth0", "eth1", "mgnt"}, true, true)
	if !ok {
		t.Fatal("expected preparation work")
	}
	if got := strings.Count(script, "ethtool -K"); got != 2 {
		t.Fatalf("expected 2 ethtool commands, got %d:\n%s", got, script)
	}
	if got := strings.Count(script, "sysctl -w"); got != 2 {
		t.Fatalf("expected 2 sysctl commands, got %d:\n%s", got, script)
	}
	if strings.Contains(script, "mgnt") {
		t.Fatalf("management interface should not be prepared:\n%s", script)
	}
	if !strings.Contains(script, "exit $rc") {
		t.Fatalf("script does not aggregate exit status:\n%s", script)
	}

	if _, ok := interfacePreparationScript([]string{"mgnt"}, false, false); ok {
		t.Fatal("expected no preparation work")
	}
}

func TestVRRPInterfaceNameIncludesGroup(t *testing.T) {
	first := vrrpInterfaceName(0, 1)
	second := vrrpInterfaceName(0, 2)
	if first == second {
		t.Fatalf("VRRP interfaces for different groups collide: %s", first)
	}
	if first != "vrrp-0-1" || second != "vrrp-0-2" {
		t.Fatalf("unexpected VRRP interface names: %s %s", first, second)
	}
}

func TestDockerNode_LazyCreation(t *testing.T) {
	options.InitServerConfig()
	node, err := NewDockerNode(utils.RandString(4), "host", DockerNodeOptions{
		Name:       utils.RandString(6),
		LazyCreate: true,
	})
	if err != nil {
		t.Fatalf("Unable to create node: %v", err)
	}
	if node.ID != "" {
		t.Fatal("lazy node was created immediately")
	}

	if err := node.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	if node.ID == "" {
		t.Fatal("container was not created on Start")
	}
	if !node.IsRunning() {
		t.Fatal("node is not running after Start")
	}

	if err := node.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
}

func TestDockerNode_StartStopWithoutInterfaces(t *testing.T) {
	options.InitServerConfig()
	prjID := utils.RandString(4)
	node, err := NewDockerNode(prjID, "host", DockerNodeOptions{
		Name: utils.RandString(6),
	})
	if err != nil {
		t.Fatalf("Unable to create node: %v", err)
	}
	defer node.Close()

	if err := node.Start(); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	if !node.IsRunning() {
		t.Fatal("node is not running after Start")
	}

	if err := node.Stop(); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}
	if node.IsRunning() {
		t.Fatal("node is still running after Stop")
	}
}

func TestDockerNode_StartStop(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	tests := []struct {
		desc        string
		name        string
		nType       string
		ipv6        bool
		mpls        bool
		expectError bool
	}{
		{
			desc:        "DockerNode: random type test",
			name:        utils.RandString(3),
			nType:       utils.RandString(8),
			expectError: true,
		},
		{
			desc:        "DockerNode: host start/stop test",
			name:        utils.RandString(3),
			nType:       "host",
			ipv6:        true,
			mpls:        true,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			prjID := utils.RandString(4)
			options := DockerNodeOptions{
				Name: tt.name,
				Ipv6: tt.ipv6,
				Mpls: tt.mpls,
			}

			node, err := NewDockerNode(prjID, tt.nType, options)
			if tt.expectError {
				if err == nil {
					t.Errorf("An error is expected but it is not occurred")
				}
				return
			}
			if err != nil {
				t.Errorf("Unable to create docker node: %v", err)
				return
			}

			// Start the node
			if err := node.Start(); err != nil {
				t.Errorf("Unable to start docker node: %v", err)
			}

			// Check the status
			status, err := node.GetStatus()
			if err != nil {
				t.Errorf("Unable to get docker node status: %v", err)
			} else if !status.Running {
				t.Error("The docker node is not running even after start command")
			}

			// Clean up
			node.Close()
		})
	}
}

func TestDockerNode_Copy(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prjID := utils.RandString(4)
	config := DockerNodeOptions{
		Name: utils.RandString(3),
	}

	node, err := NewDockerNode(prjID, "router", config)
	if err != nil {
		t.Errorf("Unable to create docker node: %v", err)
		return
	}
	defer node.Close()

	// Test copy from
	localFile := "/tmp/frr-test.conf"
	if err := node.CopyFrom("/etc/frr/frr.conf", localFile); err != nil {
		t.Errorf("Unable to copy a file from docker node: %v", err)
		return
	}
	stat, err := os.Stat(localFile)
	if err != nil || !stat.Mode().IsRegular() {
		t.Errorf("The local file has not bee created")
		return
	}
	defer os.Remove(localFile)

	// Test copy to
	if err := node.CopyTo(localFile, "/tmp/"); err != nil {
		t.Errorf("Unable to copy a file to docker node: %v", err)
		return
	}
}

func TestDockerNode_ReadConfigFiles(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prefix := utils.RandString(3)
	tests := []struct {
		desc          string
		name          string
		nType         string
		confFileNames []string
	}{
		{
			desc:          "DockerNode: read config files of host node",
			name:          fmt.Sprintf("%s-%s", prefix, "host"),
			nType:         "host",
			confFileNames: []string{"Init", "Network", "NTP"},
		},
		{
			desc:          "DockerNode: read config files of server node",
			name:          fmt.Sprintf("%s-%s", prefix, "server"),
			nType:         "server",
			confFileNames: []string{"Init", "Network", "NTP", "DHCP", "TFTP", "DHCP-RELAY", "Bind"},
		},
		{
			desc:          "DockerNode: read config files of router node",
			name:          fmt.Sprintf("%s-%s", prefix, "router"),
			nType:         "router",
			confFileNames: []string{"FRR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			prjID := utils.RandString(4)
			config := DockerNodeOptions{
				Name: tt.name,
			}

			node, err := NewDockerNode(prjID, tt.nType, config)
			if err != nil {
				t.Fatalf("Unable to create docker node: %v", err)
			}
			defer node.Close()

			// Start the node and load config
			if err := node.Start(); err != nil {
				t.Fatalf("Unable to start node %s: %v", tt.name, err)
			}

			// create temp dir to save configuration files
			dir, err := os.MkdirTemp("/tmp", "ntmtst")
			if err != nil {
				t.Fatalf("Unable to create temp folder: %v", err)
			}
			defer os.RemoveAll(dir)

			// read config files
			configFiles, err := node.ReadConfigFiles(dir, NODE_TIMEOUT_OP)
			if err != nil {
				t.Fatalf("Unable to read config files of %s: %v", tt.name, err)
			}

			if len(configFiles) != len(tt.confFileNames) {
				t.Errorf(
					"Wrong number of config files of %d != %d: %v",
					len(configFiles), len(tt.confFileNames), err)
			}
		})
	}
}

func TestDockerNode_Save(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prefix := utils.RandString(3)
	tests := []struct {
		desc      string
		name      string
		nType     string
		ipv6      bool
		mpls      bool
		confFiles []string
	}{
		{
			desc:      "DockerNode: save host node",
			name:      fmt.Sprintf("%s-%s", prefix, "host"),
			nType:     "host",
			ipv6:      true,
			mpls:      false,
			confFiles: []string{"%s.net.conf", "%s.ntp.conf"},
		},
		{
			desc:      "DockerNode: save server node",
			name:      fmt.Sprintf("%s-%s", prefix, "server"),
			nType:     "server",
			ipv6:      true,
			mpls:      false,
			confFiles: []string{"%s.net.conf", "%s.dhcpd.conf"},
		},
		{
			desc:      "DockerNode: save router node",
			name:      fmt.Sprintf("%s-%s", prefix, "router"),
			nType:     "router",
			ipv6:      false,
			mpls:      true,
			confFiles: []string{"%s.frr.conf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			prjID := utils.RandString(4)
			config := DockerNodeOptions{
				Name: tt.name,
				Ipv6: tt.ipv6,
				Mpls: tt.mpls,
			}

			node, err := NewDockerNode(prjID, tt.nType, config)
			if err != nil {
				t.Errorf("Unable to create docker node: %v", err)
				return
			}
			defer node.Close()

			// Start the node and load config
			if err := node.Start(); err != nil {
				t.Errorf("Unable to start node %s: %v", tt.name, err)
				return
			}
			if _, err := node.LoadConfig("/tmp/fake", NODE_TIMEOUT_OP); err != nil {
				t.Errorf("Unable to load config for node %s: %v", tt.name, err)
				return
			}

			// create temp dir to save configuration files
			dir, err := os.MkdirTemp("/tmp", "ntmtst")
			if err != nil {
				t.Errorf("Unable to create temp folder: %v", err)
				return
			}
			defer os.RemoveAll(dir)

			// save configuration
			if err := node.Save(dir, NODE_TIMEOUT_OP); err != nil {
				t.Errorf("Unable to save node %s: %v", tt.name, err)
				return
			}

			// check file
			for _, f := range tt.confFiles {
				fPath := path.Join(dir, fmt.Sprintf(f, tt.name))
				if _, err := os.Stat(fPath); os.IsNotExist(err) {
					t.Errorf("File '%s' is not found", fPath)
				}
			}
		})
	}
}

func TestDockerNode_AttachLink(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prjID := utils.RandString(4)
	// Create 2 nodes and create a link between
	config := DockerNodeOptions{
		Name: utils.RandString(3),
	}
	node1, err := NewDockerNode(prjID, "router", config)
	if err != nil {
		t.Fatalf("Unable to create docker node: %v", err)
	}
	defer node1.Close()

	config = DockerNodeOptions{
		Name: utils.RandString(3),
	}
	node2, err := NewDockerNode(prjID, "host", config)
	if err != nil {
		t.Fatalf("Unable to create docker node: %v", err)
	}
	defer node2.Close()

	for _, node := range []*DockerNode{node1, node2} {
		if err := node.Start(); err != nil {
			t.Fatalf("Unable to start docker node %s: %v", node.GetName(), err)
		}
	}

	// create link
	node1Netns, err := node1.GetNetns()
	if err != nil {
		t.Fatalf("Unable to get netns for node 1: %v", err)
	}
	defer node1Netns.Close()

	node2Netns, err := node2.GetNetns()
	if err != nil {
		t.Fatalf("Unable to get netns for node 2: %v", err)
	}
	defer node2Netns.Close()

	_, err = link.CreateVethLink(
		node1.GetInterfaceName(0), node1Netns,
		node2.GetInterfaceName(0), node2Netns,
	)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
}

func TestDockerNode_ImageOverride(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prjID := utils.RandString(4)

	overrideImage := options.ServerConfig.Docker.Nodes.Host.Image
	expectedImage := options.GetDockerImageId(overrideImage)

	// check that the override image is available
	client, err := NewDockerClient()
	if err != nil {
		t.Fatalf("Unable to create docker client: %v", err)
	}
	defer client.Close()

	present, err := client.IsImagePresent(context.Background(), expectedImage)
	if err != nil {
		t.Fatalf("Unable to check image presence: %v", err)
	}
	if !present {
		t.Skipf("Override image %s is not present, skipping test", expectedImage)
	}

	// Create a router node but override its image with the host image
	config := DockerNodeOptions{
		Name:  utils.RandString(3),
		Image: overrideImage,
	}
	node, err := NewDockerNode(prjID, "router", config)
	if err != nil {
		t.Fatalf("Unable to create docker node: %v", err)
	}
	defer node.Close()

	container, err := client.Get(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("Unable to inspect container: %v", err)
	}

	if container.Image != expectedImage {
		t.Fatalf("Container image %s does not match overridden image %s", container.Image, expectedImage)
	}
}

func TestDockerNode_Volumes(t *testing.T) {
	skipUnlessRoot(t)

	options.InitServerConfig()
	prjID := utils.RandString(4)

	// Create 1 host node
	config := DockerNodeOptions{
		Name:    utils.RandString(3),
		Volumes: []string{"/tmp:/tmp/volume"},
	}
	node, err := NewDockerNode(prjID, "host", config)
	if err != nil {
		t.Fatalf("Unable to create docker node: %v", err)
	}
	defer node.Close()

	// start node
	if err := node.Start(); err != nil {
		t.Fatalf("Unable to start docker node %s: %v", node.GetName(), err)
	}

	// create empty file in /tmp folder
	filename := fmt.Sprintf("%s.txt", utils.RandString(6))
	target := path.Join("/tmp", filename)
	if err := os.WriteFile(target, []byte{}, 0666); err != nil {
		t.Fatalf("Unable to create temp file %s: %v", target, err)
	}
	defer os.Remove(filename)

	// check this file exist in node
	nTarget := path.Join("/tmp/volume", filename)
	client, err := NewDockerClient()
	if err != nil {
		t.Fatalf("Unable to create docker client: %v", err)
	}
	defer client.Close()

	if !client.IsFileExist(context.Background(), node.ID, nTarget) {
		t.Fatalf("File %s is not present in the binding volume", nTarget)
	}
}
