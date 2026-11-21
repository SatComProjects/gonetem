package docker

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/shlex"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/satcomprojects/gonetem/internal/options"
)

func vrrpInterfaceName(ifIndex, group int) string {
	return fmt.Sprintf("vrrp-%d-%d", ifIndex, group)
}

func (n *DockerNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	n.configMu.Lock()
	defer n.configMu.Unlock()

	var messages []string

	if !n.IsRunning() {
		n.Logger.Info("LoadConfig: node not running")
		return messages, nil
	}

	n.mu.RLock()
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	if !configLoaded {
		ns, err := n.GetRunningNetns()
		if err != nil {
			return messages, fmt.Errorf("unable to get netns of node %s: %w", n.Name, err)
		}
		defer ns.Close()

		// create vrfs
		for idx, vrf := range n.Vrfs {
			if _, err := link.CreateVrf(vrf, ns, 10+idx); err != nil {
				return messages, err
			}
		}

		client, err := n.dockerClient()
		if err != nil {
			return messages, err
		}

		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc

			ctx, cancel = context.WithTimeoutCause(
				ctx,
				time.Duration(timeout)*time.Second,
				fmt.Errorf("node %s: loadconfig op timeout", n.Name))
			defer cancel()
		}

		// create vrrps interfaces
		for _, vrrpGroup := range n.Vrrps {
			name := vrrpInterfaceName(vrrpGroup.Interface, vrrpGroup.Group)
			if _, err := link.CreateVRRPMacVlan(
				name, fmt.Sprintf("eth%d", vrrpGroup.Interface),
				vrrpGroup.Group, ns); err != nil {
				return messages, err
			}
			if err := link.SetInterfaceState(name, ns, link.IFSTATE_UP); err != nil {
				return messages, fmt.Errorf("unable to set VRRP interface %s up: %w", name, err)
			}

			// set ip address
			if _, err := client.Exec(ctx, n.ID, []string{"ip", "addr", "add", vrrpGroup.Address, "dev", name}); err != nil {
				return messages, err
			}

			// modify kernel settings to disable routes when interface
			// is in linkdown state
			_, err := client.Exec(ctx, n.ID, []string{"sysctl", "-w", fmt.Sprintf("net.ipv4.conf.%s.ignore_routes_with_linkdown=1", name)})
			if err != nil {
				return messages, err
			}
		}

		// Load confifuration files/folders
		if _, err := os.Stat(confPath); err == nil {
			for _, confFileOpts := range n.Config.ConfigurationFiles {
				confFile := path.Join(confPath, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
				if _, err := os.Stat(confFile); os.IsNotExist(err) {
					continue
				}

				if err := client.CopyTo(ctx, n.ID, confFile, confFileOpts.Source); err != nil {
					return messages, fmt.Errorf("unable to load config file %s:\n\tdest: %s\n\t%w", confFile, confFileOpts.Source, err)
				}
			}

			for _, confFolderOpts := range n.Config.ConfigurationFolders {
				filename := fmt.Sprintf("%s.%s", n.Name, confFolderOpts.DestSuffix)
				confFolderArchive := path.Join(confPath, filename)
				if _, err := os.Stat(confFolderArchive); os.IsNotExist(err) {
					continue
				}

				if err := client.CopyTo(ctx, n.ID, confFolderArchive, filepath.Join("/tmp", filename)); err != nil {
					return messages, fmt.Errorf("unable to copy archile file %s:\n\t%w", filename, err)
				}

				if _, err := client.ExecWithWorkingDir(ctx, n.ID, []string{"tar", "xzf", filepath.Join("/tmp", filename)}, "/"); err != nil {
					return messages, fmt.Errorf("unable to extract archile file %s in the node:\n\t%w", filename, err)
				}
			}
		}

		// Execute load config commands
		for _, loadConfigCmd := range n.Config.Commands.LoadConfig {
			canExec := true
			for _, filepath := range loadConfigCmd.CheckFiles {
				if !client.IsFileExist(context.Background(), n.ID, filepath) {
					canExec = false
				}
			}
			if !canExec {
				continue
			}

			cmd, err := shlex.Split(loadConfigCmd.Command)
			if err != nil {
				return messages, fmt.Errorf("unable to parse loadConfig command %s: %v", loadConfigCmd.Command, err)
			}

			output, err := client.Exec(ctx, n.ID, cmd)
			output = strings.Trim(output, " ")
			if err != nil {
				return messages, fmt.Errorf("node %s - unable to exec load config cmd %s - %v", n.Name, loadConfigCmd.Command, err)
			} else if output != "" && output != "\n" && n.Config.Options.Log {
				messages = append(messages, output)
			}
		}

		n.mu.Lock()
		n.ConfigLoaded = true
		n.mu.Unlock()
	}

	return messages, nil
}

func (n *DockerNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	configFilesData := make(map[string][]byte)

	n.mu.RLock()
	running := n.Running
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	filesDir := confDir
	if running || configLoaded {
		// create temp directory for the project
		dir, err := os.MkdirTemp(options.ServerConfig.Workdir, "gonetem-config-node"+n.Name+"-")
		if err != nil {
			return nil, fmt.Errorf("unable to create temp folder to save node config: %w", err)
		}
		defer os.RemoveAll(dir)

		if err := n.Save(dir, timeout); err != nil {
			return nil, fmt.Errorf("unable to save node configs in temp folder %s: %w", dir, err)
		}
		filesDir = dir
	}

	for _, confFileOpts := range n.Config.ConfigurationFiles {
		filepath := path.Join(filesDir, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
		if _, err := os.Stat(filepath); os.IsNotExist(err) {
			configFilesData[confFileOpts.Label] = []byte{}
			continue
		}

		data, err := os.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("unable to read config file '%s':\n\t%w", filepath, err)
		}
		configFilesData[confFileOpts.Label] = data
	}

	return configFilesData, nil
}

func (n *DockerNode) Save(dstPath string, timeout int) error {
	n.mu.RLock()
	running := n.Running
	configLoaded := n.ConfigLoaded
	n.mu.RUnlock()

	if !running || !configLoaded {
		n.Logger.Info("Save: node not running")
		return nil
	}

	client, err := n.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(
			ctx,
			time.Duration(timeout)*time.Second)
		defer cancel()
	}

	// Execute save config commands
	for _, saveConfigCmd := range n.Config.Commands.SaveConfig {
		canExec := true
		for _, filepath := range saveConfigCmd.CheckFiles {
			if !client.IsFileExist(context.Background(), n.ID, filepath) {
				canExec = false
			}
		}
		if !canExec {
			continue
		}

		cmd, err := shlex.Split(saveConfigCmd.Command)
		if err != nil {
			return fmt.Errorf("unable to parse save config command %s: %v", saveConfigCmd.Command, err)
		}

		if _, err := client.Exec(ctx, n.ID, cmd); err != nil {
			return fmt.Errorf("node %s - unable to exec save config cmd %s - %v", n.Name, saveConfigCmd.Command, err)
		}
	}

	for _, confFileOpts := range n.Config.ConfigurationFiles {
		if !client.IsFileExist(ctx, n.ID, confFileOpts.Source) {
			continue
		}

		confFile := path.Join(dstPath, fmt.Sprintf("%s.%s", n.Name, confFileOpts.DestSuffix))
		if err := client.CopyFrom(ctx, n.ID, confFileOpts.Source, confFile); err != nil {
			return fmt.Errorf("unable to save config file %s:\n\t%w", confFile, err)
		}
	}

	for _, confFolderOpts := range n.Config.ConfigurationFolders {
		if !client.IsFolderExist(ctx, n.ID, confFolderOpts.Source) {
			continue
		}

		filename := fmt.Sprintf("%s.%s", n.Name, confFolderOpts.DestSuffix)
		confFolderArchive := path.Join(dstPath, filename)

		if _, err := client.ExecWithWorkingDir(ctx, n.ID, []string{"tar", "czf", "/tmp/" + filename, confFolderOpts.Source}, "/"); err != nil {
			return err
		}

		if err := client.CopyFrom(ctx, n.ID, "/tmp/"+filename, confFolderArchive); err != nil {
			return fmt.Errorf("node %s - unable to save archive file %s:\n\t%v", n.Name, filename, err)
		}
	}

	return nil
}
