//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound), errors.Is(err, app.ErrProjectNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestQualityRestrictions: every Quality read and write of a project
// outside a principal's scope is not found, and an assigned member —
// whose work is units, not the project's quality — reads none of it
// (RFC 0006 §3.3, §4.1).
func TestQualityRestrictions(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "brotwerk")
	page := pagination.Page{Size: 10}
	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{uuid.New()}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, p, uuid.New(), "de")
	inScope := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{p}, []string{"developer"})

	calls := map[string]func(context.Context) error{
		"findings": func(ctx context.Context) error {
			_, err := h.svc.ListFindings(ctx, p, app.FindingQuery{}, page)
			return err
		},
		"runs": func(ctx context.Context) error {
			_, _, err := h.svc.ListCheckRuns(ctx, p, app.RunFilter{}, page)
			return err
		},
		"run":      func(ctx context.Context) error { _, err := h.svc.GetCheckRun(ctx, p, uuid.New()); return err },
		"policy":   func(ctx context.Context) error { _, err := h.svc.CheckPolicy(ctx, p); return err },
		"versions": func(ctx context.Context) error { _, _, err := h.svc.ListPolicyVersions(ctx, p, page); return err },
		"waivers": func(ctx context.Context) error {
			_, _, err := h.svc.ListWaivers(ctx, p, app.WaiverFilter{}, page)
			return err
		},
		"summary": func(ctx context.Context) error {
			_, err := h.svc.QualitySummary(ctx, p, app.SummaryQuery{})
			return err
		},
		"jobs": func(ctx context.Context) error { _, _, err := h.svc.ListLinguisticJobs(ctx, p, "", page); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if got := outcome(call(scoped)); got != "not found" {
				t.Errorf("out of scope: got %s, want not found", got)
			}
			if got := outcome(call(vendor)); got != "denied" {
				t.Errorf("assigned: got %s, want denied", got)
			}
			if got := outcome(call(inScope)); got == "not found" && name != "run" || got == "denied" {
				t.Errorf("in scope: got %s", got)
			}
		})
	}
}
