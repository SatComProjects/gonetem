package ovs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

func (o *OvsProjectInstance) LoadConfig(name, brName, confPath string, timeout int) ([]string, error) {
	var messages []string

	client, err := o.dockerClient()
	if err != nil {
		return messages, err
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	tmpConfFile := "/tmp/" + name + ".conf"
	confFile := path.Join(confPath, name+".conf")
	if _, err := os.Stat(confFile); os.IsNotExist(err) {
		return messages, nil
	}

	if err := client.CopyTo(ctx, o.containerId, confFile, tmpConfFile); err != nil {
		return messages, fmt.Errorf("unable to copy config file %s:\n\t%w", confFile, err)
	}

	cmd := []string{"ovs-config.py", "-a", "load", "-c", tmpConfFile, brName}
	output, err := client.Exec(ctx, o.containerId, cmd)
	if err != nil {
		return messages, fmt.Errorf("unable to load config file %s:\n\t%w", confFile, err)
	} else if output != "" {
		messages = strings.Split(output, "\n")
	}

	return messages, nil
}

func (o *OvsProjectInstance) SaveConfig(name, brName, dstPath string, timeout int) error {
	client, err := o.dockerClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	tmpConfFile := "/tmp/" + name + ".conf"
	confFile := path.Join(dstPath, name+".conf")

	cmd := []string{"ovs-config.py", "-a", "save", "-c", tmpConfFile, brName}
	if _, err := client.Exec(ctx, o.containerId, cmd); err != nil {
		return fmt.Errorf("unable to save config in file %s:\n\t%w", confFile, err)
	}

	if err := client.CopyFrom(ctx, o.containerId, tmpConfFile, confFile); err != nil {
		msg := fmt.Sprintf("Unable to save file %s:\n\t%v", confFile, err)
		return errors.New(msg)
	}

	return nil
}
