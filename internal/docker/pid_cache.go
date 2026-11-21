package docker

import "sync"

// pidCache memoizes a container PID. Docker is inspected again only after the
// cache is invalidated, which keeps route installation from taking the Docker
// client lock on every call. The zero value is ready for use.
type pidCache struct {
	mu  sync.RWMutex
	pid int
}

// get returns the cached PID, invoking resolve only when no PID is cached.
// Errors are not cached. Concurrent first callers may each resolve, as before;
// the first successful value wins.
func (c *pidCache) get(resolve func() (int, error)) (int, error) {
	c.mu.RLock()
	pid := c.pid
	c.mu.RUnlock()
	if pid > 0 {
		return pid, nil
	}

	pid, err := resolve()
	if err != nil {
		return -1, err
	}

	c.mu.Lock()
	if c.pid == 0 {
		c.pid = pid
	}
	c.mu.Unlock()
	return pid, nil
}

// cached reports the cached PID without resolving it.
func (c *pidCache) cached() (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pid, c.pid > 0
}

// invalidate drops the cached PID. It is called on every lifecycle transition
// that stops the container.
func (c *pidCache) invalidate() {
	c.mu.Lock()
	c.pid = 0
	c.mu.Unlock()
}
