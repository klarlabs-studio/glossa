//go:build integration

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The §10.1 gauges are counted across tenants in the timer sweep's
// system scope, which migrations 0043 and 0055 open to statuses and due
// dates only.
func TestTheWorkloadIsCountedAcrossTenants(t *testing.T) {
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := newWorkHarness(t, mustTenant(t, "workload-a"))
	b := newWorkHarness(t, mustTenant(t, "workload-b"))
	due := func(h *workHarness, in time.Duration) {
		t.Helper()
		at := h.clock.Add(in)
		if _, _, err := h.svc.Assign(h.manager(), app.AssignInput{
			ProjectID: uuid.New(), Units: []domain.Unit{unitIn("de")}, To: domain.Party{Role: "translator"}, DueAt: &at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	due(a, time.Hour)
	due(a, 48*time.Hour)
	due(b, 2*time.Hour)
	b.assign(uuid.New(), domain.Party{Role: "translator"}, unitIn("fr")) // no due date: never overdue

	scanner := postgres.NewInstances(a.uow)
	overdue, onTime, err := scanner.LiveAssignments(context.Background(), a.clock.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if overdue != 2 || onTime != 2 {
		t.Errorf("live assignments: %d overdue, %d on time; want 2 and 2", overdue, onTime)
	}
	byStatus, err := scanner.InstancesByStatus(context.Background())
	if err != nil || len(byStatus) != 0 {
		t.Errorf("instances by status = %v, %v; want none", byStatus, err)
	}
}
