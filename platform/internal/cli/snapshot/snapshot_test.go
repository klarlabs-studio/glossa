package snapshot_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/cli/config"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func localConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Version: 1, SourceLocale: "en", Catalogs: config.Catalogs{Path: "locales/{locale}.json"},
		Path: filepath.Join(dir, config.FileName)}
	return cfg
}

func TestFromLocalParsesCatalogsWithTheKernel(t *testing.T) {
	cfg := localConfig(t)
	writeFile(t, cfg.CatalogPath("en"), `{"checkout": {"pay": "Pay {amount, number}"}, "broken": "{oops"}`)
	writeFile(t, cfg.CatalogPath("de_de"), `{"checkout.pay": "Zahle {amount, number}"}`)
	writeFile(t, cfg.CatalogPath("ar"), `{"checkout.pay": "ادفع"}`)
	s, err := snapshot.FromLocal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Locales) != 3 || s.Locales[0].Code != "en" || !s.Locales[0].IsSource ||
		s.Locales[1].Code != "ar" || s.Locales[1].Direction != "rtl" || s.Locales[2].Code != "de-DE" {
		t.Fatalf("locales = %+v", s.Locales)
	}
	pay, ok := s.Message("checkout.pay")
	if !ok || pay.Model == nil || len(pay.Arguments) != 1 || pay.Arguments[0].Type != mf.ArgNumber {
		t.Fatalf("checkout.pay = %+v", pay)
	}
	broken, _ := s.Message("broken")
	if broken.Invalid == nil || broken.Invalid.Code != string(mf.CodeMF1SyntaxError) {
		t.Errorf("broken = %+v", broken.Invalid)
	}
	if tr := s.Translations["de-DE"]["checkout.pay"]; tr.Model == nil || tr.Text != "Zahle {amount, number}" {
		t.Errorf("de translation = %+v", tr)
	}
}

func TestFromLocalNeedsTheSourceCatalog(t *testing.T) {
	_, err := snapshot.FromLocal(localConfig(t))
	var missing *snapshot.SourceCatalogMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v", err)
	}
}

type fakeReader struct{}

func (fakeReader) Locales(context.Context, remote.Scope) ([]remote.ProjectLocale, error) {
	return []remote.ProjectLocale{{Code: "de"}, {Code: "en", IsSource: true}}, nil
}

func (fakeReader) Messages(_ context.Context, _ remote.Scope, f remote.MessageFilter) ([]remote.Message, error) {
	if f.State != "active" {
		return nil, errors.New("want active messages only")
	}
	m := remote.Message{Id: "m1", Key: "cart.items", Namespace: "default", SourceRevision: 2}
	m.Source.Text = "{count, plural, one {# item} other {# items}}"
	m.Source.Syntax = "mf1"
	model, _ := mf.ParseMF1(m.Source.Text, "en")
	m.Source.Model = toMap(model)
	return []remote.Message{m}, nil
}

// ProjectTranslations answers like the bulk listing: Localization's
// view of the keys (here, one that lags a rename) and a translation of
// a message Catalog no longer lists.
func (fakeReader) ProjectTranslations(_ context.Context, _ remote.Scope, locales []string, f remote.TranslationFilter) ([]remote.ProjectTranslation, error) {
	if f.MessageState != "active" || len(f.States) != 0 || len(locales) != 1 || locales[0] != "de" {
		return nil, fmt.Errorf("want every state of active messages in the target locales, got %v %+v", locales, f)
	}
	model, _ := mf.ParseMF1("{count, plural, one {# Artikel} other {# Artikel}}", "de")
	detail := "one"
	return []remote.ProjectTranslation{
		{MessageId: "m1", Key: "cart.old_key", Locale: "de", Text: "…", State: "approved", SourceRevision: 1, Outdated: true,
			Model: toMap(model), Warnings: []remote.QAFinding{{Code: "max-length-exceeded", Severity: "warning", Message: "long", Detail: &detail}}},
		{MessageId: "m-gone", Key: "gone", Locale: "de", Text: "x", State: "approved", Model: toMap(model)},
	}, nil
}

// FirstProjectTranslations answers the one bounded read FromServer
// makes of the translations of obsolete messages: a page, and whether
// the listing had another.
func (fakeReader) FirstProjectTranslations(_ context.Context, _ remote.Scope, locales []string, f remote.TranslationFilter, size int) ([]remote.ProjectTranslation, bool, error) {
	if f.MessageState != "obsolete" || len(locales) != 1 || locales[0] != "de" {
		return nil, false, fmt.Errorf("want the obsolete messages' translations in the target locales, got %v %+v", locales, f)
	}
	if !slicesEqual(f.States, []string{"draft", "needs_review", "approved"}) {
		return nil, false, fmt.Errorf("want every usable review state, got %v", f.States)
	}
	if size != 100 {
		return nil, false, fmt.Errorf("want one page of layers.MaxOrphans, got %d", size)
	}
	return []remote.ProjectTranslation{
		{Id: "tr-legacy", MessageId: "m-legacy", Key: "help.legacy.title", Namespace: "help", MessageState: "obsolete",
			Locale: "de", Text: "Alte Hilfe", State: "approved"},
	}, true, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (fakeReader) FallbackGraph(context.Context, remote.Scope) (map[string][]string, error) {
	return map[string][]string{"*": {"en"}}, nil
}

func toMap(m mf.Message) map[string]any {
	var out map[string]any
	b, _ := m.MarshalJSON()
	_ = json.Unmarshal(b, &out)
	return out
}

func TestFromServerDecodesModelsAndTranslations(t *testing.T) {
	s, err := snapshot.FromServer(context.Background(), fakeReader{}, remote.Scope{}, "en", snapshot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Locales[0].Code != "en" || len(s.TargetLocales()) != 1 || s.Fallback["*"][0] != "en" {
		t.Errorf("locales = %+v fallback = %v", s.Locales, s.Fallback)
	}
	m, _ := s.Message("cart.items")
	if m.Model == nil || len(m.Arguments) != 1 || m.Arguments[0].Selector == nil || m.Revision != 2 {
		t.Errorf("message = %+v", m)
	}
	// The catalog message ID comes with the message. A finding's identity
	// is hashed over it (domain.Fingerprint), so an online `glossa check`
	// fingerprints a finding the way the server fingerprints it.
	if m.ID != "m1" {
		t.Errorf("message ID = %q, want the catalog's", m.ID)
	}
	tr := s.Translations["de"]["cart.items"]
	if tr.Model == nil || !tr.Outdated || tr.State != "approved" || len(tr.Warnings) != 1 || tr.Warnings[0].Detail != "one" || tr.Key != "cart.items" {
		t.Errorf("translation = %+v", tr)
	}
	if len(s.Translations["de"]) != 1 {
		t.Errorf("translations of messages Catalog doesn't list: %+v", s.Translations["de"])
	}
}

// A check asks for the translations of obsolete messages too, and gets
// them apart from the catalog's translations — never in Translations,
// where `glossa pull` would write them back into a catalog file.
func TestFromServerReadsOrphansOnlyWhenAsked(t *testing.T) {
	s, err := snapshot.FromServer(context.Background(), fakeReader{}, remote.Scope{}, "en", snapshot.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Orphans) != 0 || s.MoreOrphans {
		t.Errorf("orphans read without asking: %+v", s.Orphans)
	}
	s, err = snapshot.FromServer(context.Background(), fakeReader{}, remote.Scope{}, "en", snapshot.Options{Orphans: true})
	if err != nil {
		t.Fatal(err)
	}
	want := snapshot.Orphan{MessageID: "m-legacy", Key: "help.legacy.title", Namespace: "help", Locale: "de", Revision: "tr-legacy"}
	if len(s.Orphans) != 1 || s.Orphans[0] != want || !s.MoreOrphans {
		t.Errorf("orphans = %+v (more %v), want %+v and more", s.Orphans, s.MoreOrphans, want)
	}
	if _, ok := s.Translations["de"]["help.legacy.title"]; ok {
		t.Error("an orphan landed among the catalog's translations")
	}
}
