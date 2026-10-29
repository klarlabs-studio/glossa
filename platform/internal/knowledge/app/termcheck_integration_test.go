//go:build integration

package app_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	kdomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// A project check runs terminology QA over every translation server-side
// and reports findings by message key, a page at a time, with how many
// it checked per locale.
func TestProjectTerminologyCheck(t *testing.T) {
	h := newHarness(t)
	messages := map[string]string{"a.open": "Open your workspace", "b.name": "Name the workspace {name}"}
	for i := range 5 {
		messages[fmt.Sprintf("c.plain%d", i)] = fmt.Sprintf("Plain text %d", i)
	}
	p := h.project(t, "shop", false, []string{"de", "fr"}, messages)
	if _, _, err := h.svc.CreateConcept(h.developer(), nil, workspace(), ""); err != nil {
		t.Fatal(err)
	}
	h.translate(t, p, "a.open", "de", "Öffne deinen Workspace", nil)             // forbidden term, term missing
	h.translate(t, p, "b.name", "de", "Benenne den Arbeitsbereich {name}", nil)  // clean
	h.translate(t, p, "a.open", "fr", "Ouvre ton espace", ptr("draft"))          // no fr terms: clean
	h.translate(t, p, "c.plain0", "de", "Klartext Workspace", ptr("draft"))      // forbidden term
	h.translate(t, p, "b.name", "fr", "Nomme le workspace {name}", ptr("draft")) // fr has no terms
	for i := 1; i < 5; i++ {
		h.translate(t, p, fmt.Sprintf("c.plain%d", i), "de", fmt.Sprintf("Klartext %d", i), nil)
	}
	h.drain(t)

	all := check(t, h, p, app.ProjectTermCheck{Locales: []bcp47.Tag{tag("de"), tag("fr")}, Page: pagination.Page{Size: 100}})
	if len(all.Items) != 2 || all.Next != nil || all.Checked["de"] != 7 || all.Checked["fr"] != 2 || all.Items[1].Key != "c.plain0" {
		t.Fatalf("report = %+v", all)
	}
	it := all.Items[0]
	if it.Key != "a.open" || it.Locale.String() != "de" || it.Namespace != "default" || it.State != "approved" ||
		it.SourceText != "Open your workspace" || len(it.Findings) != 2 ||
		it.Findings[0].Code != kdomain.FindingTermMissing || it.Findings[1].Code != kdomain.FindingTermForbidden {
		t.Errorf("item = %+v", it)
	}

	// Pages scan page_size translations; summing them gives the totals.
	var (
		checked, found int
		pages          int
		page           = pagination.Page{Size: 2}
	)
	for {
		r := check(t, h, p, app.ProjectTermCheck{Locales: []bcp47.Tag{tag("de"), tag("fr")}, Page: page})
		pages++
		for _, n := range r.Checked {
			checked += n
		}
		found += len(r.Items)
		if r.Next == nil {
			break
		}
		size := 2
		var err error
		if page, err = pagination.Parse(&size, r.Next); err != nil {
			t.Fatal(err)
		}
	}
	if checked != 9 || found != 2 || pages != 5 {
		t.Errorf("paged: checked %d, found %d in %d pages", checked, found, pages)
	}

	// States narrow it.
	drafts := check(t, h, p, app.ProjectTermCheck{Locales: []bcp47.Tag{tag("de")}, States: []string{"draft"}, Page: pagination.Page{Size: 100}})
	if len(drafts.Items) != 1 || drafts.Items[0].Key != "c.plain0" || drafts.Checked["de"] != 1 {
		t.Errorf("drafts = %+v", drafts)
	}

	for _, bad := range []app.ProjectTermCheck{
		{},
		{Locales: []bcp47.Tag{tag("de")}, States: []string{"published"}},
	} {
		if _, err := h.svc.CheckProjectTerminology(h.translator(), p, bad); !errors.Is(err, app.ErrLocaleCount) && !errors.Is(err, app.ErrInvalidState) {
			t.Errorf("check %+v: %v", bad, err)
		}
	}
	if _, err := h.svc.CheckProjectTerminology(h.translator(), uuid.New(), app.ProjectTermCheck{Locales: []bcp47.Tag{tag("de")}}); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unknown project: %v", err)
	}
}

// Each item names the source revision its findings were computed
// against — the message's current one, whose text SourceText is — and
// it moves when the source does. A waiver on a terminology finding is
// measured against it (RFC 0005 §2.3); without it, a waived finding
// could never come back, whatever happened to the source under it.
func TestProjectTerminologyCheckNamesTheSourceRevision(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, "shop", false, []string{"de"}, map[string]string{"a.open": "Open your workspace"})
	if _, _, err := h.svc.CreateConcept(h.developer(), nil, workspace(), ""); err != nil {
		t.Fatal(err)
	}
	h.translate(t, p, "a.open", "de", "Öffne deinen Workspace", nil)
	h.drain(t)
	q := app.ProjectTermCheck{Locales: []bcp47.Tag{tag("de")}, Page: pagination.Page{Size: 100}}

	first := check(t, h, p, q)
	if len(first.Items) != 1 || first.Items[0].SourceRevision != 1 {
		t.Fatalf("report = %+v, want one item at source revision 1", first.Items)
	}

	// The source moves; the translation does not. The check reads the
	// new source, so that is the revision it names.
	h.push(t, p, map[string]string{"a.open": "Open the shared workspace"})
	h.drain(t)
	second := check(t, h, p, q)
	if len(second.Items) != 1 || second.Items[0].SourceRevision != 2 ||
		second.Items[0].SourceText != "Open the shared workspace" {
		t.Fatalf("report = %+v, want the item at source revision 2", second.Items)
	}
}

func check(t *testing.T, h *harness, p uuid.UUID, c app.ProjectTermCheck) app.ProjectTermReport {
	t.Helper()
	r, err := h.svc.CheckProjectTerminology(h.translator(), p, c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
