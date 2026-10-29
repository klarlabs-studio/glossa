package domain_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func v0Change() domain.V0Change {
	return domain.V0Change{
		V0ID: "4711", Actor: "v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d",
		OccurredAt: time.Date(2025, 3, 14, 15, 9, 26, 535897000, time.UTC), Project: goldenProject,
		Key: "cart.items", Locale: "de", BeforeSHA256: sum("alt"), AfterSHA256: sum("neu"),
		Restore: "glossa-v03-2026-09-30.sql.gz", RestoreSHA256: sum("the dump"),
	}
}

// An imported entry, pinned like the golden pair: its canonical form is
// glossa.audit.entry/1 with "import" as the source — the same members
// in the same encoding, which is why the format did not change.
func TestAnImportedEntrysCanonicalFormIsTheSameFormat(t *testing.T) {
	d, err := v0Change().Draft()
	if err != nil {
		t.Fatal(err)
	}
	e, err := domain.Append(goldenTenant, domain.Head{}, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"action":"v0.translation.changed","actor":"v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d",` +
		`"aggregate_id":"4711","aggregate_type":"v0.audit_log",` +
		`"event_id":"` + domain.V0EventID("4711").String() + `","format":"glossa.audit.entry/1","locale":"de",` +
		`"occurred_at":"2025-03-14T15:09:26.535897Z","project_id":"0190a1b2-0000-7000-8000-000000000001",` +
		`"request_id":null,"sequence":1,"source":"import",` +
		`"summary":{"after_sha256":"` + sum("neu") + `","before_sha256":"` + sum("alt") + `","key":"cart.items",` +
		`"restore":"glossa-v03-2026-09-30.sql.gz","restore_sha256":"` + sum("the dump") + `","unresolved":null,"v0_id":"4711"},` +
		`"tenant_id":"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","trace_id":null}`
	if string(got) != want {
		t.Errorf("canonical form\n got: %s\nwant: %s", got, want)
	}
	// The golden entries written before the third source existed still
	// hash as pinned: TestGoldenHashesArePinned runs alongside this.
}

func TestV0EventIDIsStableAndDistinct(t *testing.T) {
	if domain.V0EventID("1") != domain.V0EventID("1") {
		t.Error("the same row must become the same event")
	}
	if domain.V0EventID("1") == domain.V0EventID("2") || domain.V0EventID("12") == domain.V0EventID("1") {
		t.Error("two rows must become two events")
	}
	// Pinned: a changed namespace would record every row again.
	if got := domain.V0EventID("1").String(); got != uuid.NewSHA1(uuid.MustParse("6f0b3c1e-5a52-4b8e-9d0c-0e3d6b7a0c35"),
		[]byte("glossa.v0.audit_log:1")).String() {
		t.Errorf("V0EventID(1) = %s", got)
	}
}

func TestV0Actors(t *testing.T) {
	for _, a := range []string{
		"v0:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d", "v0:unknown", "v0:ai", "v0:ai:openai", "v0:system:bulk import",
		"v0:ai:" + strings.Repeat("x", 100),
	} {
		if !domain.ValidV0Actor(a) {
			t.Errorf("%q is a v0.3 actor", a)
		}
	}
	for _, a := range []string{
		"", "v0:", "v0:person", "person:6f1c2a9e-1b7d-4c55-9a51-3c1a3e1f0b2d", "unknown", "v0:6F1C2A9E-1B7D-4C55-9A51-3C1A3E1F0B2D",
		"v0:ai:", "v0:ai:a\nb", "v0:ai:" + strings.Repeat("x", 101), "v1:unknown",
	} {
		if domain.ValidV0Actor(a) {
			t.Errorf("%q is not a v0.3 actor", a)
		}
	}
}

// Each source holds its own actors: an imported entry cannot claim a
// platform actor, and a live entry cannot claim a v0.3 one.
func TestSourcesKeepTheirActorsApart(t *testing.T) {
	c := v0Change()
	c.Actor = "person:0190a1b2-0000-7000-8000-0000000000aa"
	if _, err := c.Draft(); !errors.Is(err, domain.ErrInvalidEntry) {
		t.Errorf("an imported entry with a platform actor: %v", err)
	}
	live := goldenDrafts()[0]
	live.Actor = "v0:unknown"
	if err := live.Validate(); !errors.Is(err, domain.ErrInvalidEntry) {
		t.Errorf("a live entry with a v0.3 actor: %v", err)
	}
}

func TestAnUnresolvedRowNamesNoTranslation(t *testing.T) {
	c := v0Change()
	c.Key, c.Locale, c.Unresolved = "", "", "translation_deleted"
	d, err := c.Draft()
	if err != nil {
		t.Fatal(err)
	}
	if d.Locale != "" || !strings.Contains(string(d.Summary), `"unresolved":"translation_deleted"`) ||
		!strings.Contains(string(d.Summary), `"key":null`) {
		t.Errorf("summary %s, locale %q", d.Summary, d.Locale)
	}
	// A sentence is recorded as its shape: nothing written as prose
	// reaches the trail.
	c.Unresolved = "its v0.3 translation no longer exists"
	d, err = c.Draft()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Summary), `"unresolved":"string(len=37)"`) {
		t.Errorf("summary %s", d.Summary)
	}
}

func TestAMalformedRowIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*domain.V0Change){
		"no v0 id":                 func(c *domain.V0Change) { c.V0ID = "" },
		"a v0 id with spaces":      func(c *domain.V0Change) { c.V0ID = "47 11" },
		"no project":               func(c *domain.V0Change) { c.Project = uuid.Nil },
		"no time":                  func(c *domain.V0Change) { c.OccurredAt = time.Time{} },
		"a key without a locale":   func(c *domain.V0Change) { c.Locale = "" },
		"resolved and unresolved":  func(c *domain.V0Change) { c.Unresolved = "translation_deleted" },
		"neither":                  func(c *domain.V0Change) { c.Key, c.Locale = "", "" },
		"a digest that is text":    func(c *domain.V0Change) { c.AfterSHA256 = "Geht's gut?" },
		"an uppercase digest":      func(c *domain.V0Change) { c.BeforeSHA256 = strings.ToUpper(sum("alt")) },
		"a short digest":           func(c *domain.V0Change) { c.BeforeSHA256 = sum("alt")[:63] },
		"no restore digest":        func(c *domain.V0Change) { c.RestoreSHA256 = "" },
		"no restore":               func(c *domain.V0Change) { c.Restore = "" },
		"a restore path":           func(c *domain.V0Change) { c.Restore = "/backups/glossa.sql.gz" },
		"a locale that is a word":  func(c *domain.V0Change) { c.Locale = "Deutsch (Schweiz)" },
		"a platform actor":         func(c *domain.V0Change) { c.Actor = "token:0190a1b2-0000-7000-8000-0000000000ab" },
		"a key with a line break":  func(c *domain.V0Change) { c.Key = "a\nb" },
		"a key longer than a name": func(c *domain.V0Change) { c.Key = strings.Repeat("k", 201) },
	} {
		c := v0Change()
		mutate(&c)
		if _, err := c.Draft(); !errors.Is(err, domain.ErrInvalidEntry) {
			t.Errorf("%s: err = %v, want ErrInvalidEntry", name, err)
		}
	}
	// No digest is a creation or a deletion, not an error.
	c := v0Change()
	c.BeforeSHA256 = ""
	if d, err := c.Draft(); err != nil || !strings.Contains(string(d.Summary), `"before_sha256":null`) {
		t.Errorf("a row with no before: %v %s", err, d.Summary)
	}
}

// A long key is an identifier, but the summary never holds a string
// long enough to be a sentence: it is recorded as its shape.
func TestALongKeyIsRecordedAsItsShape(t *testing.T) {
	c := v0Change()
	c.Key = strings.Repeat("k", 150)
	d, err := c.Draft()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Summary), `"key":"string(len=150)"`) {
		t.Errorf("summary %s", d.Summary)
	}
}
