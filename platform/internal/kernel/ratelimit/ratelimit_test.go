package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/ratelimit"
)

func TestBurstThenRefusePerKey(t *testing.T) {
	l := ratelimit.New(ratelimit.Config{Rate: 1, Interval: time.Hour, Burst: 3})
	t.Cleanup(func() { _ = l.Close() })
	ctx := context.Background()
	for i := range 3 {
		if !l.Allow(ctx, "tenant:a") {
			t.Fatalf("call %d refused within the burst", i+1)
		}
	}
	if l.Allow(ctx, "tenant:a") {
		t.Error("allowed past the burst")
	}
	if !l.Allow(ctx, "tenant:b") {
		t.Error("another key shares the first one's budget")
	}
}
