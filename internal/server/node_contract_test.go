package server

import (
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
	"github.com/satcomprojects/gonetem/internal/ovs"
	"github.com/satcomprojects/gonetem/internal/testutil"
)

// TestNodeContract checks the lifecycle/identity contract shared by the Docker,
// namespace and OVS adapters. It is a privileged test because it creates real
// namespaces/containers.
func TestNodeContract(t *testing.T) {
	options.InitServerConfig()

	cases := []struct {
		name   string
		config NodeConfig
		guard  func(t *testing.T)
	}{
		{
			name:   "namespace",
			config: NodeConfig{Type: "namespace", Launch: true},
			guard:  func(t *testing.T) { testutil.RequireRoot(t) },
		},
		{
			name:   "docker",
			config: NodeConfig{Type: "docker.host", Launch: false},
			guard: func(t *testing.T) {
				testutil.RequireRoot(t)
				requireDockerImage(t, options.ServerConfig.Docker.Nodes.Host.Image)
			},
		},
		{
			name:   "ovs",
			config: NodeConfig{Type: "ovs", Launch: true},
			guard: func(t *testing.T) {
				testutil.RequireRoot(t)
				requireDockerImage(t, options.ServerConfig.Docker.OvsImage)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.guard(t)

			factory := testNodeFactory()
			if tc.config.Type == "ovs" {
				instance, err := factory.CreateOvsInstance("CTR")
				if err != nil {
					t.Fatalf("CreateOvsInstance returned an error: %v", err)
				}
				if err := instance.Start(); err != nil {
					t.Fatalf("OVS instance Start returned an error: %v", err)
				}
				t.Cleanup(func() { _ = ovs.CloseOvsInstance("CTR") })
			}

			node, err := factory.Create("CTR", "n1", "n1", tc.config)
			if err != nil {
				t.Fatalf("Create returned an error: %v", err)
			}
			t.Cleanup(func() { _ = node.Close() })
			if node.GetName() != "n1" || node.GetShortName() != "n1" {
				t.Fatalf("unexpected identity: %q/%q", node.GetName(), node.GetShortName())
			}
			if node.GetType() != tc.name {
				t.Fatalf("GetType = %q, want %q", node.GetType(), tc.name)
			}
			if node.GetFullType() == "" {
				t.Fatal("GetFullType returned an empty string")
			}
			if node.IsRunning() {
				t.Fatal("new node reports running")
			}
			if state := node.GetInterfacesState(); state == nil {
				t.Fatal("GetInterfacesState returned nil")
			}

			if err := node.Start(); err != nil {
				t.Fatalf("Start returned an error: %v", err)
			}
			if !node.IsRunning() {
				t.Fatal("node is not running after Start")
			}
			if err := node.ConfigureInterfaces(); err != nil {
				t.Fatalf("ConfigureInterfaces returned an error: %v", err)
			}
			if err := node.Stop(); err != nil {
				t.Fatalf("Stop returned an error: %v", err)
			}
			if node.IsRunning() {
				t.Fatal("node is still running after Stop")
			}
			if err := node.Close(); err != nil {
				t.Fatalf("Close returned an error: %v", err)
			}
			if err := node.Close(); err != nil {
				t.Fatalf("second Close returned an error: %v", err)
			}
		})
	}
}
