package server

import (
	"errors"
	"os"
	"testing"

	"github.com/satcomprojects/gonetem/internal/options"
)

func TestProjectRegistryLifecycle(t *testing.T) {
	options.InitServerConfig()
	oldWorkdir := options.ServerConfig.Workdir
	options.ServerConfig.Workdir = t.TempDir()
	t.Cleanup(func() { options.ServerConfig.Workdir = oldWorkdir })

	registry := NewProjectRegistry()
	archive := createProjectArchive(t, namespaceNetwork)

	project, err := registry.Open("p1", "ProjectOne", archive)
	if err != nil {
		t.Fatalf("Open returned an error: %v", err)
	}
	if !registry.Has("p1") || !registry.IsNameOpen("ProjectOne") {
		t.Fatal("opened project is not registered")
	}
	if registry.Get("p1") != project {
		t.Fatal("Get returned a different project")
	}
	if snapshot := registry.Snapshot(); len(snapshot) != 1 || snapshot["p1"] != project {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	if _, err := registry.Open("p2", "ProjectOne", archive); err == nil {
		t.Fatal("Open accepted a duplicate project name")
	}
	if _, err := registry.Open("p1", "OtherName", archive); err == nil {
		t.Fatal("Open accepted a duplicate project ID")
	}

	data, err := registry.Save("p1", nil)
	if err != nil || data.Len() == 0 {
		t.Fatalf("Save returned %d bytes, err %v", data.Len(), err)
	}
	configs, err := registry.GetNodeConfigs("p1")
	if err != nil || configs.Len() == 0 {
		t.Fatalf("GetNodeConfigs returned %d bytes, err %v", configs.Len(), err)
	}

	dir := project.Dir
	if err := registry.Close("p1", nil); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if registry.Has("p1") {
		t.Fatal("closed project is still registered")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("project directory still exists: %v", err)
	}

	for name, fn := range map[string]func() error{
		"save":    func() error { _, err := registry.Save("missing", nil); return err },
		"configs": func() error { _, err := registry.GetNodeConfigs("missing"); return err },
		"close":   func() error { return registry.Close("missing", nil) },
	} {
		t.Run(name, func(t *testing.T) {
			err := fn()
			var notFound *ProjectNotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("error = %v, want ProjectNotFoundError", err)
			}
		})
	}
}

func TestProjectRegistryAddRemove(t *testing.T) {
	registry := NewProjectRegistry()
	project := &NetemProject{Id: "p1", Name: "one"}
	if err := registry.Add(project); err != nil {
		t.Fatalf("Add returned an error: %v", err)
	}
	if err := registry.Add(project); err == nil {
		t.Fatal("Add accepted a duplicate project")
	}
	if err := registry.Add(nil); err == nil {
		t.Fatal("Add accepted a nil project")
	}
	registry.Remove("p1")
	if registry.Has("p1") {
		t.Fatal("Remove did not remove the project")
	}
	if err := registry.Add(project); err != nil {
		t.Fatalf("Add after Remove returned an error: %v", err)
	}
}
