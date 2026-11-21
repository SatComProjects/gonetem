package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamespaceNodeCopyErrors(t *testing.T) {
	dir := t.TempDir()
	node := NewNamespaceNode("abc", "ns1", "n1")

	t.Run("missing source", func(t *testing.T) {
		err := node.CopyFrom(filepath.Join(dir, "missing"), filepath.Join(dir, "dest"))
		if err == nil || !strings.Contains(err.Error(), "unable to stat") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("source is a directory", func(t *testing.T) {
		err := node.CopyTo(dir, filepath.Join(dir, "dest"))
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("destination cannot be created", func(t *testing.T) {
		src := filepath.Join(dir, "src")
		if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
			t.Fatal(err)
		}
		err := node.CopyTo(src, filepath.Join(dir, "missing", "dest"))
		if err == nil || !strings.Contains(err.Error(), "unable to create") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestNamespaceNodeCopyPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dest := filepath.Join(dir, "dest")
	if err := os.WriteFile(src, []byte("payload"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0640); err != nil {
		t.Fatal(err)
	}

	node := NewNamespaceNode("abc", "ns1", "n1")
	if err := node.CopyFrom(src, dest); err != nil {
		t.Fatalf("CopyFrom returned an error: %v", err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("copied file mode = %o, want 640", got)
	}
}
