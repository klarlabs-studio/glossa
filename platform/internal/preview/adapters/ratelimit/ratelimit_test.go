package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/preview/adapters/ratelimit"
)

func TestBurstThenRefusePerKey(t *testing.T) {
	l := ratelimit.New(ratelimit.Config{Rate: 1, Interval: time.Hour, Burst: 3})
	t.Cleanup(func() { _ = l.Close() })
	ctx := context.Background()
	for i := range 3 {
		if !l.Allow(ctx, "person:a") {
			t.Fatalf("request %d refused within the burst", i+1)
		}
	}
	if l.Allow(ctx, "person:a") {
		t.Error("allowed past the burst")
	}
	if !l.Allow(ctx, "token:b") {
		t.Error("another caller shares the first one's budget")
	}
}

func TestDefaultsArePreviewLimits(t *testing.T) {
	cfg := ratelimit.Default()
	if cfg.Rate != 10 || cfg.Interval != time.Second || cfg.Burst != 120 {
		t.Errorf("defaults = %+v", cfg)
	}
}
