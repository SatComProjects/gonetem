package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/moby/term"
)

// ContainerRuntime is the container lifecycle surface used by node and OVS
// adapters.
type ContainerRuntime interface {
	Create(ctx context.Context, imgName, containerName, hostName string, volumes []string, ipv6, mpls bool) (string, error)
	Start(ctx context.Context, containerID string) error
	Stop(ctx context.Context, containerID string) error
	Rm(ctx context.Context, containerID string) error
	Pid(ctx context.Context, containerID string) (int, error)
	GetState(ctx context.Context, containerID string) (string, error)
	Get(ctx context.Context, containerID string) (*container.Summary, error)
	List(ctx context.Context, prefix string) ([]NetemContainerList, error)
}

// ContainerExec is the command execution surface used by node and OVS
// adapters.
type ContainerExec interface {
	Exec(ctx context.Context, containerID string, cmd []string) (string, error)
	ExecWithWorkingDir(ctx context.Context, containerID string, cmd []string, workingDir string) (string, error)
	ExecOutStream(ctx context.Context, containerID string, cmd []string, out io.Writer) error
	ExecTty(ctx context.Context, containerID string, cmd []string, in io.ReadCloser, out io.Writer, tty bool, ttyHeight, ttyWidth uint, resizeCh chan term.Winsize) error
}

// ContainerFiles is the file transfer surface used by node and OVS adapters.
type ContainerFiles interface {
	IsFileExist(ctx context.Context, containerID, path string) bool
	IsFolderExist(ctx context.Context, containerID, path string) bool
	CopyFrom(ctx context.Context, containerID, source, dest string) error
	CopyTo(ctx context.Context, containerID, source, dest string) error
}

// ImageManager is the image surface used by node and OVS adapters.
type ImageManager interface {
	IsImagePresent(ctx context.Context, imgName string) (bool, error)
	ImagePull(ctx context.Context, imgName string) error
}

// ContainerNodeAPI is the Docker surface a node or OVS adapter needs.
// DockerClient implements it; tests can provide a fake.
type ContainerNodeAPI interface {
	ContainerRuntime
	ContainerExec
	ContainerFiles
	ImageManager
}

// ContainerAPI is the complete client surface, including administrative
// operations used by the server handlers.
type ContainerAPI interface {
	ContainerNodeAPI
	Close() error
}

var _ ContainerAPI = (*DockerClient)(nil)
