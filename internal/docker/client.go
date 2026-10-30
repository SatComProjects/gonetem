package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/moby/term"
	"github.com/mroy31/gonetem/internal/utils"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

type NetemContainerList struct {
	Container container.Summary
	Name      string
}

type DockerClient struct {
	cli *client.Client
}

var (
	imagePresenceMu    sync.RWMutex
	imagePresenceCache = make(map[string]bool)
	imagePresenceGroup singleflight.Group
)

func rememberImagePresent(image string) {
	imagePresenceMu.Lock()
	imagePresenceCache[image] = true
	imagePresenceMu.Unlock()
}

func imagePresentCached(ctx context.Context, image string, check func(context.Context) (bool, error)) (bool, error) {
	imagePresenceMu.RLock()
	present, found := imagePresenceCache[image]
	imagePresenceMu.RUnlock()
	if found {
		return present, nil
	}

	value, err, _ := imagePresenceGroup.Do(image, func() (any, error) {
		imagePresenceMu.RLock()
		cached, ok := imagePresenceCache[image]
		imagePresenceMu.RUnlock()
		if ok {
			return cached, nil
		}

		present, err := check(ctx)
		if err != nil {
			return false, err
		}
		if present {
			rememberImagePresent(image)
		}
		return present, nil
	})
	if err != nil {
		return false, err
	}
	return value.(bool), nil
}

func (c *DockerClient) Close() error {
	// The underlying Docker client is shared for the lifetime of the process.
	// Closing individual handles would break concurrent/reused operations.
	return nil
}

func (c *DockerClient) isImagePresentUncached(ctx context.Context, imgName string) (bool, error) {
	list, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return false, err
	}

	for _, imgInfo := range list {
		for _, tag := range imgInfo.RepoTags {
			if tag == imgName {
				return true, nil
			}
		}
	}
	return false, nil
}

func (c *DockerClient) IsImagePresent(ctx context.Context, imgName string) (bool, error) {
	return imagePresentCached(ctx, imgName, func(ctx context.Context) (bool, error) {
		return c.isImagePresentUncached(ctx, imgName)
	})
}

func (c *DockerClient) ImagePull(ctx context.Context, imgName string) error {
	out, err := c.cli.ImagePull(ctx, imgName, image.PullOptions{})
	if err != nil {
		return err
	}
	defer out.Close()

	dec := json.NewDecoder(out)
	for {
		var msg jsonmessage.JSONMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				rememberImagePresent(imgName)
				return nil
			}
			return err
		}
		if msg.Error != nil {
			return msg.Error
		}
	}
}

func (c *DockerClient) List(ctx context.Context, prefix string) ([]NetemContainerList, error) {
	result := make([]NetemContainerList, 0)

	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return result, err
	}

	for _, container := range list {
		for _, name := range container.Names {
			if strings.HasPrefix(name, "/"+prefix) {
				result = append(result, NetemContainerList{
					Container: container,
					Name:      name[1:],
				})
				continue
			}
		}
	}

	return result, nil
}

func (c *DockerClient) Get(ctx context.Context, containerId string) (*types.Container, error) {
	containerInfo, err := c.cli.ContainerInspect(ctx, containerId)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, fmt.Errorf("container with id %s does not exist", containerId)
		}
		return nil, err
	}

	cSummary := &types.Container{
		ID:    containerInfo.ID,
		Names: []string{containerInfo.Name},
		State: containerInfo.State.Status,
	}
	if containerInfo.Config != nil {
		cSummary.Image = containerInfo.Config.Image
	}
	if containerInfo.Image != "" {
		cSummary.ImageID = containerInfo.Image
	}
	if containerInfo.State != nil {
		cSummary.Status = containerInfo.State.Status
	}

	return cSummary, nil
}

func (c *DockerClient) GetState(ctx context.Context, containerId string) (string, error) {
	containerInfo, err := c.cli.ContainerInspect(ctx, containerId)
	if err != nil {
		return "", err
	}

	return containerInfo.State.Status, nil
}

func (c *DockerClient) Create(
	ctx context.Context,
	imgName,
	containerName,
	hostName string,
	volumes []string,
	ipv6, mpls bool,
) (string, error) {
	hostConfig := container.HostConfig{
		NetworkMode: "none",
		Privileged:  true,
		CapAdd:      []string{"ALL"},
		Sysctls:     make(map[string]string),
		Binds:       volumes,
	}
	if ipv6 {
		hostConfig.Sysctls["net.ipv6.conf.all.disable_ipv6"] = "0"
	}
	if mpls {
		hostConfig.Sysctls["net.mpls.platform_labels"] = "100000"
		hostConfig.Sysctls["net.mpls.conf.lo.input"] = "1"
	}

	resp, err := c.cli.ContainerCreate(ctx, &container.Config{
		Image:    imgName,
		Hostname: hostName,
		Tty:      false,
	}, &hostConfig, nil, nil, containerName)
	if err != nil {
		return "", err
	}

	return resp.ID, nil
}

func (c *DockerClient) Start(ctx context.Context, containerId string) error {
	return c.cli.ContainerStart(ctx, containerId, container.StartOptions{})
}

func (c *DockerClient) Stop(ctx context.Context, containerId string) error {
	state, err := c.GetState(ctx, containerId)
	if err != nil {
		return err
	}

	if state == "running" {
		return c.cli.ContainerKill(ctx, containerId, "SIGKILL")
	}

	return nil
}

func (c *DockerClient) Rm(ctx context.Context, containerId string) error {
	err := c.cli.ContainerRemove(ctx, containerId, container.RemoveOptions{Force: true})
	if err != nil && client.IsErrNotFound(err) {
		// Cleanup and project close are idempotent.
		return nil
	}
	return err
}

func (c *DockerClient) Pid(ctx context.Context, containerId string) (int, error) {
	containerInfo, err := c.cli.ContainerInspect(ctx, containerId)
	if err != nil {
		return -1, err
	}

	if !containerInfo.State.Running {
		return -1, fmt.Errorf("container %s is not running", containerId)
	}
	return containerInfo.State.Pid, nil
}

func (c *DockerClient) IsFileExist(ctx context.Context, containerId, filepath string) bool {
	sourceStat, err := c.cli.ContainerStatPath(ctx, containerId, filepath)
	if err != nil {
		return false
	}

	return sourceStat.Mode.IsRegular()
}

func (c *DockerClient) IsFolderExist(ctx context.Context, containerId, filepath string) bool {
	sourceStat, err := c.cli.ContainerStatPath(ctx, containerId, filepath)
	if err != nil {
		return false
	}

	return sourceStat.Mode.IsDir()
}

func (c *DockerClient) CopyFrom(ctx context.Context, containerId, source, dest string) error {
	sourceStat, err := c.cli.ContainerStatPath(ctx, containerId, source)
	// we do not support the case where source is not a regular file
	if err == nil && !sourceStat.Mode.IsRegular() {
		return errors.New("DockerCopyFrom support only the copy of a regular file")
	}

	// check that Dir(dest) exists
	parentStat, err := os.Stat(path.Dir(dest))
	if err != nil || !parentStat.Mode().IsDir() {
		return fmt.Errorf("CopyFrom: %s is not a directory", path.Dir(dest))
	}

	reader, _, err := c.cli.CopyFromContainer(ctx, containerId, source)
	if err != nil {
		return err
	}
	defer reader.Close()

	destPath := dest
	if stat, err := os.Stat(dest); err == nil && stat.Mode().IsDir() {
		destPath = path.Join(dest, path.Base(source))
	}
	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	srcTar := tar.NewReader(reader)
	_, _ = srcTar.Next()
	if _, err = io.Copy(destFile, srcTar); err != nil {
		return err
	}

	return nil
}

func (c *DockerClient) CopyTo(ctx context.Context, containerId, source, dest string) error {
	stat, err := os.Stat(source)
	if err != nil {
		return err
	} else if !stat.Mode().IsRegular() {
		return errors.New("DockerCopyTo support only the copy of a regular file")
	}

	dstStat, err := c.cli.ContainerStatPath(ctx, containerId, dest)
	// we do not support the case where dstPath is a symbolic link
	if err == nil && dstStat.Mode&os.ModeSymlink != 0 {
		return errors.New("target destination is a symlink, this case is not supported")
	}

	dstPath := path.Dir(dest)
	dstName := path.Base(dest)
	if dstStat.Mode.IsDir() {
		dstPath = dest
		dstName = path.Base(source)
	}

	// create temp tar file to use the client API
	pReader, pWriter := io.Pipe()

	go func() {
		tarWriter := tar.NewWriter(pWriter)
		if err = utils.AddFileToTar(tarWriter, source, dstName); err != nil {
			pWriter.CloseWithError(err)
			return
		}
		tarWriter.Close()
		pWriter.Close()
	}()

	return c.cli.CopyToContainer(
		context.Background(), containerId, dstPath,
		pReader, container.CopyToContainerOptions{})
}

func (c *DockerClient) ExecWithWorkingDir(ctx context.Context, containerId string, cmd []string, workingDir string) (string, error) {
	config := container.ExecOptions{
		AttachStderr: true,
		AttachStdout: true,
		Cmd:          cmd,
		WorkingDir:   workingDir,
	}

	execID, err := c.cli.ContainerExecCreate(ctx, containerId, config)
	if err != nil {
		return "", err
	}

	resp, err := c.cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	// read the output
	var outBuf, errBuf bytes.Buffer
	outputDone := make(chan error)

	go func() {
		// StdCopy demultiplexes the stream into two buffers
		_, err = stdcopy.StdCopy(&outBuf, &errBuf, resp.Reader)
		outputDone <- err
	}()

	select {
	case err := <-outputDone:
		if err != nil {
			return "", err
		}
		break

	case <-ctx.Done():
		return "", ctx.Err()
	}

	stdout, err := io.ReadAll(&outBuf)
	if err != nil {
		return "", err
	}
	stderr, err := io.ReadAll(&errBuf)
	if err != nil {
		return "", err
	}

	res, err := c.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return "", err
	}

	if res.ExitCode != 0 {
		msg := fmt.Sprintf(
			"DockerExec returns an non-zero exit code: \n\t%s", string(stderr))
		return "", errors.New(msg)
	}
	return string(stdout), nil
}

func (c *DockerClient) Exec(ctx context.Context, containerId string, cmd []string) (string, error) {
	return c.ExecWithWorkingDir(ctx, containerId, cmd, "")
}

func (c *DockerClient) ExecOutStream(ctx context.Context, containerId string, cmd []string, out io.Writer) error {
	config := container.ExecOptions{
		AttachStderr: false,
		AttachStdout: true,
		Cmd:          cmd,
	}

	execID, err := c.cli.ContainerExecCreate(ctx, containerId, config)
	if err != nil {
		return err
	}

	resp, err := c.cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()

	// read the output
	var errBuf bytes.Buffer
	outputDone := make(chan error)

	go func() {
		_, err = stdcopy.StdCopy(out, &errBuf, resp.Reader)
		outputDone <- err
	}()

	select {
	case err := <-outputDone:
		if err != nil {
			return err
		}
		break

	case <-ctx.Done():
		return ctx.Err()
	}

	res, err := c.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return err
	}

	if res.ExitCode != 0 {
		return fmt.Errorf("ExecOutStream: exit code %d", res.ExitCode)
	}
	return nil
}

func (c *DockerClient) ExecTty(
	ctx context.Context,
	containerId string,
	cmd []string,
	in io.ReadCloser,
	out io.Writer,
	tty bool,
	ttyHeight uint,
	ttyWidth uint,
	resizeCh chan term.Winsize) error {
	config := container.ExecOptions{
		AttachStderr: true,
		AttachStdout: true,
		AttachStdin:  true,
		Tty:          tty,
		Cmd:          cmd,
		ConsoleSize:  &[2]uint{ttyHeight, ttyWidth},
	}

	execID, err := c.cli.ContainerExecCreate(ctx, containerId, config)
	if err != nil {
		return err
	}

	resp, err := c.cli.ContainerExecAttach(ctx, execID.ID, container.ExecAttachOptions{
		Tty:         config.Tty,
		ConsoleSize: config.ConsoleSize,
	})
	if err != nil {
		return err
	}
	defer resp.Close()

	// read the output
	outputDone := make(chan error)
	go func() {
		_, err = io.Copy(out, resp.Reader)
		outputDone <- err
	}()

	// write the input
	inputDone := make(chan error)
	go func() {
		_, err := io.Copy(resp.Conn, in)
		inputDone <- err
	}()

	// resize TTY goroutine
	go func() {
		for ws := range resizeCh {
			if err := c.cli.ContainerExecResize(ctx, execID.ID, container.ResizeOptions{
				Height: uint(ws.Height),
				Width:  uint(ws.Width),
			}); err != nil {
				logrus.WithField(
					"container",
					containerId,
				).Errorf("unable to resize TTY: %s", err)
			}
		}
	}()

	select {
	case err := <-outputDone:
		if err != nil {
			return err
		}
		break

	case <-inputDone:
		// Input stream has closed.
		// Wait for output to complete streaming.
		select {
		case err := <-outputDone:
			if err != nil {
				return err
			}
			break
		case <-ctx.Done():
			return ctx.Err()
		}

	case <-ctx.Done():
		return ctx.Err()
	}

	res, err := c.cli.ContainerExecInspect(ctx, execID.ID)
	if err != nil {
		return err
	}

	if res.ExitCode != 0 {
		return fmt.Errorf("ExecTty: exit code %d", res.ExitCode)
	}
	return nil
}

var (
	sharedDockerMu  sync.Mutex
	sharedDockerCLI *client.Client
)

func NewDockerClient() (*DockerClient, error) {
	sharedDockerMu.Lock()
	defer sharedDockerMu.Unlock()

	if sharedDockerCLI == nil {
		cli, err := client.NewClientWithOpts(client.FromEnv)
		if err != nil {
			return nil, err
		}
		sharedDockerCLI = cli
	}

	return &DockerClient{cli: sharedDockerCLI}, nil
}
