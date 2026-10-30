package options

import (
	"fmt"
	"os"
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

func TestOptions_ParseServerConfig_UpdateDefaults(t *testing.T) {
	config := t.TempDir() + "/config.yaml"
	if err := os.WriteFile(config, []byte("listen: \"localhost:10110\"\n"), 0644); err != nil {
		t.Fatalf("unable to write config file: %v", err)
	}

	// ParseServerConfig modifies the global config, restore it for other tests.
	previous := ServerConfig
	defer func() { ServerConfig = previous }()

	if err := ParseServerConfig(config); err != nil {
		t.Fatalf("unable to parse config file: %v", err)
	}

	if ServerConfig.Update.Enabled {
		t.Error("update must be disabled by default")
	}
	if ServerConfig.Update.Repository != "https://github.com/satcomprojects/gonetem" {
		t.Errorf("unexpected default repository: %s", ServerConfig.Update.Repository)
	}
	if ServerConfig.Update.Branch != "master" {
		t.Errorf("unexpected default branch: %s", ServerConfig.Update.Branch)
	}
	if ServerConfig.Update.GoVersion != "1.24.0" {
		t.Errorf("unexpected default go version: %s", ServerConfig.Update.GoVersion)
	}
}

func TestOptions_ParseServerConfig_UpdateSection(t *testing.T) {
	config := t.TempDir() + "/config.yaml"
	data := `
listen: "localhost:10110"
update:
  enabled: true
  branch: develop
  goVersion: "1.25.1"
  workdir: /var/tmp/gonetem
`
	if err := os.WriteFile(config, []byte(data), 0644); err != nil {
		t.Fatalf("unable to write config file: %v", err)
	}

	previous := ServerConfig
	defer func() { ServerConfig = previous }()

	if err := ParseServerConfig(config); err != nil {
		t.Fatalf("unable to parse config file: %v", err)
	}

	if !ServerConfig.Update.Enabled {
		t.Error("update must be enabled")
	}
	if ServerConfig.Update.Branch != "develop" {
		t.Errorf("unexpected branch: %s", ServerConfig.Update.Branch)
	}
	if ServerConfig.Update.GoVersion != "1.25.1" {
		t.Errorf("unexpected go version: %s", ServerConfig.Update.GoVersion)
	}
	if ServerConfig.Update.Workdir != "/var/tmp/gonetem" {
		t.Errorf("unexpected workdir: %s", ServerConfig.Update.Workdir)
	}
	if ServerConfig.Update.Repository != "https://github.com/satcomprojects/gonetem" {
		t.Errorf("repository must keep its default value: %s", ServerConfig.Update.Repository)
	}
}
