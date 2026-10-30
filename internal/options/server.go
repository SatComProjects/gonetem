package options

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"sigs.k8s.io/yaml"
)

// VERSION is a variable so release builds can override it with:
//
//	-ldflags "-X github.com/mroy31/gonetem/internal/options.VERSION=..."
var VERSION = "0.8.0"

const (
	IMAGE_VERSION         = "0.6.0"
	NETEM_ID              = "ntm"
	SERVER_CONFIG_FILE    = "/etc/gonetem/config.yaml"
	MINIMUM_SERVER_CONFIG = `
listen: "localhost:10110"
tls:
  enabled: false
  ca: ""
  cert: ""
  key: ""
workdir: /tmp
docker:
  timeoutop: 60
  createConcurrency: 30
  startConcurrency: 30
  removeConcurrency: 30
  configConcurrency: 30
  linkConcurrency: 30
  routeConcurrency: 32
  nodes:
    router:
      image: mroy31/gonetem-frr
    host:
      image: mroy31/gonetem-host
    server:
      image: mroy31/gonetem-server
  extraNodes: []
update:
  enabled: false
  repository: https://github.com/satcomprojects/gonetem
  branch: master
  goVersion: "1.24.0"
  workdir: ""
`
	INITIAL_SERVER_CONFIG = `
listen: "localhost:10110"
tls:
  enabled: false
  ca: ""
  cert: ""
  key: ""
workdir: /tmp
docker:
  timeoutop: 60
  createConcurrency: 30
  startConcurrency: 30
  removeConcurrency: 30
  configConcurrency: 30
  linkConcurrency: 30
  routeConcurrency: 32
  nodes:
    router:
      type: router
      image: mroy31/gonetem-frr
      volumes: []
      logOutput: false
      options:
        log: false
        tso: true
      commands:
        console: /usr/bin/vtysh
        shell: /bin/bash
        loadConfig:
        - command: /usr/lib/frr/frrinit.sh start
          checkFiles: []
        saveConfig:
        - command: vtysh -w
          checkFiles: []
      configurationFiles:
      - destSuffix: frr.conf
        source: /etc/frr/frr.conf
        label: FRR
    host:
      type: host
      image: mroy31/gonetem-host
      volumes: []
      options:
        log: true
        tso: false
      commands:
        console: /bin/bash
        shell: /bin/bash
        loadConfig:
        - command: network-config.py -l /tmp/custom.net.conf
          checkFiles:
          - /tmp/custom.net.conf
        - command: /bin/bash /gonetem-init.sh
          checkFiles:
          - /gonetem-init.sh
        saveConfig:
        - command: network-config.py -s /tmp/custom.net.conf
          checkFiles: []
      configurationFiles:
      - destSuffix: init.conf
        source: /gonetem-init.sh
        label: Init
      - destSuffix: net.conf
        source: /tmp/custom.net.conf
        label: Network
      - destSuffix: ntp.conf
        source: /etc/ntpsec/ntp.conf
        label: NTP
    server:
      type: server
      image: mroy31/gonetem-server
      volumes: []
      options:
        log: true
        tso: false
      commands:
        console: /bin/bash
        shell: /bin/bash
        loadConfig:
        - command: network-config.py -l /tmp/custom.net.conf
          checkFiles:
          - /tmp/custom.net.conf
        - command: /bin/bash /gonetem-init.sh
          checkFiles:
          - /gonetem-init.sh
        saveConfig:
        - command: network-config.py -s /tmp/custom.net.conf
          checkFiles: []
      configurationFiles:
      - destSuffix: init.conf
        source: /gonetem-init.sh
        label: Init
      - destSuffix: net.conf
        source: /tmp/custom.net.conf
        label: Network
      - destSuffix: ntp.conf
        source: /etc/ntpsec/ntp.conf
        label: NTP
      - destSuffix: dhcpd.conf
        source: /etc/dhcp/dhcpd.conf
        label: DHCP
      - destSuffix: tftpd-hpa.default
        source: /etc/default/tftpd-hpa
        label: TFTP
      - destSuffix: isc-relay.default
        source: /etc/default/isc-dhcp-relay
        label: DHCP-RELAY
      - destSuffix: bind.default
        source: /etc/default/named
        label: Bind
      configurationFolders:
      - destSuffix: tftp-data.tgz
        source: /srv/tftp
        label: TFTP-Data
      - destSuffix: bind-etc.tgz
        source: /etc/bind
        label: Bind-Data
    p4sw:
      type: p4sw
      image: mroy31/gonetem-bmv2
      options:
        log: false
        tso: true
      commands:
        console: /usr/local/bin/simple_switch_CLI
        shell: /bin/bash
        loadConfig:
        - command: /usr/local/bin/launch-sw.py
          checkFiles: []  
        saveConfig:
      configurationFiles:
  extraNodes: []
  ovsImage: mroy31/gonetem-ovs
update:
  enabled: false
  repository: https://github.com/satcomprojects/gonetem
  branch: master
  goVersion: "1.24.0"
  workdir: ""
`
)

type DockerConfiguration struct {
	DestSuffix string
	Source     string
	Label      string
}

type DockerConfigCommand struct {
	Command    string
	CheckFiles []string
}

type DockerNodeOptions struct {
	Log bool
	Tso bool
}

type DockerNodeConfig struct {
	Type     string
	Image    string
	Volumes  []string
	Commands struct {
		Console    string
		Shell      string
		LoadConfig []DockerConfigCommand
		SaveConfig []DockerConfigCommand
	}
	ConfigurationFiles   []DockerConfiguration
	ConfigurationFolders []DockerConfiguration
	Options              DockerNodeOptions
}

// UpdateConfig configures the self-update of gonetem-server, triggered by
// the `gonetem-console update` command.
type UpdateConfig struct {
	// Enabled must be set to true for the server to accept update requests.
	Enabled bool
	// Repository is the git repository cloned to build the new version.
	Repository string
	// Branch is the git branch (or tag) checked out in Repository.
	Branch string
	// GoVersion is the version of go downloaded to build gonetem.
	GoVersion string
	// Workdir is the folder used to clone and build gonetem. If empty, a
	// temporary directory is used.
	Workdir string
}

type NetemServerConfig struct {
	Listen  string
	Tls     TLSOptions
	Workdir string
	Docker  struct {
		Timeoutop         int
		CreateConcurrency int
		StartConcurrency  int
		RemoveConcurrency int
		ConfigConcurrency int
		LinkConcurrency   int
		RouteConcurrency  int
		Nodes             struct {
			Router DockerNodeConfig
			Host   DockerNodeConfig
			Server DockerNodeConfig
			P4sw   DockerNodeConfig
		}
		ExtraNodes []DockerNodeConfig
		OvsImage   string
	}
	Update UpdateConfig
}

var (
	ServerConfig = NetemServerConfig{}
)

func InitServerConfig() {
	err := yaml.Unmarshal([]byte(INITIAL_SERVER_CONFIG), &ServerConfig)
	if err != nil {
		log.Fatalf("Unable to initialize server config: %v", err)
	}
}

func CreateServerConfig(config string) error {
	return os.WriteFile(config, []byte(MINIMUM_SERVER_CONFIG), 0644)
}

func ParseServerConfig(config string) error {
	data, err := os.ReadFile(config)
	if err != nil {
		return err
	}

	if err := yaml.Unmarshal(data, &ServerConfig); err != nil {
		return err
	}

	// The update section is optional: keep the defaults for the keys that
	// are not defined in the configuration file.
	defaults := NetemServerConfig{}
	if err := yaml.Unmarshal([]byte(INITIAL_SERVER_CONFIG), &defaults); err != nil {
		return err
	}

	if ServerConfig.Update.Repository == "" {
		ServerConfig.Update.Repository = defaults.Update.Repository
	}
	if ServerConfig.Update.Branch == "" {
		ServerConfig.Update.Branch = defaults.Update.Branch
	}
	if ServerConfig.Update.GoVersion == "" {
		ServerConfig.Update.GoVersion = defaults.Update.GoVersion
	}
	if ServerConfig.Docker.RouteConcurrency == 0 {
		ServerConfig.Docker.RouteConcurrency = defaults.Docker.RouteConcurrency
	}

	return nil
}

func GetDockerImageId(image string) string {
	// Preserve an existing tag or digest. A colon after the last slash is a
	// tag; '@' introduces a digest.
	if strings.Contains(image, "@") {
		return image
	}

	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image
	}

	return fmt.Sprintf("%s:%s", image, IMAGE_VERSION)
}

func LoadServerTLSCredentials() (credentials.TransportCredentials, error) {
	certPool, serverCerts, err := loadTLSCerts(ServerConfig.Tls)
	if err != nil {
		return nil, err
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates: serverCerts,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
	}

	return credentials.NewTLS(config), nil
}
