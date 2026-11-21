package ovs

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/satcomprojects/gonetem/internal/utils"
)

func TestRegistry_GetMissingReturnsNil(t *testing.T) {
	registry := NewRegistry()

	if instance := registry.Get("missing"); instance != nil {
		t.Fatalf("Get(missing) = %v, want nil", instance)
	}
}

func TestRegistry_CreateRegistersInstance(t *testing.T) {
	client := newFakeContainerAPI()
	registry := NewRegistry()

	instance, err := registry.Create(client, "prj1")
	if err != nil {
		t.Fatalf("Create returned an error: %v", err)
	}
	if instance == nil {
		t.Fatal("Create returned a nil instance")
	}
	if got := registry.Get("prj1"); got != instance {
		t.Fatalf("Get(prj1) = %v, want the created instance", got)
	}
	if instance.prjID != "prj1" {
		t.Fatalf("instance prjID = %q, want prj1", instance.prjID)
	}
	if instance.containerId != "cid" {
		t.Fatalf("instance containerId = %q, want cid", instance.containerId)
	}
	if instance.state != created {
		t.Fatalf("instance state = %d, want created", instance.state)
	}

	created := client.createdContainers()
	if len(created) != 1 {
		t.Fatalf("container created %d times, want 1", len(created))
	}
	if !strings.Contains(created[0], "prj1.ovs") {
		t.Fatalf("container name %q does not contain the project id", created[0])
	}
	if len(client.imageCheckCalls()) != 1 {
		t.Fatalf("image checked %d times, want 1", len(client.imageCheckCalls()))
	}
}

func TestRegistry_CreateRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Create(newFakeContainerAPI(), "prj1"); err != nil {
		t.Fatalf("first Create returned an error: %v", err)
	}

	duplicateClient := newFakeContainerAPI()
	_, err := registry.Create(duplicateClient, "prj1")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate Create error = %v, want already exists error", err)
	}
	if calls := duplicateClient.createdContainers(); len(calls) != 0 {
		t.Fatalf("duplicate Create reached Docker: %#v", calls)
	}
}

func TestRegistry_CreateRejectsNilClient(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Create(nil, "prj1")
	if err == nil || !strings.Contains(err.Error(), "docker client is required") {
		t.Fatalf("Create error = %v, want missing client error", err)
	}
	if instance := registry.Get("prj1"); instance != nil {
		t.Fatalf("Get(prj1) = %v after failed Create, want nil", instance)
	}
}

func TestRegistry_CreateRequiresImage(t *testing.T) {
	client := newFakeContainerAPI()
	client.imagePresent = false
	registry := NewRegistry()

	_, err := registry.Create(client, "prj1")
	if err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("Create error = %v, want missing image error", err)
	}
	if calls := client.createdContainers(); len(calls) != 0 {
		t.Fatalf("Create reached Docker without the image: %#v", calls)
	}
	if instance := registry.Get("prj1"); instance != nil {
		t.Fatalf("Get(prj1) = %v after failed Create, want nil", instance)
	}
}

func TestRegistry_CreatePropagatesClientErrors(t *testing.T) {
	imageErr := errors.New("image lookup failed")
	client := newFakeContainerAPI()
	client.imageErr = imageErr
	registry := NewRegistry()

	if _, err := registry.Create(client, "prj1"); !errors.Is(err, imageErr) {
		t.Fatalf("Create error = %v, want %v", err, imageErr)
	}

	createErr := errors.New("container create failed")
	client = newFakeContainerAPI()
	client.createErr = createErr
	if _, err := registry.Create(client, "prj2"); !errors.Is(err, createErr) {
		t.Fatalf("Create error = %v, want %v", err, createErr)
	}
	if instance := registry.Get("prj2"); instance != nil {
		t.Fatalf("Get(prj2) = %v after failed Create, want nil", instance)
	}
}

func TestRegistry_CloseMissingReturnsError(t *testing.T) {
	registry := NewRegistry()

	err := registry.Close("missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Close(missing) error = %v, want not found error", err)
	}
}

func TestRegistry_CloseRemovesEntryAndContainer(t *testing.T) {
	client := newFakeContainerAPI()
	registry := NewRegistry()
	if _, err := registry.Create(client, "prj1"); err != nil {
		t.Fatalf("Create returned an error: %v", err)
	}

	if err := registry.Close("prj1"); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if instance := registry.Get("prj1"); instance != nil {
		t.Fatalf("Get(prj1) = %v after Close, want nil", instance)
	}
	if removed := client.removedContainers(); len(removed) != 1 || removed[0] != "cid" {
		t.Fatalf("unexpected removed containers: %#v", removed)
	}
	if err := registry.Close("prj1"); err == nil {
		t.Fatal("second Close did not return an error")
	}
}

func TestRegistry_CloseForgetsEntryWhenRemoveFails(t *testing.T) {
	rmErr := errors.New("remove failed")
	client := newFakeContainerAPI()
	client.rmErr = rmErr
	registry := NewRegistry()
	if _, err := registry.Create(client, "prj1"); err != nil {
		t.Fatalf("Create returned an error: %v", err)
	}

	if err := registry.Close("prj1"); !errors.Is(err, rmErr) {
		t.Fatalf("Close error = %v, want %v", err, rmErr)
	}
	if instance := registry.Get("prj1"); instance != nil {
		t.Fatalf("Get(prj1) = %v after failed Close, want nil", instance)
	}
}

func TestRegistry_CreateAfterCloseSucceeds(t *testing.T) {
	registry := NewRegistry()
	first, err := registry.Create(newFakeContainerAPI(), "prj1")
	if err != nil {
		t.Fatalf("first Create returned an error: %v", err)
	}
	if err := registry.Close("prj1"); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}

	second, err := registry.Create(newFakeContainerAPI(), "prj1")
	if err != nil {
		t.Fatalf("Create after Close returned an error: %v", err)
	}
	if second == first {
		t.Fatal("Create after Close reused the closed instance")
	}
	if got := registry.Get("prj1"); got != second {
		t.Fatalf("Get(prj1) = %v, want the second instance", got)
	}
}

func TestRegistry_ConcurrentCreateSameProjectOnlyOneSucceeds(t *testing.T) {
	const count = 8
	registry := NewRegistry()

	results := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := registry.Create(newFakeContainerAPI(), "prj1")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	created, duplicates := 0, 0
	for err := range results {
		if err == nil {
			created++
			continue
		}
		duplicates++
	}
	if created != 1 {
		t.Fatalf("Create succeeded %d times, want 1", created)
	}
	if duplicates != count-1 {
		t.Fatalf("Create reported %d duplicates, want %d", duplicates, count-1)
	}
	if instance := registry.Get("prj1"); instance == nil {
		t.Fatal("Get(prj1) returned nil after concurrent Create")
	}
}

func TestRegistry_ConcurrentIndependentProjects(t *testing.T) {
	const count = 20
	registry := NewRegistry()

	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prjID := fmt.Sprintf("prj%d", i)
			if _, err := registry.Create(newFakeContainerAPI(), prjID); err != nil {
				t.Errorf("Create(%s) returned an error: %v", prjID, err)
				return
			}
			if registry.Get(prjID) == nil {
				t.Errorf("Get(%s) returned nil", prjID)
			}
		}(i)
	}
	wg.Wait()
}

func TestPackageLevelRegistryWrappers(t *testing.T) {
	prjID := "wrap-" + utils.RandString(8)
	t.Cleanup(func() { _ = CloseOvsInstance(prjID) })

	client := newFakeContainerAPI()
	instance, err := NewOvsInstance(client, prjID)
	if err != nil {
		t.Fatalf("NewOvsInstance returned an error: %v", err)
	}
	if got := GetOvsInstance(prjID); got != instance {
		t.Fatalf("GetOvsInstance = %v, want the created instance", got)
	}
	if err := CloseOvsInstance(prjID); err != nil {
		t.Fatalf("CloseOvsInstance returned an error: %v", err)
	}
	if got := GetOvsInstance(prjID); got != nil {
		t.Fatalf("GetOvsInstance = %v after close, want nil", got)
	}
	if err := CloseOvsInstance(prjID); err == nil {
		t.Fatal("second CloseOvsInstance did not return an error")
	}
}
