package domain_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

var now = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func TestFormats(t *testing.T) {
	kinds := map[domain.Format]domain.Kind{
		domain.FormatXLIFF: domain.KindCatalog, domain.FormatJSON: domain.KindCatalog, domain.FormatPO: domain.KindCatalog,
		domain.FormatTMX: domain.KindTM, domain.FormatTBX: domain.KindTermbase,
	}
	for f, k := range kinds {
		got, err := domain.ParseFormat(string(f))
		if err != nil || got.Kind() != k || got.Extension() == "" || got.ContentType() == "" {
			t.Errorf("%s: %v %v", f, got, err)
		}
		if got.Exportable() != (f != domain.FormatPO) {
			t.Errorf("%s exportable = %t", f, got.Exportable())
		}
	}
	if _, err := domain.ParseFormat("csv"); !errors.Is(err, domain.ErrInvalidFormat) {
		t.Errorf("csv: %v", err)
	}
	if m, err := domain.ParseMode(""); err != nil || m != domain.ModeMerge {
		t.Errorf("default mode = %q %v", m, err)
	}
	if _, err := domain.ParseMode("replace"); !errors.Is(err, domain.ErrInvalidMode) {
		t.Errorf("bad mode: %v", err)
	}
}

func TestCancel(t *testing.T) {
	for _, s := range []domain.State{domain.StateAwaitingUpload, domain.StateQueued} {
		j := domain.Job{State: s}
		if changed, err := j.Cancel(now); !changed || err != nil || j.State != domain.StateCancelled || j.FinishedAt == nil {
			t.Errorf("%s: %+v %t %v", s, j, changed, err)
		}
	}
	running := domain.Job{State: domain.StateRunning}
	if changed, err := running.Cancel(now); !changed || err != nil || running.State != domain.StateRunning || !running.CancelRequested {
		t.Errorf("running: %+v", running)
	}
	if changed, _ := running.Cancel(now); changed {
		t.Error("requesting twice changed something")
	}
	cancelled := domain.Job{State: domain.StateCancelled}
	if changed, err := cancelled.Cancel(now); changed || err != nil {
		t.Errorf("cancelled again: %t %v", changed, err)
	}
	for _, s := range []domain.State{domain.StateSucceeded, domain.StateFailed} {
		j := domain.Job{State: s}
		if _, err := j.Cancel(now); !errors.Is(err, domain.ErrNotCancellable) {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestBackoff(t *testing.T) {
	if domain.Backoff(1) != 30*time.Second || domain.Backoff(2) != time.Minute || domain.Backoff(9) != 10*time.Minute {
		t.Errorf("backoff = %s %s %s", domain.Backoff(1), domain.Backoff(2), domain.Backoff(9))
	}
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"catalog.de.xlf":         "catalog.de.xlf",
		"../../etc/passwd":       "passwd",
		`C:\Users\ada\de.po`:     "de.po",
		"a\"b\nc.json":           "abc.json",
		"..":                     "",
		strings.Repeat("x", 300): strings.Repeat("x", 255),
		"  übersetzung.xlf  ":    "übersetzung.xlf",
	} {
		if got := domain.CleanFileName(in); got != want {
			t.Errorf("CleanFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImportOptions(t *testing.T) {
	o, err := domain.Options{Locale: "de_de", Namespace: "web", Syntax: "mf2", State: "draft"}.NormalizeImport(domain.FormatJSON)
	if err != nil || o.Locale != "de-DE" {
		t.Errorf("json = %+v %v", o, err)
	}
	bad := map[string]struct {
		f domain.Format
		o domain.Options
	}{
		"export option":      {domain.FormatJSON, domain.Options{Layout: "nested"}},
		"mf2 plain xliff":    {domain.FormatXLIFF, domain.Options{Syntax: "mf2"}},
		"syntax on po":       {domain.FormatPO, domain.Options{Syntax: "mf1"}},
		"options on tmx":     {domain.FormatTMX, domain.Options{Namespace: "x"}},
		"bad locale":         {domain.FormatJSON, domain.Options{Locale: "not a locale"}},
		"bad state":          {domain.FormatPO, domain.Options{State: "done"}},
		"bad plural var":     {domain.FormatPO, domain.Options{PluralVariable: "$n"}},
		"bad syntax on json": {domain.FormatJSON, domain.Options{Syntax: "icu"}},
	}
	for name, tc := range bad {
		if _, err := tc.o.NormalizeImport(tc.f); !errors.Is(err, domain.ErrInvalidOptions) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if o, err := (domain.Options{Syntax: "mf1"}).NormalizeImport(domain.FormatXLIFF); err != nil || o.Syntax != "mf1" {
		t.Errorf("xliff mf1: %v", err)
	}
	if o, err := (domain.Options{Locale: "de_at"}).NormalizeImport(domain.FormatXLIFF); err != nil || o.Locale != "de-AT" {
		t.Errorf("xliff target locale = %+v, %v", o, err)
	}
}

func TestImportLocaleMustBeTheProjects(t *testing.T) {
	en, de, fr := bcp47.MustParse("en"), bcp47.MustParse("de"), bcp47.MustParse("fr")
	targets := []bcp47.Tag{de, fr}
	ok := map[string]struct {
		f      domain.Format
		locale string
	}{
		"xliff target":   {domain.FormatXLIFF, "de"},
		"json source":    {domain.FormatJSON, "en"},
		"json target":    {domain.FormatJSON, "fr"},
		"po target":      {domain.FormatPO, "de"},
		"no locale":      {domain.FormatXLIFF, ""},
		"tmx has none":   {domain.FormatTMX, ""},
		"json no locale": {domain.FormatJSON, ""},
	}
	for name, tc := range ok {
		if err := domain.CheckImportLocale(tc.f, tc.locale, en, targets); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]struct {
		f      domain.Format
		locale string
		want   error
	}{
		"xliff unknown":   {domain.FormatXLIFF, "ja", domain.ErrLocaleNotInProject},
		"xliff as source": {domain.FormatXLIFF, "en", domain.ErrInvalidOptions},
		"po as source":    {domain.FormatPO, "en", domain.ErrInvalidOptions},
		"json unknown":    {domain.FormatJSON, "de-AT", domain.ErrLocaleNotInProject},
	}
	for name, tc := range bad {
		if err := domain.CheckImportLocale(tc.f, tc.locale, en, targets); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestItemAt(t *testing.T) {
	it := domain.Item{Key: "a"}.At(formats.Position{Line: 3, Column: 5, Ref: "/a"})
	if it.Line != 3 || it.Column != 5 || it.Ref != "/a" || it.Key != "a" {
		t.Errorf("item = %+v", it)
	}
	long := domain.Item{}.At(formats.Position{Ref: strings.Repeat("é", domain.MaxRefRunes+10)})
	if n := len([]rune(long.Ref)); n != domain.MaxRefRunes || !strings.HasSuffix(long.Ref, "…") {
		t.Errorf("ref of %d runes, want %d ending in …", n, domain.MaxRefRunes)
	}
}

func TestTargetLocaleMismatch(t *testing.T) {
	targets := []bcp47.Tag{bcp47.MustParse("de"), bcp47.MustParse("fr")}
	if got := domain.UnknownTargetLocales([]bcp47.Tag{bcp47.MustParse("de")}, targets); len(got) != 0 {
		t.Errorf("de is the project's: %v", got)
	}
	got := domain.UnknownTargetLocales([]bcp47.Tag{bcp47.MustParse("de-AT"), bcp47.MustParse("fr"), bcp47.MustParse("de-AT")}, targets)
	if len(got) != 1 || got[0].String() != "de-AT" {
		t.Errorf("unknown = %v, want [de-AT]", got)
	}
}

func TestExportOptions(t *testing.T) {
	o, err := domain.Options{Locales: []string{"fr", "de_de", "fr"}, Namespaces: []string{"web", "app", "web"},
		States: []string{"needs_review", "approved"}, Layout: "nested", Syntax: "mf1"}.NormalizeExport(domain.FormatJSON)
	if err != nil || strings.Join(o.Locales, ",") != "fr,de-DE" || strings.Join(o.Namespaces, ",") != "app,web" ||
		strings.Join(o.ExportStates(), ",") != "approved,needs_review" {
		t.Errorf("json = %+v %v", o, err)
	}
	if got := (domain.Options{}).ExportStates(); len(got) != 1 || got[0] != "approved" {
		t.Errorf("default states = %v", got)
	}
	if _, err := (domain.Options{}).NormalizeExport(domain.FormatPO); !errors.Is(err, domain.ErrNotExportable) {
		t.Errorf("po export: %v", err)
	}
	for name, tc := range map[string]struct {
		f domain.Format
		o domain.Options
	}{
		"import option":     {domain.FormatJSON, domain.Options{Locale: "de"}},
		"layout on xliff":   {domain.FormatXLIFF, domain.Options{Layout: "flat"}},
		"states on tmx":     {domain.FormatTMX, domain.Options{States: []string{"approved"}}},
		"locales on tbx":    {domain.FormatTBX, domain.Options{Locales: []string{"de"}}},
		"bad layout":        {domain.FormatJSON, domain.Options{Layout: "tree"}},
		"bad state":         {domain.FormatXLIFF, domain.Options{States: []string{"final"}}},
		"bad source locale": {domain.FormatTMX, domain.Options{SourceLocale: "??"}},
		"too many locales":  {domain.FormatXLIFF, domain.Options{Locales: make([]string, domain.MaxExportLocales+1)}},
	} {
		if _, err := tc.o.NormalizeExport(tc.f); !errors.Is(err, domain.ErrInvalidOptions) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A file's review state is kept only as far as the requester may
// decide it.
func TestRequestedStateIsCappedByPermissionAndPolicy(t *testing.T) {
	tests := []struct {
		in                        formats.State
		canReview, reviewRequired bool
		want                      string
	}{
		{formats.StateApproved, true, true, "approved"},
		{formats.StateApproved, false, true, "needs_review"},
		{formats.StateApproved, false, false, "approved"},
		{formats.StateRejected, true, true, "rejected"},
		{formats.StateRejected, false, false, "needs_review"},
		{formats.StateNeedsReview, true, true, "needs_review"},
		{formats.StateDraft, false, true, "draft"},
		{"", false, false, "needs_review"},
	}
	for _, tc := range tests {
		if got := domain.RequestedState(tc.in, tc.canReview, tc.reviewRequired); got != tc.want {
			t.Errorf("RequestedState(%q, review=%t, required=%t) = %q, want %q", tc.in, tc.canReview, tc.reviewRequired, got, tc.want)
		}
	}
}

func TestTranslationStatus(t *testing.T) {
	for _, tc := range []struct {
		write, code string
		want        domain.ItemStatus
	}{
		{"created", "", domain.ItemCreated}, {"revised", "", domain.ItemUpdated}, {"reviewed", "", domain.ItemUpdated},
		{"unchanged", "", domain.ItemUnchanged}, {"", domain.CodeApprovedConflict, domain.ItemConflict},
		{"", "structural_qa_failed", domain.ItemInvalid},
	} {
		if got := domain.TranslationStatus(tc.write, tc.code); got != tc.want {
			t.Errorf("TranslationStatus(%q, %q) = %q, want %q", tc.write, tc.code, got, tc.want)
		}
	}
}

// Merge never rewrites a message's source; overwrite does, for those
// who may manage the catalog.
func TestPlanMessage(t *testing.T) {
	tests := []struct {
		name         string
		exists, same bool
		mode         domain.Mode
		manage       bool
		action       domain.MessageAction
		status       domain.ItemStatus
		code         string
	}{
		{"new, manager", false, false, domain.ModeMerge, true, domain.ActionCreate, domain.ItemCreated, ""},
		{"new, translator", false, false, domain.ModeMerge, false, domain.ActionNone, domain.ItemInvalid, domain.CodeForbidden},
		{"same", true, true, domain.ModeMerge, false, domain.ActionNone, domain.ItemUnchanged, ""},
		{"differs, merge", true, false, domain.ModeMerge, true, domain.ActionNone, domain.ItemConflict, domain.CodeSourceDiffers},
		{"differs, overwrite", true, false, domain.ModeOverwrite, true, domain.ActionRevise, domain.ItemUpdated, ""},
		{"differs, dry run", true, false, domain.ModeDryRun, true, domain.ActionNone, domain.ItemConflict, domain.CodeSourceDiffers},
	}
	for _, tc := range tests {
		p := domain.PlanMessage(tc.exists, tc.same, tc.mode, tc.manage)
		if p.Action != tc.action || p.Status != tc.status || p.Code != tc.code {
			t.Errorf("%s: %+v", tc.name, p)
		}
	}
}

var keyPattern = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)*$`)

func TestPOMessageKey(t *testing.T) {
	k := domain.POMessageKey("", "Add to cart")
	if !strings.HasPrefix(k, "add_to_cart_") || len(k) != len("add_to_cart_")+8 {
		t.Errorf("key = %q", k)
	}
	if k != domain.POMessageKey("", "Add to cart") {
		t.Error("the same entry must get the same key")
	}
	if k == domain.POMessageKey("", "Add to cart!") || k == domain.POMessageKey("menu", "Add to cart") {
		t.Error("different entries share a key")
	}
	if c := domain.POMessageKey("Menu|File", "Open"); !strings.HasPrefix(c, "menu_file.open_") {
		t.Errorf("context key = %q", c)
	}
	for _, id := range []string{"Größe ändern", "日本語", "%s items in your cart, %d left!", strings.Repeat("very long words ", 20), "  "} {
		got := domain.POMessageKey("", id)
		if !keyPattern.MatchString(got) || len(got) > 60 {
			t.Errorf("POMessageKey(%q) = %q is not a catalog key", id, got)
		}
	}
	if got := domain.POMessageKey("", "Größe ändern"); !strings.HasPrefix(got, "grosse_andern_") {
		t.Errorf("accents: %q", got)
	}
	if got := domain.POMessageKey("", "日本語"); len(got) != 8 {
		t.Errorf("non-Latin msgid = %q, want its hash", got)
	}
}

func TestFingerprint(t *testing.T) {
	base := domain.Fingerprint("abc", "p1", domain.FormatXLIFF, domain.ModeMerge, domain.Options{})
	if len(base) != 64 || base != domain.Fingerprint("abc", "p1", domain.FormatXLIFF, domain.ModeMerge, domain.Options{}) {
		t.Fatalf("fingerprint = %q", base)
	}
	for _, other := range []string{
		domain.Fingerprint("abd", "p1", domain.FormatXLIFF, domain.ModeMerge, domain.Options{}),
		domain.Fingerprint("abc", "p2", domain.FormatXLIFF, domain.ModeMerge, domain.Options{}),
		domain.Fingerprint("abc", "p1", domain.FormatXLIFF, domain.ModeOverwrite, domain.Options{}),
		domain.Fingerprint("abc", "p1", domain.FormatXLIFF, domain.ModeMerge, domain.Options{Syntax: "mf1"}),
	} {
		if other == base {
			t.Error("a different import has the same fingerprint")
		}
	}
}

func TestSummary(t *testing.T) {
	var s domain.Summary
	for _, it := range []domain.Item{
		{Kind: domain.ItemMessage, Status: domain.ItemCreated}, {Kind: domain.ItemTranslation, Status: domain.ItemCreated},
		{Kind: domain.ItemTranslation, Status: domain.ItemConflict}, {Kind: domain.ItemTranslation, Status: domain.ItemInvalid},
	} {
		s.Add(it)
	}
	if s.Created != 2 || s.Conflict != 1 || s.Invalid != 1 || s.Total() != 4 ||
		s.ByKind[domain.ItemTranslation].Total() != 3 || s.ByKind[domain.ItemMessage].Created != 1 {
		t.Errorf("summary = %+v", s)
	}
}

func TestIntersectAndCovers(t *testing.T) {
	all := authz.Scope{Granted: true}
	de := authz.Scope{Granted: true, Locales: []string{"de"}}
	deFR := authz.Scope{Granted: true, Locales: []string{"de-AT", "fr"}}
	if got := domain.Intersect(all, de); !got.Granted || len(got.Locales) != 1 {
		t.Errorf("all ∩ de = %+v", got)
	}
	if got := domain.Intersect(de, deFR); !got.Granted || strings.Join(got.Locales, ",") != "de-AT" {
		t.Errorf("de ∩ {de-AT, fr} = %+v", got)
	}
	if got := domain.Intersect(de, authz.Scope{}); got.Granted {
		t.Errorf("∩ nothing = %+v", got)
	}
	if !domain.Covers(de, bcp47.MustParse("de-CH")) || domain.Covers(de, bcp47.MustParse("fr")) {
		t.Error("de covers de-CH and not fr")
	}
}
