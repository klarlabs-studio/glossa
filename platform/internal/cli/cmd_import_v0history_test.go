package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
)

// The canary of RFC 0006 §12.5, applied to the history import: the
// text of every v0.3 change is in the plan, and none of it may reach
// what --history sends. Only its digest does.
func TestHistoryRowsCarryDigestsNeverText(t *testing.T) {
	const canary = "Quokkafluent"
	before, after := "Hallo "+canary, "Geht's gut, "+canary+"?"
	entries := []v0.AuditEntry{
		{V0ID: 7, Action: v0.AuditActionTranslationChanged, Actor: "v0:ai:openai", Key: "greeting", Locale: "de",
			V0TranslationID: "tr-1", OccurredAt: time.Date(2025, 2, 3, 8, 0, 0, 0, time.FixedZone("CET", 3600)),
			Before: &before, After: &after},
		{V0ID: 8, Action: v0.AuditActionTranslationChanged, Actor: "v0:unknown", V0TranslationID: "tr-gone",
			Before: &after, Unresolved: "its v0.3 translation no longer exists " + canary},
		{V0ID: 9, Action: v0.AuditActionTranslationChanged, Actor: "v0:unknown", After: &before,
			Unresolved: "the v0.3 row names no translation"},
	}
	rows := historyRows(entries)
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), canary) || strings.Contains(string(raw), "Geht") {
		t.Fatalf("the history sent carries v0.3's text: %s", raw)
	}
	digest := func(s string) string {
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])
	}
	r := rows[0]
	if r.V0ID != "7" || r.BeforeSHA256 != digest(before) || r.AfterSHA256 != digest(after) || r.Key != "greeting" ||
		r.Locale != "de" || r.Unresolved != "" || r.OccurredAt.Location() != time.UTC || r.OccurredAt.Hour() != 7 {
		t.Errorf("row 7 = %+v", r)
	}
	// A creation has no before, a deletion no after: none, not the
	// digest of an empty string.
	if rows[1].AfterSHA256 != "" || rows[2].BeforeSHA256 != "" {
		t.Errorf("an absent value has a digest: %+v / %+v", rows[1], rows[2])
	}
	// Why a row names no translation is sent as a code: the trail
	// records no prose.
	if rows[1].Unresolved != "translation_deleted" || rows[1].Key != "" || rows[2].Unresolved != "no_translation" {
		t.Errorf("unresolved rows = %+v / %+v", rows[1], rows[2])
	}
}

func TestHistoryUsage(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	var doc errorDoc
	w.json(&doc, "import", "--from", "v0", "--v0-db", "postgres://x/y", "--history", "--dry-run").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--dry-run writes nothing") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--from", "v0", "--v0-url", "http://x", "--history").want(t, ExitUsage)
	if !strings.Contains(doc.Error.Message, "--history belongs to --v0-db") {
		t.Errorf("error = %+v", doc.Error)
	}
	w.json(&doc, "import", "--format", "json", "f.json", "--history").want(t, ExitUsage)
	if srv.audit.posts != 0 {
		t.Errorf("a refused command sent %d history imports", srv.audit.posts)
	}
}
