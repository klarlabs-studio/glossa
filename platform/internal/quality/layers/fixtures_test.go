package layers_test

import (
	"sort"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The fixtures the four wave-1 and wave-2 layers are tested over.
//
// They are built the way the CLI and the server build a project: the
// text is parsed by the same kernel, the model *and* the authored text
// are kept (a span points into the latter), and a message carries the
// catalog ID wherever the caller would have one, because that is what a
// finding's fingerprint is hashed over.

func parseIn(t *testing.T, locale, text string) *mf.Message {
	t.Helper()
	tag, err := bcp47.Parse(locale)
	if err != nil {
		t.Fatalf("locale %q: %v", locale, err)
	}
	c, err := mfcontent.Parse(mfcontent.MF1, text, tag)
	if err != nil {
		t.Fatalf("parse %q in %s: %v", text, locale, err)
	}
	model := c.Model
	return &model
}

// source is a source message with its ID, as a project read from the
// server carries it.
func source(t *testing.T, key, locale, text string) layers.Message {
	t.Helper()
	return layers.Message{
		ID: "msg_" + key, Key: key, Namespace: "app", Revision: 1,
		Model: parseIn(t, locale, text), Text: text,
	}
}

// target is one translation, approved.
func target(t *testing.T, key, locale, text string) layers.Translation {
	t.Helper()
	return layers.Translation{
		Key: key, Locale: locale, Model: parseIn(t, locale, text), Text: text,
		State: "approved", Revision: "rev_" + key + "_" + locale, SourceRevision: 1,
	}
}

// pair is the smallest project a translation layer can be run over: one
// source locale, one target, one message.
func pair(t *testing.T, sourceLocale, targetLocale, key, src, tgt string) *layers.Project {
	t.Helper()
	return &layers.Project{
		Origin: "server", SourceLocale: sourceLocale,
		Locales:  []layers.Locale{{Code: sourceLocale, IsSource: true}, {Code: targetLocale}},
		Messages: []layers.Message{source(t, key, sourceLocale, src)},
		Translations: map[string]map[string]layers.Translation{
			targetLocale: {key: target(t, key, targetLocale, tgt)},
		},
	}
}

// run is one layer over one project, with the default policy.
func run(c layers.Checker, p *layers.Project) []domain.Finding {
	return c.Check(p, checkpolicy.Policy{})
}

// codesOf are the codes a run produced, sorted, so a failure says what
// was found instead of only that something was not.
func codesOf(fs []domain.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Code)
	}
	sort.Strings(out)
	return out
}

// one is the single finding with code, and fails loudly with everything
// that was found when there is not exactly one.
func one(t *testing.T, fs []domain.Finding, code string) domain.Finding {
	t.Helper()
	var hits []domain.Finding
	for _, f := range fs {
		if f.Code == code {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("%d findings with code %q, want 1; the run produced %v", len(hits), code, codesOf(fs))
	}
	return hits[0]
}

// none asserts that no finding carries code.
func none(t *testing.T, fs []domain.Finding, code string) {
	t.Helper()
	for _, f := range fs {
		if f.Code == code {
			t.Fatalf("unwanted %q: %s (%v)", code, f.Message, f.Evidence)
		}
	}
}
