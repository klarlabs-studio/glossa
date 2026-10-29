//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

func outcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, authz.ErrNotVisible), errors.Is(err, app.ErrNotFound), errors.Is(err, app.ErrProjectNotFound),
		errors.Is(err, app.ErrMessageNotFound), errors.Is(err, app.ErrCaptureNotFound):
		return "not found"
	case errors.Is(err, authz.ErrForbidden):
		return "denied"
	}
	return err.Error()
}

// TestContextRestrictions: usages and builds are not found outside a
// principal's project scope, and an assigned member reads the usages of
// the messages their units are in — what translating them needs (RFC
// 0006 §3.3) — and nothing project-wide.
func TestContextRestrictions(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "brotwerk", []string{"web"}, "checkout.pay", "checkout.cancel")
	if _, err := h.svc.IngestUsages(h.ci(), app.IngestUsages{
		Project: f.project, Source: "plugin",
		Document: document("web", "abc", "main", use{"checkout.pay", "src/Pay.tsx", 3}, use{"checkout.cancel", "src/Cancel.tsx", 4}),
	}); err != nil {
		t.Fatal(err)
	}
	h.drain(t)

	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{uuid.New()}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, f.project, f.ids["checkout.pay"], "de")

	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: a message's usages out of scope", func() error {
			_, err := h.svc.UsagesOfKey(scoped, f.project, "checkout.pay", app.UsageQuery{})
			return err
		}, "not found"},
		{"scoped: builds out of scope", func() error {
			_, _, err := h.svc.ListBuilds(scoped, f.project, app.BuildQuery{}, pagination.Page{Size: 10})
			return err
		}, "not found"},
		{"scoped: upload out of scope", func() error {
			_, err := h.svc.IngestUsages(authztest.ScopedToken(context.Background(), h.tenant, []uuid.UUID{uuid.New()}, "write"),
				app.IngestUsages{Project: f.project, Source: "plugin", Document: document("web", "def", "main")})
			return err
		}, "not found"},

		{"assigned: a covered message's usages", func() error {
			_, err := h.svc.UsagesOfKey(vendor, f.project, "checkout.pay", app.UsageQuery{})
			return err
		}, "ok"},
		{"assigned: an uncovered message's usages", func() error {
			_, err := h.svc.UsagesOfKey(vendor, f.project, "checkout.cancel", app.UsageQuery{})
			return err
		}, "not found"},
		{"assigned: an uncovered message by id", func() error {
			_, err := h.svc.MessageUsages(vendor, f.project, f.ids["checkout.cancel"], app.UsageQuery{})
			return err
		}, "not found"},
		{"assigned: a covered message's captures", func() error {
			_, err := h.svc.CapturesOfKey(vendor, f.project, "checkout.pay", app.UsageQuery{})
			return err
		}, "ok"},
		{"assigned: an uncovered message's captures", func() error {
			_, err := h.svc.CapturesOfKey(vendor, f.project, "checkout.cancel", app.UsageQuery{})
			return err
		}, "not found"},
		{"assigned: every usage in the project", func() error {
			_, err := h.svc.ListUsages(vendor, f.project, "", app.UsageFilter{}, pagination.Page{Size: 10})
			return err
		}, "denied"},
		{"assigned: the builds", func() error {
			_, _, err := h.svc.ListBuilds(vendor, f.project, app.BuildQuery{}, pagination.Page{Size: 10})
			return err
		}, "denied"},
		{"assigned: unused messages", func() error { _, err := h.svc.UnusedMessages(vendor, f.project, ""); return err }, "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcome(tc.call()); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
