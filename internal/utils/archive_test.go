package utils

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type archiveEntry struct {
	name     string
	typeflag byte
	body     string
}

func buildArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			Mode:     0644,
			Size:     int64(len(entry.body)),
		}
		if entry.typeflag == tar.TypeDir {
			hdr.Mode = 0755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestCreateArchivePropagatesWriterErrors(t *testing.T) {
	err := CreateOneFileArchive(failingWriter{err: os.ErrClosed}, "network.yml", []byte("data"))
	if err == nil {
		t.Fatal("CreateOneFileArchive did not propagate the writer error")
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	sub := filepath.Join(src, "configs")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "R1.conf"), []byte("config"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := CreateArchive(src, &buf); err != nil {
		t.Fatalf("CreateArchive returned an error: %v", err)
	}

	dst := t.TempDir()
	if err := OpenArchive(dst, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("OpenArchive returned an error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "configs", "R1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "config" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestOpenArchiveExtractsNestedFiles(t *testing.T) {
	dir := t.TempDir()
	data := buildArchive(t,
		archiveEntry{name: "sub/", typeflag: tar.TypeDir},
		archiveEntry{name: "sub/file.txt", typeflag: tar.TypeReg, body: "hello"},
	)

	if err := OpenArchive(dir, bytes.NewReader(data)); err != nil {
		t.Fatalf("OpenArchive returned an error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sub", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected content: %q", got)
	}
}

func TestOpenArchiveRejectsTraversal(t *testing.T) {
	for _, name := range []string{
		"../escaped.txt",
		"sub/../../escaped.txt",
		"a/b/../../../escaped.txt",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dst := filepath.Join(root, "inner")
			if err := os.Mkdir(dst, 0755); err != nil {
				t.Fatal(err)
			}

			data := buildArchive(t,
				archiveEntry{name: name, typeflag: tar.TypeReg, body: "pwned"},
			)
			if err := OpenArchive(dst, bytes.NewReader(data)); err == nil {
				t.Fatalf("expected traversal entry %q to be rejected", name)
			}

			escaped := filepath.Join(root, "escaped.txt")
			if _, err := os.Stat(escaped); err == nil {
				t.Fatalf("archive wrote outside destination: %s", escaped)
			}
		})
	}
}

func TestOpenArchiveRejectsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	data := buildArchive(t,
		archiveEntry{name: "/tmp/absolute.txt", typeflag: tar.TypeReg, body: "x"},
	)

	if err := OpenArchive(dir, bytes.NewReader(data)); err == nil {
		t.Fatal("expected absolute archive path to be rejected")
	}
}

func TestOpenArchiveRejectsUnsupportedTypes(t *testing.T) {
	dir := t.TempDir()
	data := buildArchive(t,
		archiveEntry{name: "link", typeflag: tar.TypeSymlink},
	)

	if err := OpenArchive(dir, bytes.NewReader(data)); err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
}
