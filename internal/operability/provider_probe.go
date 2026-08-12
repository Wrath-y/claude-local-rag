package operability

import (
	"context"
	"sync"
	"time"
)

// ProviderProbe is an optional, capability-only probe. It must not submit
// content to an embedding, rerank, or LLM endpoint.
type ProviderProbe interface {
	Probe(context.Context) error
}

// ProviderStateCache retains the last safe provider outcome for a short TTL.
// It keeps health polling bounded while Observe lets real provider work update
// the status immediately instead of waiting for the cache to expire.
type ProviderStateCache struct {
	Name     string
	Provider string
	Model    string
	Probe    ProviderProbe
	TTL      time.Duration
	Timeout  time.Duration
	Now      func() time.Time

	mu    sync.Mutex
	valid bool
	last  Dependency
}

func (c *ProviderStateCache) Check(ctx context.Context) Dependency {
	now := c.now()
	c.mu.Lock()
	if c.valid && now.Sub(*c.last.CheckedAt) < c.ttl() {
		result := c.last
		c.mu.Unlock()
		return result
	}
	c.mu.Unlock()

	result := c.dependency(CapabilityAvailable, "", now)
	if c.Probe != nil {
		probeCtx, cancel := context.WithTimeout(ctx, c.timeout())
		err := c.Probe.Probe(probeCtx)
		cancel()
		if err != nil {
			result = c.dependency(CapabilityDegraded, "PROVIDER_UNAVAILABLE", now)
		}
	}
	c.store(result)
	return result
}

// Observe records a real provider outcome, invalidating any contrary cached
// probe state at the exact point the provider is known to have changed.
func (c *ProviderStateCache) Observe(err error) {
	state, reason := CapabilityAvailable, ""
	if err != nil {
		state, reason = CapabilityDegraded, "PROVIDER_UNAVAILABLE"
	}
	c.store(c.dependency(state, reason, c.now()))
}

func (c *ProviderStateCache) Invalidate() {
	c.mu.Lock()
	c.valid = false
	c.mu.Unlock()
}

func (c *ProviderStateCache) store(value Dependency) {
	c.mu.Lock()
	c.last, c.valid = value, true
	c.mu.Unlock()
}

func (c *ProviderStateCache) dependency(state CapabilityState, reason string, checkedAt time.Time) Dependency {
	return Dependency{Name: c.Name, State: state, Reason: reason, Provider: c.Provider, Model: c.Model, CheckedAt: &checkedAt}
}

func (c *ProviderStateCache) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *ProviderStateCache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return 15 * time.Second
}

func (c *ProviderStateCache) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 2 * time.Second
}
