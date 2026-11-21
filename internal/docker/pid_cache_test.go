package docker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPidCacheResolvesOnce(t *testing.T) {
	var cache pidCache
	calls := 0
	resolve := func() (int, error) {
		calls++
		return 4242, nil
	}

	for i := 0; i < 3; i++ {
		pid, err := cache.get(resolve)
		if err != nil {
			t.Fatalf("get returned an error: %v", err)
		}
		if pid != 4242 {
			t.Fatalf("get returned pid %d, want 4242", pid)
		}
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times, want 1", calls)
	}
}

func TestPidCacheDoesNotCacheErrors(t *testing.T) {
	var cache pidCache
	wantErr := errors.New("inspect failed")

	pid, err := cache.get(func() (int, error) { return -1, wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("get error = %v, want %v", err, wantErr)
	}
	if pid != -1 {
		t.Fatalf("get returned pid %d on error, want -1", pid)
	}

	calls := 0
	pid, err = cache.get(func() (int, error) {
		calls++
		return 777, nil
	})
	if err != nil {
		t.Fatalf("retry returned an error: %v", err)
	}
	if pid != 777 {
		t.Fatalf("retry returned pid %d, want 777", pid)
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times after a failure, want 1", calls)
	}
}

func TestPidCacheCachedAndInvalidate(t *testing.T) {
	var cache pidCache

	if pid, ok := cache.cached(); ok || pid != 0 {
		t.Fatalf("zero cache reported pid=%d ok=%v", pid, ok)
	}

	if _, err := cache.get(func() (int, error) { return 4242, nil }); err != nil {
		t.Fatalf("get returned an error: %v", err)
	}
	if pid, ok := cache.cached(); !ok || pid != 4242 {
		t.Fatalf("cached() = %d,%v, want 4242,true", pid, ok)
	}

	cache.invalidate()
	if pid, ok := cache.cached(); ok || pid != 0 {
		t.Fatalf("cache after invalidate reported pid=%d ok=%v", pid, ok)
	}

	calls := 0
	pid, err := cache.get(func() (int, error) {
		calls++
		return 555, nil
	})
	if err != nil {
		t.Fatalf("get after invalidate returned an error: %v", err)
	}
	if pid != 555 || calls != 1 {
		t.Fatalf("get after invalidate = pid %d, calls %d; want 555, 1", pid, calls)
	}
}

func TestPidCacheConcurrentAccess(t *testing.T) {
	var cache pidCache
	var calls atomic.Int32
	resolve := func() (int, error) {
		calls.Add(1)
		return 4242, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pid, err := cache.get(resolve)
			if err != nil {
				t.Errorf("get returned an error: %v", err)
				return
			}
			if pid != 4242 {
				t.Errorf("get returned pid %d, want 4242", pid)
			}
		}()
	}
	wg.Wait()

	pid, ok := cache.cached()
	if !ok || pid != 4242 {
		t.Fatalf("cache after concurrent access = %d,%v, want 4242,true", pid, ok)
	}

	// Once warm, the cache must never call the resolver again.
	before := calls.Load()
	if _, err := cache.get(func() (int, error) {
		t.Error("resolver called with a warm cache")
		return 0, nil
	}); err != nil {
		t.Fatalf("get with a warm cache returned an error: %v", err)
	}
	if after := calls.Load(); after != before {
		t.Fatalf("resolver calls grew from %d to %d with a warm cache", before, after)
	}
}

func TestPidCacheConcurrentInvalidate(t *testing.T) {
	var cache pidCache

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = cache.get(func() (int, error) { return 4242, nil })
		}()
		go func() {
			defer wg.Done()
			cache.invalidate()
		}()
	}
	wg.Wait()
}
