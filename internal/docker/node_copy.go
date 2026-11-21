package docker

import "context"

func (n *DockerNode) CopyFrom(source, dest string) error {
	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	return client.CopyFrom(context.Background(), n.ID, source, dest)
}

func (n *DockerNode) CopyTo(source, dest string) error {
	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	return client.CopyTo(context.Background(), n.ID, source, dest)
}
