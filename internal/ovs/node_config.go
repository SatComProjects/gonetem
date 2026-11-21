package ovs

import (
	"fmt"
	"os"
	"path"
)

func (o *OvsNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	configFilesData := make(map[string][]byte)

	filesDir := confDir
	if o.IsRunning() {
		if err := o.ensureInstance(); err != nil {
			return nil, err
		}
		// create temp directory for the project
		dir, err := os.MkdirTemp(o.workdir, "gonetem-config-sw"+o.Name+"-")
		if err != nil {
			return nil, fmt.Errorf("unable to create temp folder to save ovs config: %w", err)
		}
		defer os.RemoveAll(dir)

		err = o.OvsInstance.SaveConfig(o.Name, o.GetBridgeName(), dir, timeout)
		if err != nil {
			return nil, fmt.Errorf("unable to save ovs config in temp folder %s: %w", dir, err)
		}
		filesDir = dir
	}

	confpath := path.Join(filesDir, o.Name+".conf")
	if _, err := os.Stat(confpath); os.IsNotExist(err) {
		configFilesData["OVS"] = []byte{}
		return configFilesData, nil
	}

	data, err := os.ReadFile(confpath)
	if err != nil {
		return nil, fmt.Errorf("unable to read ovs config file '%s':\n\t%w", confpath, err)
	}

	configFilesData["OVS"] = data
	return configFilesData, nil
}

func (o *OvsNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	if !o.IsRunning() {
		o.Logger.Warn("LoadConfig: node not running")
		return []string{}, nil
	}
	if err := o.ensureInstance(); err != nil {
		return []string{}, err
	}

	return o.OvsInstance.LoadConfig(o.Name, o.GetBridgeName(), confPath, timeout)
}

func (o *OvsNode) Save(dstPath string, timeout int) error {
	if !o.IsRunning() {
		o.Logger.Warn("Save: node not running")
		return nil
	}
	if err := o.ensureInstance(); err != nil {
		return err
	}

	return o.OvsInstance.SaveConfig(o.Name, o.GetBridgeName(), dstPath, timeout)
}
