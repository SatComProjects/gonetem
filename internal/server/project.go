package server

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/mroy31/gonetem/internal/options"
	"github.com/mroy31/gonetem/internal/utils"
)

type ProjectNotFoundError struct {
	Id string
}

func (e *ProjectNotFoundError) Error() string {
	return "Project " + e.Id + ": not found"
}

type NetemProject struct {
	Id       string
	Name     string
	Dir      string
	OpenAt   time.Time
	Topology *NetemTopologyManager
}

var (
	openProjects = make(map[string]*NetemProject)
	projectsMu   sync.RWMutex
	projectOpsMu sync.Mutex
)

func ProjectIsExist(prjName string) bool {
	projectsMu.RLock()
	defer projectsMu.RUnlock()

	for _, prj := range openProjects {
		if prj.Name == prjName {
			return true
		}
	}
	return false
}

func ProjectIsIdExist(prjID string) bool {
	projectsMu.RLock()
	defer projectsMu.RUnlock()
	_, found := openProjects[prjID]
	return found
}

func ProjectGetMany() map[string]*NetemProject {
	projectsMu.RLock()
	defer projectsMu.RUnlock()

	snapshot := make(map[string]*NetemProject, len(openProjects))
	for id, prj := range openProjects {
		snapshot[id] = prj
	}
	return snapshot
}

func ProjectGetOne(prjID string) *NetemProject {
	projectsMu.RLock()
	defer projectsMu.RUnlock()
	return openProjects[prjID]
}

func ProjectOpen(prjId, name string, data []byte) (*NetemProject, error) {
	projectOpsMu.Lock()
	defer projectOpsMu.Unlock()

	if ProjectIsExist(name) {
		return nil, fmt.Errorf("a project named %s is already open", name)
	}

	// create temp directory for the project
	dir, err := os.MkdirTemp(options.ServerConfig.Workdir, "gonetem-"+prjId+"-")
	if err != nil {
		return nil, fmt.Errorf("unable to create temp folder for project: %w", err)
	}

	if err := utils.OpenArchive(dir, bytes.NewReader(data)); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("unable to open project: %w", err)
	}

	// load the topology
	topology, err := LoadTopology(prjId, dir)
	if err != nil {
		topology.Close(nil)
		os.RemoveAll(dir)
		return nil, err
	}

	prj := &NetemProject{
		Id:       prjId,
		Name:     name,
		Dir:      dir,
		OpenAt:   time.Now(),
		Topology: topology,
	}

	projectsMu.Lock()
	defer projectsMu.Unlock()
	if _, found := openProjects[prjId]; found {
		topology.Close(nil)
		os.RemoveAll(dir)
		return nil, fmt.Errorf("project id %s is already open", prjId)
	}
	openProjects[prjId] = prj

	return prj, nil
}

func ProjectSave(prjId string, progressCh chan TopologySaveProgressT) (*bytes.Buffer, error) {
	projectOpsMu.Lock()
	defer projectOpsMu.Unlock()

	project := ProjectGetOne(prjId)
	if project == nil {
		return nil, &ProjectNotFoundError{prjId}
	}

	if err := project.Topology.Save(progressCh); err != nil {
		return nil, err
	}

	buffer := new(bytes.Buffer)
	if err := utils.CreateArchive(project.Dir, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}

func ProjectGetNodeConfigs(prjId string) (*bytes.Buffer, error) {
	projectOpsMu.Lock()
	defer projectOpsMu.Unlock()

	project := ProjectGetOne(prjId)
	if project == nil {
		return nil, &ProjectNotFoundError{prjId}
	}

	// save project before return config archive
	if err := project.Topology.Save(nil); err != nil {
		return nil, err
	}

	configPath := path.Join(project.Dir, configDir)
	buffer := new(bytes.Buffer)
	if err := utils.CreateArchive(configPath, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}

func ProjectClose(prjId string, progressCh chan TopologyRunCloseProgressT) error {
	projectOpsMu.Lock()
	defer projectOpsMu.Unlock()

	project := ProjectGetOne(prjId)
	if project == nil {
		return &ProjectNotFoundError{prjId}
	}

	err := project.Topology.Close(progressCh)

	projectsMu.Lock()
	delete(openProjects, prjId)
	projectsMu.Unlock()

	os.RemoveAll(project.Dir)
	return err
}
