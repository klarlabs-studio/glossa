//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
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

// TestIntelligenceRestrictions: AI fills, jobs and suggestions are their
// project's — not found outside a principal's scope and left out of its
// lists — and an assigned member reads and requests none of them (RFC
// 0006 §3.3, §4.1). No provider is called: consent is off and the
// provider refuses.
func TestIntelligenceRestrictions(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0)
	p := w.project(t, []string{"de"}, map[string]string{"a": "Apple"})
	fill, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	page := pagination.Page{Size: 10}
	jobs, _, err := w.svc.ListJobs(w.developer(), app.JobFilter{}, page)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("jobs = %v, %v", jobs, err)
	}
	scoped := authztest.ScopedMember(context.Background(), w.tenant, []uuid.UUID{uuid.New()}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), w.tenant, cov, "de")
	apple, err := w.catalog.MessagesByKeys(w.developer(), catalogdomain.ProjectID(p), []string{"a"})
	if err != nil || len(apple) != 1 {
		t.Fatalf("message a = %v, %v", apple, err)
	}
	cov.Assign(member, p, apple["a"].ID.UUID(), "de")
	elsewhereCov := &authztest.Coverage{}
	elsewhere, other := authztest.Assigned(context.Background(), w.tenant, elsewhereCov, "de")
	elsewhereCov.Assign(other, uuid.New(), uuid.New(), "de")

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: a fill out of scope", func() error { _, err := w.svc.GetFill(scoped, fill.Fill.ID); return err }, "not found"},
		{"scoped: a job out of scope", func() error { _, err := w.svc.GetJob(scoped, jobs[0].ID); return err }, "not found"},
		{"scoped: cancels a job out of scope", func() error { _, err := w.svc.CancelJob(scoped, jobs[0].ID); return err }, "not found"},
		{"scoped: fills a project out of scope", func() error {
			_, _, err := w.svc.RequestFill(scoped, p, app.FillRequest{Locales: []string{"de"}}, "")
			return err
		}, "not found"},
		{"scoped: the review queue out of scope", func() error { _, _, err := w.svc.ReviewQueue(scoped, p, nil, page); return err }, "not found"},
		{"scoped: project settings out of scope", func() error { _, err := w.svc.GetProjectSettings(scoped, p); return err }, "not found"},

		// Read by its id, a fill or job of a project no assignment of
		// theirs is in is not there — the answer for an id that does not
		// exist — rather than a refusal that says it is (§12.2's sweep
		// found the 403); in a project they work in, it is refused.
		{"assigned elsewhere: a fill", func() error { _, err := w.svc.GetFill(elsewhere, fill.Fill.ID); return err }, "not found"},
		{"assigned elsewhere: a job", func() error { _, err := w.svc.GetJob(elsewhere, jobs[0].ID); return err }, "not found"},
		{"assigned: a fill of their project", func() error { _, err := w.svc.GetFill(vendor, fill.Fill.ID); return err }, "denied"},
		{"assigned: a job of their project", func() error { _, err := w.svc.GetJob(vendor, jobs[0].ID); return err }, "denied"},
		{"assigned: lists jobs", func() error { _, _, err := w.svc.ListJobs(vendor, app.JobFilter{}, page); return err }, "denied"},
		{"assigned: suggestions of a covered unit", func() error {
			rows, err := w.svc.UnitSuggestions(vendor, p, "a", bcp47.MustParse("de"))
			for _, r := range rows {
				if r.Decidable {
					return errors.New("a vendor may decide a suggestion")
				}
			}
			return err
		}, "ok"},
		{"assigned: suggestions of the message in another locale", func() error {
			_, err := w.svc.UnitSuggestions(vendor, p, "a", bcp47.MustParse("fr"))
			return err
		}, "not found"},
		{"assigned: suggestions of another message", func() error {
			_, err := w.svc.UnitSuggestions(vendor, p, "nope", bcp47.MustParse("de"))
			return err
		}, "not found"},
		{"assigned elsewhere: suggestions of a unit in a project they are not in", func() error {
			_, err := w.svc.UnitSuggestions(elsewhere, p, "a", bcp47.MustParse("de"))
			return err
		}, "not found"},
		{"scoped: suggestions of a unit out of scope", func() error {
			_, err := w.svc.UnitSuggestions(scoped, p, "a", bcp47.MustParse("de"))
			return err
		}, "not found"},
		{"developer: suggestions of a unit", func() error {
			rows, err := w.svc.UnitSuggestions(w.developer(), p, "a", bcp47.MustParse("de"))
			for _, r := range rows {
				if !r.Decidable {
					return errors.New("a developer may decide a suggestion")
				}
			}
			return err
		}, "ok"},
		{"assigned: lists suggestions", func() error {
			_, _, err := w.svc.ListSuggestions(vendor, app.SuggestionFilter{}, page)
			return err
		}, "denied"},
		{"assigned: the review queue", func() error { _, _, err := w.svc.ReviewQueue(vendor, p, nil, page); return err }, "denied"},
		{"assigned: requests a fill", func() error {
			_, _, err := w.svc.RequestFill(vendor, p, app.FillRequest{Locales: []string{"de"}}, "")
			return err
		}, "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("lists", func(t *testing.T) {
		got, _, err := w.svc.ListJobs(scoped, app.JobFilter{}, page)
		if err != nil || len(got) != 0 {
			t.Errorf("a project-scoped job list holds %d jobs of another project (%v)", len(got), err)
		}
		in := authztest.ScopedMember(context.Background(), w.tenant, []uuid.UUID{p}, []string{"developer"})
		got, _, err = w.svc.ListJobs(in, app.JobFilter{}, page)
		if err != nil || len(got) != len(jobs) {
			t.Errorf("jobs in scope = %d, %v; want %d", len(got), err, len(jobs))
		}
	})
}
