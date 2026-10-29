//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestIntegrationRestrictions: import and export jobs are their
// project's — not found outside a principal's scope, and left out of
// its lists — a tenant-wide TM or termbase job (whose file holds every
// project's) is no project-scoped principal's, and an assigned member
// neither imports, exports nor lists anything (RFC 0006 §3.3, §4.1).
func TestIntegrationRestrictions(t *testing.T) {
	h := newHarness(t)
	a := h.project(t, "a", false, []string{"de"}, map[string]string{"pay": "Pay"})
	b := h.project(t, "b", false, []string{"de"}, map[string]string{"other": "Other"})
	owner := h.owner()
	exportOf := func(p uuid.UUID) domain.Job {
		j, _, err := h.svc.CreateExport(owner, app.ExportRequest{ProjectID: &p, Format: "json"}, "")
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	ja, jb := exportOf(a), exportOf(b)
	tm, _, err := h.svc.CreateKnowledgeExport(owner, domain.KindTM, domain.Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	page := pagination.Page{Size: 10}
	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{a}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, a, uuid.New(), "de")

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: its project's export", func() error { _, err := h.svc.GetExport(scoped, ja.ID); return err }, "ok"},
		{"scoped: another project's export", func() error { _, err := h.svc.GetExport(scoped, jb.ID); return err }, "not found"},
		{"scoped: the tenant's TM export", func() error { _, err := h.svc.GetExport(scoped, tm.ID); return err }, "not found"},
		{"scoped: downloads another project's", func() error { _, _, err := h.svc.OpenExport(scoped, jb.ID); return err }, "not found"},
		{"scoped: cancels another project's", func() error { _, err := h.svc.Cancel(scoped, jb.ID, domain.Export); return err }, "not found"},
		{"scoped: exports another project", func() error {
			_, _, err := h.svc.CreateExport(scoped, app.ExportRequest{ProjectID: &b, Format: "json"}, "")
			return err
		}, "not found"},
		{"scoped: imports into another project", func() error {
			_, _, err := h.svc.CreateImport(scoped, app.ImportRequest{ProjectID: &b, Format: "json", Options: domain.Options{Locale: "de"}}, "")
			return err
		}, "not found"},
		{"scoped: exports the tenant's TM", func() error {
			_, _, err := h.svc.CreateKnowledgeExport(scoped, domain.KindTM, domain.Options{}, "")
			return err
		}, "denied"},

		{"assigned: exports", func() error {
			_, _, err := h.svc.CreateExport(vendor, app.ExportRequest{ProjectID: &a, Format: "json"}, "")
			return err
		}, "denied"},
		{"assigned: imports", func() error {
			_, _, err := h.svc.CreateImport(vendor, app.ImportRequest{ProjectID: &a, Format: "json", Options: domain.Options{Locale: "de"}}, "")
			return err
		}, "denied"},
		{"assigned: lists jobs", func() error {
			_, _, err := h.svc.ListJobs(vendor, app.JobFilter{Direction: domain.Export}, page)
			return err
		}, "denied"},
		{"assigned: reads a job", func() error { _, err := h.svc.GetExport(vendor, ja.ID); return err }, "denied"},
		// Another project's job, or the tenant's, is not there for them
		// — the answer for an id that does not exist — rather than a
		// refusal that says it is (§12.2's sweep found the 403).
		{"assigned: reads another project's job", func() error { _, err := h.svc.GetExport(vendor, jb.ID); return err }, "not found"},
		{"assigned: reads the tenant's TM export", func() error { _, err := h.svc.GetExport(vendor, tm.ID); return err }, "not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("lists", func(t *testing.T) {
		jobs, _, err := h.svc.ListJobs(scoped, app.JobFilter{Direction: domain.Export}, page)
		if err != nil || len(jobs) != 1 || jobs[0].ID != ja.ID {
			t.Errorf("scoped exports = %v, %v; want only %s", jobs, err, ja.ID)
		}
		all, _, err := h.svc.ListJobs(owner, app.JobFilter{Direction: domain.Export}, page)
		if err != nil || len(all) != 3 {
			t.Errorf("unrestricted exports = %d, %v; want 3", len(all), err)
		}
	})
}
