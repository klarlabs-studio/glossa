package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

func TestPublishRequestDebounces(t *testing.T) {
	t0 := time.Date(2026, 9, 19, 8, 0, 0, 0, time.UTC)
	r := domain.NewPublishRequest(uuid.New(), "pr-42", "system:ci", t0)
	if r.NotBefore != t0.Add(30*time.Second) || r.FirstRequestedAt != t0 || r.Due(t0.Add(29*time.Second)) || !r.Due(t0.Add(30*time.Second)) {
		t.Fatalf("%+v", r)
	}
	first := r.ID
	r.Renew("system:ci", t0.Add(20*time.Second))
	if r.NotBefore != t0.Add(50*time.Second) || r.ID == first || r.FirstRequestedAt != t0 {
		t.Errorf("renewed: %+v", r)
	}
	// A branch that keeps changing still publishes within MaxPublishDelay.
	for s := 40; s <= 600; s += 20 {
		r.Renew("system:ci", t0.Add(time.Duration(s)*time.Second))
	}
	if r.NotBefore != t0.Add(domain.MaxPublishDelay) {
		t.Errorf("capped: %v", r.NotBefore.Sub(t0))
	}
}
