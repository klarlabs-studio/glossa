package app_test

import (
	"slices"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// These are M1's `glossa check` tests, moved with the layers they test
// (RFC 0005 §13 wave 1). Every code, every severity and every count is
// what it was: the layers moved, they did not change.

func parse(t *testing.T, text, locale string) (*mf.Message, *layers.Invalid) {
	t.Helper()
	tag, err := bcp47.Parse(locale)
	if err != nil {
		t.Fatalf("locale %q: %v", locale, err)
	}
	c, err := mfcontent.Parse(mfcontent.MF1, text, tag)
	if err != nil {
		return nil, &layers.Invalid{Code: "invalid-message", Detail: err.Error()}
	}
	model := c.Model
	return &model, nil
}

func msg(t *testing.T, key, text string) layers.Message {
	t.Helper()
	model, invalid := parse(t, text, "en")
	return layers.Message{Key: key, Model: model, Invalid: invalid}
}

func tr(t *testing.T, key, locale, text string) layers.Translation {
	t.Helper()
	model, invalid := parse(t, text, locale)
	return layers.Translation{Key: key, Locale: locale, Model: model, Invalid: invalid, State: "approved"}
}

func project(t *testing.T) *layers.Project {
	t.Helper()
	return &layers.Project{
		Origin: "server", SourceLocale: "en",
		Locales: []layers.Locale{{Code: "en", IsSource: true}, {Code: "de"}, {Code: "ja"}},
		Messages: []layers.Message{
			msg(t, "cart.items", "{count, plural, one {# item} other {# items}}"),
			msg(t, "checkout.pay", "Pay {amount, number}"),
		},
		Translations: map[string]map[string]layers.Translation{
			"de": {
				"cart.items":   tr(t, "cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}"),
				"checkout.pay": tr(t, "checkout.pay", "de", "Bezahlen"),
			},
			"ja": {"cart.items": tr(t, "cart.items", "ja", "{count}個")},
		},
	}
}

func codes(fs []domain.Finding) map[string]domain.Severity {
	out := map[string]domain.Severity{}
	for _, f := range fs {
		out[f.Locus.Locale+" "+f.Locus.Key+" "+f.Code] = f.Severity
	}
	return out
}

func TestRunReportsCompatAndMissingTranslations(t *testing.T) {
	r := app.Run(project(t), checkpolicy.Policy{}, layers.Default()...)
	got := codes(r.Findings)
	for key, sev := range map[string]domain.Severity{
		"de checkout.pay " + string(mf.FindingMissingArgument):  domain.Error,
		"ja checkout.pay " + checkpolicy.CodeMissingTranslation: domain.Error,
	} {
		if got[key] != sev {
			t.Errorf("%s = %q, want %q (all: %v)", key, got[key], sev, got)
		}
	}
	if r.Passed() || r.Conclusion != domain.ConclusionFailure {
		t.Error("check passed with errors")
	}
	ja := r.Locales[2]
	if ja.Code != "ja" || ja.Missing != 1 || ja.Translated != 1 || ja.Complete || !ja.Required {
		t.Errorf("ja report = %+v", ja)
	}
	if r.Locales[0].Code != "en" || !r.Locales[0].IsSource || r.Locales[0].Required {
		t.Errorf("source report = %+v", r.Locales[0])
	}
	// Every deterministic layer runs, in the order layers.Default()
	// lists them. The four wave-1 and wave-2 layers joined the three
	// M1 ones here, and a run that quietly stopped computing one would
	// be the one failure mode a check may never have.
	want := []domain.Layer{
		domain.LayerStructure, domain.LayerParity, domain.LayerCompleteness,
		domain.LayerStyle, domain.LayerLength, domain.LayerLocale, domain.LayerSource,
	}
	if !slices.Equal(r.Layers, want) {
		t.Errorf("layers = %v, want %v", r.Layers, want)
	}
}

func TestPolicyRequireCompleteAndFailOn(t *testing.T) {
	p := project(t)
	p.Translations["de"]["checkout.pay"] = tr(t, "checkout.pay", "de", "Bezahle {amount, number}")
	r := app.Run(p, checkpolicy.Policy{RequireComplete: []string{"de"}}, layers.Default()...)
	if got := codes(r.Findings)["ja checkout.pay "+checkpolicy.CodeMissingTranslation]; got != domain.Warning {
		t.Fatalf("ja missing = %q, want a warning when only de is required", got)
	}
	if !r.Passed() {
		t.Errorf("warnings failed the check: %+v", r.Findings)
	}
	if r = app.Run(p, checkpolicy.Policy{RequireComplete: []string{"de"}, FailOn: checkpolicy.Warning},
		layers.Default()...); r.Passed() {
		t.Error("fail-on warning passed with warnings")
	}
	r = app.Run(p, checkpolicy.Policy{RequireComplete: []string{"de", "fr"}}, layers.Default()...)
	if got := codes(r.Findings)["fr  "+checkpolicy.CodeMissingLocale]; got != domain.Error || r.Passed() {
		t.Errorf("required fr not in the project = %q (passed %v)", got, r.Passed())
	}
}

func TestOutdatedAndInvalidAndUnknown(t *testing.T) {
	p := project(t)
	p.Messages = append(p.Messages, msg(t, "broken", "{oops"))
	out := tr(t, "cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}")
	out.Outdated = true
	p.Translations["de"]["cart.items"] = out
	p.Translations["de"]["gone"] = tr(t, "gone", "de", "Weg")
	p.Translations["ja"]["checkout.pay"] = tr(t, "checkout.pay", "ja", "{amount")
	got := codes(app.Run(p, checkpolicy.Policy{RequireComplete: []string{}}, layers.Default()...).Findings)
	for key, sev := range map[string]domain.Severity{
		"en broken " + checkpolicy.CodeInvalidMessage:           domain.Error,
		"de cart.items " + checkpolicy.CodeOutdatedTranslation:  domain.Warning,
		"de gone " + checkpolicy.CodeUnknownKey:                 domain.Warning,
		"ja checkout.pay " + checkpolicy.CodeInvalidTranslation: domain.Error,
	} {
		if got[key] != sev {
			t.Errorf("%s = %q, want %q", key, got[key], sev)
		}
	}
}

func TestServerWarningsAreKeptWhenTheKernelCantComputeThem(t *testing.T) {
	p := project(t)
	de := p.Translations["de"]["cart.items"]
	de.Warnings = []mf.Finding{{Code: "max-length-exceeded", Severity: mf.SeverityWarning, Message: "too long"}}
	p.Translations["de"]["cart.items"] = de
	fs := app.Run(p, checkpolicy.Policy{}, layers.Default()...).Findings
	if got := codes(fs)["de cart.items max-length-exceeded"]; got != domain.Warning {
		t.Errorf("max-length-exceeded = %q", got)
	}
	// max_length keeps working through the new model, and arrives
	// layered, fingerprinted and locatable. The layer is `length` and
	// no longer `parity`: RFC 0005 §3.3 owns the rule there, and the
	// length layer relays the stored warning for exactly the caller
	// this test is — one that has the warning and not the constraint.
	seen := 0
	for _, f := range fs {
		if f.Code != "max-length-exceeded" {
			continue
		}
		seen++
		if f.Layer != domain.LayerLength || f.Fingerprint == "" || f.Locus.Key != "cart.items" {
			t.Errorf("max-length-exceeded = %+v", f)
		}
	}
	if seen != 1 {
		t.Errorf("%d max-length-exceeded findings, want one; two layers must not relay one warning", seen)
	}
}

// Every finding a layer emits carries an identity, whichever layer it
// came from, so a waiver can name it.
func TestEveryFindingIsFingerprintedAndLayered(t *testing.T) {
	p := project(t)
	p.Messages = append(p.Messages, msg(t, "broken", "{oops"))
	p.Translations["de"]["gone"] = tr(t, "gone", "de", "Weg")
	seen := map[string]bool{}
	for _, f := range app.Run(p, checkpolicy.Policy{}, layers.Default()...).Findings {
		if f.Schema != domain.Schema || f.Fingerprint == "" || !f.Layer.Valid() {
			t.Fatalf("unsealed finding: %+v", f)
		}
		if seen[f.Fingerprint] {
			t.Errorf("two findings share a fingerprint: %+v", f)
		}
		seen[f.Fingerprint] = true
	}
}
