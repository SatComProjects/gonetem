package options

import (
	"fmt"
	"testing"
)

type DockerImageT int

const (
	IMG_ROUTER DockerImageT = iota
	IMG_HOST
	IMG_SERVER
	IMG_OVS
)

func getImageFromT(imgId DockerImageT) string {
	switch imgId {
	case IMG_ROUTER:
		return GetDockerImageId(ServerConfig.Docker.Nodes.Router.Image)
	case IMG_HOST:
		return GetDockerImageId(ServerConfig.Docker.Nodes.Host.Image)
	case IMG_SERVER:
		return GetDockerImageId(ServerConfig.Docker.Nodes.Server.Image)
	case IMG_OVS:
		return GetDockerImageId(ServerConfig.Docker.OvsImage)
	}

	return ""
}

func TestOptions_ImageId(t *testing.T) {
	InitServerConfig()

	tests := []struct {
		imgType    DockerImageT
		expectedID string
	}{
		{
			imgType:    IMG_HOST,
			expectedID: fmt.Sprintf("%s:%s", ServerConfig.Docker.Nodes.Host.Image, IMAGE_VERSION),
		},
		{
			imgType:    IMG_SERVER,
			expectedID: fmt.Sprintf("%s:%s", ServerConfig.Docker.Nodes.Server.Image, IMAGE_VERSION),
		},
		{
			imgType:    IMG_ROUTER,
			expectedID: fmt.Sprintf("%s:%s", ServerConfig.Docker.Nodes.Router.Image, IMAGE_VERSION),
		},
		{
			imgType:    IMG_OVS,
			expectedID: fmt.Sprintf("%s:%s", ServerConfig.Docker.OvsImage, IMAGE_VERSION),
		},
	}
	for _, test := range tests {
		id := GetDockerImageId(getImageFromT(test.imgType))
		if id != test.expectedID {
			t.Errorf("Error: %s != %s", id, test.expectedID)
		}
	}

	tagged := []string{
		"mroy31/ovs-img:0.0.0",
		"mroy31/ovs-img:1.0-rc1",
		"repo@sha256:abcdef",
	}
	for _, image := range tagged {
		if id := GetDockerImageId(image); id != image {
			t.Errorf("existing tag or digest was modified: %s != %s", id, image)
		}
	}

	withPort := "registry.example.com:5000/ovs-img"
	expectedPort := withPort + ":" + IMAGE_VERSION
	if id := GetDockerImageId(withPort); id != expectedPort {
		t.Fatalf("registry port was mistaken for a tag: %s != %s", id, expectedPort)
	}
}
