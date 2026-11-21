package server

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/satcomprojects/gonetem/internal/utils"
)

// ProjectRegistry owns the open-project set for one server instance. All
// filesystem/archive operations that belong to a project go through it; the
// gRPC handlers never touch the map.
type ProjectRegistry struct {
	mu       sync.RWMutex
	opsMu    sync.Mutex
	projects map[string]*NetemProject
	config   Config
	factory  *NodeFactory
}

func NewProjectRegistry(config Config, factory *NodeFactory) *ProjectRegistry {
	return &ProjectRegistry{
		projects: make(map[string]*NetemProject),
		config:   config,
		factory:  factory,
	}
}

// IsNameOpen reports whether a project with the display name is open.
func (r *ProjectRegistry) IsNameOpen(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, project := range r.projects {
		if project.Name == name {
			return true
		}
	}
	return false
}

// Has reports whether the project ID is open.
func (r *ProjectRegistry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, found := r.projects[id]
	return found
}

// Snapshot returns a shallow copy of the registry map. The projects themselves
// are shared, matching the previous read semantics.
func (r *ProjectRegistry) Snapshot() map[string]*NetemProject {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := make(map[string]*NetemProject, len(r.projects))
	for id, project := range r.projects {
		snapshot[id] = project
	}
	return snapshot
}

// Get returns the open project for id, or nil.
func (r *ProjectRegistry) Get(id string) *NetemProject {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.projects[id]
}

// Add registers an existing project. It is used by tests and by code that
// builds a project outside Open.
func (r *ProjectRegistry) Add(project *NetemProject) error {
	if project == nil {
		return fmt.Errorf("project is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.projects[project.Id]; found {
		return fmt.Errorf("project id %s is already open", project.Id)
	}
	if r.projects == nil {
		r.projects = make(map[string]*NetemProject)
	}
	r.projects[project.Id] = project
	return nil
}

// Remove forgets the project without closing its topology or directory.
func (r *ProjectRegistry) Remove(id string) {
	r.mu.Lock()
	delete(r.projects, id)
	r.mu.Unlock()
}

// Open unpacks the archive, loads the topology and registers the project.
func (r *ProjectRegistry) Open(id, name string, data []byte) (*NetemProject, error) {
	r.opsMu.Lock()
	defer r.opsMu.Unlock()

	if r.IsNameOpen(name) {
		return nil, fmt.Errorf("a project named %s is already open", name)
	}

	dir, err := os.MkdirTemp(r.config.Workdir, "gonetem-"+id+"-")
	if err != nil {
		return nil, fmt.Errorf("unable to create temp folder for project: %w", err)
	}

	if err := utils.OpenArchive(dir, bytes.NewReader(data)); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("unable to open project: %w", err)
	}

	topology, err := LoadTopology(id, dir, r.config, r.factory)
	if err != nil {
		_ = topology.Close(nil)
		_ = os.RemoveAll(dir)
		return nil, err
	}

	project := &NetemProject{
		Id:       id,
		Name:     name,
		Dir:      dir,
		OpenAt:   time.Now(),
		Topology: topology,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, found := r.projects[id]; found {
		_ = topology.Close(nil)
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("project id %s is already open", id)
	}
	r.projects[id] = project

	return project, nil
}

// Save writes the project archive after synchronising node configuration.
func (r *ProjectRegistry) Save(id string, progressCh chan TopologySaveProgressT) (*bytes.Buffer, error) {
	r.opsMu.Lock()
	defer r.opsMu.Unlock()

	project := r.Get(id)
	if project == nil {
		return nil, &ProjectNotFoundError{id}
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

// GetNodeConfigs saves the project and returns the config-directory archive.
func (r *ProjectRegistry) GetNodeConfigs(id string) (*bytes.Buffer, error) {
	r.opsMu.Lock()
	defer r.opsMu.Unlock()

	project := r.Get(id)
	if project == nil {
		return nil, &ProjectNotFoundError{id}
	}

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

// Close closes the topology, forgets the project and removes its temp dir.
func (r *ProjectRegistry) Close(id string, progressCh chan TopologyRunCloseProgressT) error {
	r.opsMu.Lock()
	defer r.opsMu.Unlock()

	project := r.Get(id)
	if project == nil {
		return &ProjectNotFoundError{id}
	}

	err := project.Topology.Close(progressCh)

	r.mu.Lock()
	delete(r.projects, id)
	r.mu.Unlock()

	_ = os.RemoveAll(project.Dir)
	return err
}
