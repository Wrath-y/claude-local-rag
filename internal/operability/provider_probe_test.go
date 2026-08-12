package operability

import (
	"context"
	"errors"
	"testing"
	"time"
)

type providerProbeFake struct {
	calls int
	err   error
}

func (p *providerProbeFake) Probe(context.Context) error {
	p.calls++
	return p.err
}

func TestProviderStateCacheUsesShortTTLAndSafeLastKnownOutcome(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	probe := &providerProbeFake{}
	cache := ProviderStateCache{Name: "vector", Provider: "local", Model: "safe-model", Probe: probe, TTL: time.Minute, Now: func() time.Time { return now }}
	first := cache.Check(context.Background())
	second := cache.Check(context.Background())
	if probe.calls != 1 || first.State != CapabilityAvailable || second.CheckedAt == nil || second.Provider != "local" || second.Model != "safe-model" {
		t.Fatalf("calls=%d first=%+v second=%+v", probe.calls, first, second)
	}
	now = now.Add(time.Minute)
	probe.err = errors.New("provider response includes a secret")
	third := cache.Check(context.Background())
	if probe.calls != 2 || third.State != CapabilityDegraded || third.Reason != "PROVIDER_UNAVAILABLE" || third.CheckedAt == nil {
		t.Fatalf("calls=%d third=%+v", probe.calls, third)
	}
	cache.Observe(nil)
	fourth := cache.Check(context.Background())
	if probe.calls != 2 || fourth.State != CapabilityAvailable || fourth.Reason != "" {
		t.Fatalf("calls=%d fourth=%+v", probe.calls, fourth)
	}
	cache.Invalidate()
	_ = cache.Check(context.Background())
	if probe.calls != 3 {
		t.Fatalf("invalidate did not reprobe: calls=%d", probe.calls)
	}
}
