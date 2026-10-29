//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
)

// outcome names how a restricted call ended: a thing outside the
// caller's restriction must be "not found", exactly as one that does
// not exist (RFC 0006 §3.3, §4.1).
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

// TestCatalogRestrictions: a project outside a principal's scope is not
// found, and an assigned member sees the projects and messages their
// units are in and nothing else.
func TestCatalogRestrictions(t *testing.T) {
	h := newHarness(t)
	dev := h.developer()
	a := h.project(t, dev)
	b, _, err := h.svc.CreateProject(dev, app.NewProject{Slug: "other", Name: "Other", SourceLocale: "en"}, "")
	if err != nil {
		t.Fatal(err)
	}
	covered, _, err := h.svc.CreateMessage(dev, a.ID, app.NewMessage{Key: "checkout.pay", Text: "Pay"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateMessage(dev, a.ID, app.NewMessage{Key: "checkout.cancel", Text: "Cancel"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateMessage(dev, b.ID, app.NewMessage{Key: "elsewhere", Text: "Elsewhere"}, ""); err != nil {
		t.Fatal(err)
	}

	scoped := authztest.ScopedMember(context.Background(), h.tenant, []uuid.UUID{a.ID.UUID()}, []string{"developer"})
	cov := &authztest.Coverage{}
	vendor, member := authztest.Assigned(context.Background(), h.tenant, cov, "de")
	cov.Assign(member, a.ID.UUID(), covered.ID.UUID(), "de")

	keys := func(ms []domain.Message) []string {
		var out []string
		for _, m := range ms {
			out = append(out, string(m.Key))
		}
		return out
	}
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"scoped: get a project in scope", func() error { _, err := h.svc.GetProject(scoped, a.ID); return err }, "ok"},
		{"scoped: get a project out of scope", func() error { _, err := h.svc.GetProject(scoped, b.ID); return err }, "not found"},
		{"scoped: list messages out of scope", func() error {
			_, _, err := h.svc.ListMessages(scoped, b.ID, app.MessageQuery{}, firstPage())
			return err
		}, "not found"},
		{"scoped: get a message out of scope", func() error { _, err := h.svc.GetMessage(scoped, b.ID, "elsewhere"); return err }, "not found"},
		{"scoped: write out of scope", func() error {
			_, _, err := h.svc.CreateMessage(scoped, b.ID, app.NewMessage{Key: "x", Text: "x"}, "")
			return err
		}, "not found"},
		{"scoped: update a project out of scope", func() error { _, err := h.svc.UpdateProject(scoped, b.ID, 1, domain.ProjectChange{}); return err }, "not found"},
		{"scoped: create a project", func() error {
			_, _, err := h.svc.CreateProject(scoped, app.NewProject{Slug: "new", Name: "New", SourceLocale: "en"}, "")
			return err
		}, "denied"},
		{"scoped: branches out of scope", func() error {
			_, _, err := h.svc.ListBranches(scoped, b.ID, app.BranchFilter{}, firstPage())
			return err
		}, "not found"},

		{"assigned: the project they work in", func() error { _, err := h.svc.GetProject(vendor, a.ID); return err }, "ok"},
		{"assigned: a project they don't", func() error { _, err := h.svc.GetProject(vendor, b.ID); return err }, "not found"},
		{"assigned: a covered message", func() error { _, err := h.svc.GetMessage(vendor, a.ID, "checkout.pay"); return err }, "ok"},
		{"assigned: an uncovered message", func() error { _, err := h.svc.GetMessage(vendor, a.ID, "checkout.cancel"); return err }, "not found"},
		{"assigned: a message that does not exist", func() error { _, err := h.svc.GetMessage(vendor, a.ID, "nope"); return err }, "not found"},
		{"assigned: an uncovered message's history", func() error {
			_, _, err := h.svc.SourceRevisions(vendor, a.ID, "checkout.cancel", firstPage())
			return err
		}, "not found"},
		{"assigned: the namespaces' counts", func() error { _, _, err := h.svc.ListNamespaces(vendor, a.ID, firstPage()); return err }, "denied"},
		{"assigned: writing source", func() error {
			_, _, err := h.svc.CreateMessage(vendor, a.ID, app.NewMessage{Key: "x", Text: "x"}, "")
			return err
		}, "denied"},
		{"assigned: the release snapshot", func() error { _, err := h.svc.ReleaseSource(vendor, a.ID); return err }, "denied"},
		{"assigned: missing_in over the project", func() error {
			_, _, err := h.svc.ListMessages(vendor, a.ID, app.MessageQuery{MissingIn: "de"}, firstPage())
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
		ps, _, err := h.svc.ListProjects(scoped, firstPage())
		if err != nil || len(ps) != 1 || ps[0].ID != a.ID {
			t.Errorf("scoped project list = %v, %v; want only %s", ps, err, a.ID)
		}
		ps, _, err = h.svc.ListProjects(vendor, firstPage())
		if err != nil || len(ps) != 1 || ps[0].ID != a.ID {
			t.Errorf("assigned project list = %v, %v; want only %s", ps, err, a.ID)
		}
		ps, _, err = h.svc.ListProjects(dev, firstPage())
		if err != nil || len(ps) != 2 {
			t.Errorf("unrestricted project list = %d, %v; want both", len(ps), err)
		}
		ms, _, err := h.svc.ListMessages(vendor, a.ID, app.MessageQuery{}, firstPage())
		if err != nil || !slices.Equal(keys(ms), []string{"checkout.pay"}) {
			t.Errorf("assigned message list = %v, %v", keys(ms), err)
		}
		ms, _, err = h.svc.ListMessages(dev, a.ID, app.MessageQuery{}, firstPage())
		if err != nil || len(ms) != 2 {
			t.Errorf("unrestricted message list = %v, %v", keys(ms), err)
		}
		found, err := h.svc.MessagesByKeys(vendor, a.ID, []string{"checkout.pay", "checkout.cancel"})
		if err != nil || len(found) != 1 || found["checkout.pay"].ID != covered.ID {
			t.Errorf("assigned MessagesByKeys = %v, %v", found, err)
		}
	})
}
