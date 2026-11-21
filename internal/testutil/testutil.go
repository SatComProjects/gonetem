// Package testutil provides capability checks and fixtures shared by GoNetEm
// tests.
//
// Unit tests run with -short and must not require root, Docker, kernel modules
// or network access. Integration tests call RequireRoot, RequireDocker or
// RequireImage: when the capability is unavailable the test is skipped with an
// explicit reason. Setting GONETEM_REQUIRE_INTEGRATION=1 turns those skips
// into failures so the privileged CI job cannot silently lose coverage.
package testutil

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

// RequireIntegrationEnv is set by the privileged integration workflow. When it
// is "1", a missing capability fails the test instead of skipping it.
const RequireIntegrationEnv = "GONETEM_REQUIRE_INTEGRATION"

// RequireRoot skips the test unless it runs as root. It is intended for tests
// that create network namespaces, veth pairs or qdiscs.
func RequireRoot(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires root privileges")
	}
	require(t, os.Geteuid() == 0, "requires root privileges")
}

// RequireDocker skips the test unless a Docker daemon is reachable.
func RequireDocker(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires Docker")
	}

	cli, err := newDockerClient()
	if err != nil {
		require(t, false, "Docker client unavailable: %v", err)
		return
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		require(t, false, "Docker daemon unavailable: %v", err)
	}
}

// RequireImage skips the test unless the Docker daemon is reachable and the
// given image is present locally.
func RequireImage(t testing.TB, image string) {
	t.Helper()
	RequireDocker(t)

	cli, err := newDockerClient()
	if err != nil {
		require(t, false, "Docker client unavailable: %v", err)
		return
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		require(t, false, "Docker image %s is not available: %v", image, err)
	}
}

func newDockerClient() (*client.Client, error) {
	return client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
}

func require(t testing.TB, ok bool, format string, args ...any) {
	t.Helper()
	if ok {
		return
	}
	if os.Getenv(RequireIntegrationEnv) == "1" {
		t.Fatalf("integration requirement not met: "+format, args...)
	}
	t.Skipf(format, args...)
}
