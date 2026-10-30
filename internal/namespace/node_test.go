package namespace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mroy31/gonetem/internal/link"
)

func TestNamespaceNodeMetadata(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")

	if node.GetName() != "ns1" {
		t.Fatalf("GetName() = %q", node.GetName())
	}
	if node.GetShortName() != "n1" {
		t.Fatalf("GetShortName() = %q", node.GetShortName())
	}
	if node.GetType() != "namespace" {
		t.Fatalf("GetType() = %q", node.GetType())
	}
	if node.GetFullType() != "namespace" {
		t.Fatalf("GetFullType() = %q", node.GetFullType())
	}
	if node.GetInterfaceName(3) != "eth3" {
		t.Fatalf("GetInterfaceName(3) = %q", node.GetInterfaceName(3))
	}
	if node.IsRunning() {
		t.Fatal("new namespace node is unexpectedly running")
	}
	if node.nsName != "ntmabc.ns1" {
		t.Fatalf("unexpected netns name %q", node.nsName)
	}
}

func TestNamespaceNodeInterfaceBookkeeping(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")

	if err := node.AttachInterface("eth0", 0, false); err != nil {
		t.Fatalf("AttachInterface returned an error: %v", err)
	}

	states := node.GetInterfacesState()
	if states["eth0"] != link.IFSTATE_UP {
		t.Fatalf("eth0 state = %v", states["eth0"])
	}

	if err := node.DetachInterface("eth0"); err != nil {
		t.Fatalf("DetachInterface returned an error: %v", err)
	}
	if _, found := node.GetInterfacesState()["eth0"]; found {
		t.Fatal("eth0 is still present after DetachInterface")
	}

	if err := node.SetInterfaceState(0, link.IFSTATE_DOWN); err == nil {
		t.Fatal("SetInterfaceState accepted an unknown interface")
	}
}

func TestNamespaceNodeCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dest := filepath.Join(dir, "dest")
	payload := []byte("namespace-copy")

	if err := os.WriteFile(src, payload, 0640); err != nil {
		t.Fatal(err)
	}

	node := NewNamespaceNode("abc", "ns1", "n1")
	if err := node.CopyTo(src, dest); err != nil {
		t.Fatalf("CopyTo returned an error: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("copied data = %q, expected %q", data, payload)
	}

	back := filepath.Join(dir, "back")
	if err := node.CopyFrom(dest, back); err != nil {
		t.Fatalf("CopyFrom returned an error: %v", err)
	}
	data, err = os.ReadFile(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Fatalf("copied-back data = %q, expected %q", data, payload)
	}
}

func TestNamespaceNodeCloseWithoutNamespace(t *testing.T) {
	node := NewNamespaceNode("abc", "ns1", "n1")
	if err := node.Close(); err != nil {
		t.Fatalf("Close on an uncreated namespace returned an error: %v", err)
	}
}
