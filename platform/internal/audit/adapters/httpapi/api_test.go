package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/audit/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/audit/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// importer records what reached the use case.
type importer struct {
	got []app.V0HistoryImport
	err error
}

func (i *importer) ImportV0History(_ context.Context, in app.V0HistoryImport) (app.V0HistoryReport, error) {
	if i.err != nil {
		return app.V0HistoryReport{}, i.err
	}
	i.got = append(i.got, in)
	return app.V0HistoryReport{Recorded: len(in.Entries) - 1, Existing: 1}, nil
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func batch(entries ...apiv1.V0HistoryEntry) *apiv1.V0HistoryImport {
	return &apiv1.V0HistoryImport{Restore: "glossa-v03-2026-10-01.dump", RestoreSha256: digest("dump"), Entries: entries}
}

func row(id string) apiv1.V0HistoryEntry {
	return apiv1.V0HistoryEntry{
		V0Id: id, Action: "v0.translation.changed", Actor: "v0:6c1f7c2e-0000-4000-8000-000000000001",
		OccurredAt: time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC), Key: apiconv.Ptr("checkout.pay"), Locale: apiconv.Ptr("de"),
		BeforeSha256: apiconv.Ptr(digest("Bezahlen")), AfterSha256: apiconv.Ptr(digest("Jetzt bezahlen")),
	}
}

func code(t *testing.T, err error) (int, problem.Code) {
	t.Helper()
	var d *problem.Details
	if !errors.As(err, &d) {
		t.Fatalf("err = %v, want a problem", err)
	}
	return d.Status, d.Code
}

func TestTheImportAnswersUnavailableUntilAuditRunsIt(t *testing.T) {
	api := httpapi.New(nil)
	_, err := api.ImportV0History(context.Background(), apiv1.ImportV0HistoryRequestObject{
		Tenant: uuid.NewString(), Project: uuid.NewString(), Body: batch(row("1")),
	})
	if status, c := code(t, err); status != 503 || c != "audit_import_unavailable" {
		t.Fatalf("got %d %s", status, c)
	}
}

func TestTheImportCarriesDigestsAndNeverText(t *testing.T) {
	imp := &importer{}
	api := httpapi.New(imp)
	project := uuid.New()
	call := func(project string, body *apiv1.V0HistoryImport) (apiv1.ImportV0HistoryResponseObject, error) {
		return api.ImportV0History(context.Background(), apiv1.ImportV0HistoryRequestObject{Tenant: uuid.NewString(), Project: project, Body: body})
	}

	unresolved := row("2")
	unresolved.Key, unresolved.Locale, unresolved.BeforeSha256 = nil, nil, nil
	unresolved.Unresolved = apiconv.Ptr("the v0.3 row names no translation")
	resp, err := call(project.String(), batch(row("1"), unresolved))
	if err != nil {
		t.Fatal(err)
	}
	if r := resp.(apiv1.ImportV0History200JSONResponse); r.Recorded != 1 || r.Existing != 1 {
		t.Fatalf("report = %+v", r)
	}
	in := imp.got[0]
	if len(in.Entries) != 2 || in.Restore != "glossa-v03-2026-10-01.dump" || in.RestoreSHA256 != digest("dump") {
		t.Fatalf("import = %+v", in)
	}
	for _, e := range in.Entries {
		if e.Project != project {
			t.Errorf("row %s recorded under %s, not the path's project", e.V0ID, e.Project)
		}
	}
	if e := in.Entries[0]; e.BeforeSHA256 != digest("Bezahlen") || e.AfterSHA256 != digest("Jetzt bezahlen") ||
		e.Key != "checkout.pay" || e.Locale != "de" || e.Actor != "v0:6c1f7c2e-0000-4000-8000-000000000001" {
		t.Fatalf("row = %+v", e)
	}
	if e := in.Entries[1]; e.Key != "" || e.BeforeSHA256 != "" || e.Unresolved == "" {
		t.Fatalf("unresolved row = %+v", e)
	}

	// Text where a digest belongs is refused, and never echoed back.
	smuggled := row("3")
	smuggled.AfterSha256 = apiconv.Ptr("Jetzt bezahlen")
	_, err = call(project.String(), batch(smuggled))
	if status, c := code(t, err); status != 400 || c != problem.CodeInvalidRequest {
		t.Fatalf("text in a digest: %d %s", status, c)
	}
	var d *problem.Details
	errors.As(err, &d)
	if len(d.Errors) != 1 || d.Errors[0].Pointer != "/entries/0/after_sha256" || strings.Contains(d.Error()+d.Errors[0].Detail, "Jetzt") {
		t.Fatalf("problem = %+v", d)
	}
	if len(imp.got) != 1 {
		t.Fatal("a refused batch reached the importer")
	}

	for name, mutate := range map[string]func(*apiv1.V0HistoryImport){
		"no rows":              func(b *apiv1.V0HistoryImport) { b.Entries = nil },
		"a platform actor":     func(b *apiv1.V0HistoryImport) { b.Entries[0].Actor = "person:" + uuid.NewString() },
		"an unknown action":    func(b *apiv1.V0HistoryImport) { b.Entries[0].Action = "translation.revised" },
		"no v0 id":             func(b *apiv1.V0HistoryImport) { b.Entries[0].V0Id = "" },
		"a key with no locale": func(b *apiv1.V0HistoryImport) { b.Entries[0].Locale = nil },
		"a bad restore digest": func(b *apiv1.V0HistoryImport) { b.RestoreSha256 = "abc" },
		"no time":              func(b *apiv1.V0HistoryImport) { b.Entries[0].OccurredAt = time.Time{} },
		"too many rows": func(b *apiv1.V0HistoryImport) {
			for i := range 1000 {
				b.Entries = append(b.Entries, row("x"+string(rune('a'+i%26))))
			}
		},
	} {
		b := batch(row("4"))
		mutate(b)
		if _, err := call(project.String(), b); err == nil {
			t.Errorf("%s: accepted", name)
		} else if status, _ := code(t, err); status != 400 {
			t.Errorf("%s: %d", name, status)
		}
	}
	if _, err := call("not-a-project", batch(row("5"))); err == nil {
		t.Error("a malformed project was accepted")
	} else if status, _ := code(t, err); status != 404 {
		t.Errorf("a malformed project: %d", status)
	}

	// The use case's refusals pass through for Identity's error writer.
	imp.err = &authz.DeniedError{Permission: authz.AuditExport}
	if _, err := call(project.String(), batch(row("6"))); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a refused import: %v", err)
	}
}
