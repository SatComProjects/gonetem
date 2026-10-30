package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mroy31/gonetem/internal/options"
)

type testLogger struct {
	steps []string
	logs  []string
}

func (l *testLogger) Step(step, message string) {
	l.steps = append(l.steps, step+": "+message)
}

func (l *testLogger) Log(step, line string) {
	l.logs = append(l.logs, step+": "+line)
}

func TestUpdater_Disabled(t *testing.T) {
	u := &Updater{Config: options.UpdateConfig{Enabled: false}}
	if err := u.Run(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestUpdater_UnsupportedArch(t *testing.T) {
	u := &Updater{
		Config: options.UpdateConfig{Enabled: true, GoVersion: "1.24.0"},
		Arch:   "riscv64",
	}
	if err := u.Run(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
}

func TestGoArch(t *testing.T) {
	tests := map[string]string{
		"amd64": "amd64",
		"arm64": "arm64",
		"armv7": "armv6l",
	}
	for arch, expected := range tests {
		if got := goArch(arch); got != expected {
			t.Errorf("goArch(%s) = %s, expected %s", arch, got, expected)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	dir := t.TempDir()

	if _, err := safeJoin(dir, "go/bin/go"); err != nil {
		t.Errorf("valid path rejected: %v", err)
	}

	for _, name := range []string{"../escape", "go/../../escape"} {
		if _, err := safeJoin(dir, name); err == nil {
			t.Errorf("path %q must be rejected", name)
		}
	}

	// absolute paths are extracted under dir, they cannot escape it
	if target, err := safeJoin(dir, "/etc/passwd"); err != nil || target != filepath.Join(dir, "etc", "passwd") {
		t.Errorf("unexpected target for absolute path: %s (%v)", target, err)
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()

	// build an archive with a directory, a file and a symlink
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	content := []byte("#!/bin/sh\necho go\n")
	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/go", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "go/bin/gofmt", Typeflag: tar.TypeSymlink, Linkname: "go"}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	archive := filepath.Join(dir, "go.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archive, dir); err != nil {
		t.Fatalf("unable to extract archive: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "go", "bin", "go"))
	if err != nil {
		t.Fatalf("unable to read extracted file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("unexpected content: %q", data)
	}

	if target, err := os.Readlink(filepath.Join(dir, "go", "bin", "gofmt")); err != nil || target != "go" {
		t.Errorf("unexpected symlink: %s (%v)", target, err)
	}
}

func TestExtractTarGz_Escape(t *testing.T) {
	dir := t.TempDir()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()

	archive := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	if err := extractTarGz(archive, filepath.Join(dir, "dest")); err == nil {
		t.Error("archive escaping the destination must be rejected")
	}
}

func TestUpdater_GoEnv(t *testing.T) {
	// gonetem-server runs under systemd without HOME: the go environment must
	// define the caches itself.
	t.Setenv("HOME", "")
	t.Setenv("GOPATH", "")
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOCACHE", "")

	workdir := t.TempDir()
	u := &Updater{Config: options.UpdateConfig{Workdir: workdir}}

	env, err := u.goEnv("/opt/go/bin/go")
	if err != nil {
		t.Fatalf("unable to build go environment: %v", err)
	}

	values := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				values[kv[:i]] = kv[i+1:]
				break
			}
		}
	}

	gohome := filepath.Join(workdir, "go-home")
	expected := map[string]string{
		"HOME":       gohome,
		"GOPATH":     gohome,
		"GOMODCACHE": filepath.Join(gohome, "pkg", "mod"),
		"GOCACHE":    filepath.Join(gohome, "build-cache"),
	}
	for key, value := range expected {
		if values[key] != value {
			t.Errorf("%s = %q, expected %q", key, values[key], value)
		}
	}

	if !strings.HasPrefix(values["PATH"], "/opt/go/bin"+string(os.PathListSeparator)) {
		t.Errorf("downloaded go must be first in PATH: %s", values["PATH"])
	}

	for _, dir := range []string{expected["GOMODCACHE"], expected["GOCACHE"]} {
		if stat, err := os.Stat(dir); err != nil || !stat.IsDir() {
			t.Errorf("%s has not been created: %v", dir, err)
		}
	}
}

func TestUpdater_GoHomeWithoutWorkdir(t *testing.T) {
	u := &Updater{Config: options.UpdateConfig{}}
	if got := u.goHome(); got != filepath.Join(os.TempDir(), "gonetem-update-go") {
		t.Errorf("unexpected go home: %s", got)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dest := filepath.Join(dir, "dest")

	if err := os.WriteFile(src, []byte("binary"), 0644); err != nil {
		t.Fatal(err)
	}
	// existing destination must be replaced
	if err := os.WriteFile(dest, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dest); err != nil {
		t.Fatalf("unable to copy file: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary" {
		t.Errorf("unexpected content: %q", data)
	}

	stat, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm()&0111 == 0 {
		t.Errorf("installed binary is not executable: %v", stat.Mode())
	}

	// no temporary file must remain
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("unexpected files in install dir: %v", entries)
	}
}

func TestUpdater_Install(t *testing.T) {
	workdir := t.TempDir()
	installDir := t.TempDir()

	// fake build output
	binDir := filepath.Join(workdir, "src", "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gonetem-server_amd64", "gonetem-console_amd64"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	logger := &testLogger{}
	u := &Updater{Logger: logger, Arch: "amd64", InstallDir: installDir}
	if err := u.install(context.Background(), workdir); err != nil {
		t.Fatalf("unable to install: %v", err)
	}

	for _, name := range []string{"gonetem-server", "gonetem-console"} {
		data, err := os.ReadFile(filepath.Join(installDir, name))
		if err != nil {
			t.Errorf("%s has not been installed: %v", name, err)
			continue
		}
		if string(data) != name+"_amd64" {
			t.Errorf("unexpected content for %s: %q", name, data)
		}
	}
}

func TestUpdater_InstallMissingBinary(t *testing.T) {
	u := &Updater{Arch: "amd64", InstallDir: t.TempDir()}
	if err := u.install(context.Background(), t.TempDir()); err == nil {
		t.Error("install must fail when the binaries have not been built")
	}
}

func TestUpdater_RestartWithoutService(t *testing.T) {
	u := &Updater{}
	if err := u.restart(context.Background()); err == nil {
		t.Error("restart must fail without a configured service")
	}
}

func TestUpdater_Run_LogsSteps(t *testing.T) {
	// Only check that the first step is reported: the run itself needs
	// network access and root privileges to install gonetem.
	logger := &testLogger{}
	u := &Updater{
		Config: options.UpdateConfig{
			Enabled:    true,
			Repository: "https://invalid.example.invalid/gonetem",
			Branch:     "master",
			GoVersion:  "1.24.0",
			Workdir:    t.TempDir(),
		},
		Logger: logger,
		Arch:   runtime.GOARCH,
	}

	if err := u.Run(context.Background()); err == nil {
		t.Fatal("expected an error with an invalid repository")
	}

	if len(logger.steps) == 0 || logger.steps[0][:len(STEP_GIT)] != STEP_GIT {
		t.Errorf("first step must be %s, got %v", STEP_GIT, logger.steps)
	}
}
