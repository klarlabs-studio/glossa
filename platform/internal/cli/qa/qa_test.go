package qa_test

import (
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func msg(key, text string) snapshot.Message {
	model, args, invalid := snapshot.Parse("mf1", text, "en")
	return snapshot.Message{Key: key, Text: text, Model: model, Arguments: args, Invalid: invalid}
}

func tr(key, locale, text string) snapshot.Translation {
	model, _, invalid := snapshot.Parse("mf1", text, locale)
	return snapshot.Translation{Key: key, Locale: locale, Text: text, Model: model, Invalid: invalid, State: "approved"}
}

func project() *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Origin: "server", SourceLocale: "en",
		Locales: []snapshot.Locale{{Code: "en", IsSource: true}, {Code: "de"}, {Code: "ja"}},
		Messages: []snapshot.Message{
			msg("cart.items", "{count, plural, one {# item} other {# items}}"),
			msg("checkout.pay", "Pay {amount, number}"),
		},
		Translations: map[string]map[string]snapshot.Translation{
			"de": {
				"cart.items":   tr("cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}"),
				"checkout.pay": tr("checkout.pay", "de", "Bezahlen"),
			},
			"ja": {"cart.items": tr("cart.items", "ja", "{count}個")},
		},
	}
}

func codes(fs []domain.Finding) map[string]qa.Severity {
	out := map[string]qa.Severity{}
	for _, f := range fs {
		out[f.Locus.Locale+" "+f.Locus.Key+" "+f.Code] = f.Severity
	}
	return out
}

func TestRunReportsCompatAndMissingTranslations(t *testing.T) {
	r := qa.Run(project(), qa.Policy{}, qa.Default()...)
	got := codes(r.Findings)
	for key, sev := range map[string]qa.Severity{
		"de checkout.pay " + string(mf.FindingMissingArgument): qa.Error,
		"ja checkout.pay " + qa.CodeMissingTranslation:         qa.Error,
	} {
		if got[key] != sev {
			t.Errorf("%s = %q, want %q (all: %v)", key, got[key], sev, got)
		}
	}
	if r.Passed() {
		t.Error("check passed with errors")
	}
	ja := r.Locales[2]
	if ja.Code != "ja" || ja.Missing != 1 || ja.Translated != 1 || ja.Complete || !ja.Required {
		t.Errorf("ja report = %+v", ja)
	}
	if r.Locales[0].Code != "en" || !r.Locales[0].IsSource || r.Locales[0].Required {
		t.Errorf("source report = %+v", r.Locales[0])
	}
}

func TestPolicyRequireCompleteAndFailOn(t *testing.T) {
	s := project()
	s.Translations["de"]["checkout.pay"] = tr("checkout.pay", "de", "Bezahle {amount, number}")
	r := qa.Run(s, qa.Policy{RequireComplete: []string{"de"}}, qa.Default()...)
	if got := codes(r.Findings)["ja checkout.pay "+qa.CodeMissingTranslation]; got != qa.Warning {
		t.Fatalf("ja missing = %q, want a warning when only de is required", got)
	}
	if !r.Passed() {
		t.Errorf("warnings failed the check: %+v", r.Findings)
	}
	if r = qa.Run(s, qa.Policy{RequireComplete: []string{"de"}, FailOn: qa.Warning}, qa.Default()...); r.Passed() {
		t.Error("fail-on warning passed with warnings")
	}
	r = qa.Run(s, qa.Policy{RequireComplete: []string{"de", "fr"}}, qa.Default()...)
	if got := codes(r.Findings)["fr  "+qa.CodeMissingLocale]; got != qa.Error || r.Passed() {
		t.Errorf("required fr not in the project = %q (passed %v)", got, r.Passed())
	}
}

func TestOutdatedAndInvalidAndUnknown(t *testing.T) {
	s := project()
	s.Messages = append(s.Messages, msg("broken", "{oops"))
	out := tr("cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}")
	out.Outdated = true
	s.Translations["de"]["cart.items"] = out
	s.Translations["de"]["gone"] = tr("gone", "de", "Weg")
	s.Translations["ja"]["checkout.pay"] = tr("checkout.pay", "ja", "{amount")
	got := codes(qa.Run(s, qa.Policy{RequireComplete: []string{}}, qa.Default()...).Findings)
	for key, sev := range map[string]qa.Severity{
		"en broken " + qa.CodeInvalidMessage:           qa.Error,
		"de cart.items " + qa.CodeOutdatedTranslation:  qa.Warning,
		"de gone " + qa.CodeUnknownKey:                 qa.Warning,
		"ja checkout.pay " + qa.CodeInvalidTranslation: qa.Error,
	} {
		if got[key] != sev {
			t.Errorf("%s = %q, want %q", key, got[key], sev)
		}
	}
}

func TestServerWarningsAreKeptWhenTheKernelCantComputeThem(t *testing.T) {
	s := project()
	de := s.Translations["de"]["cart.items"]
	de.Warnings = []mf.Finding{{Code: "max-length-exceeded", Severity: mf.SeverityWarning, Message: "too long"}}
	s.Translations["de"]["cart.items"] = de
	if got := codes(qa.Run(s, qa.Policy{}, qa.Default()...).Findings)["de cart.items max-length-exceeded"]; got != qa.Warning {
		t.Errorf("max-length-exceeded = %q", got)
	}
}

// Wave 3 rebuilt the command on the Quality library: a run hands back
// domain.Finding itself. The layer is spelled as the domain spells it —
// `parity`, not M1's `arguments` — and the kernel's subject and the
// local file the finding came from survive all the way to the command.
func TestARunHandsBackTheDomainFinding(t *testing.T) {
	s := project()
	layersSeen := map[domain.Layer]bool{}
	var compat domain.Finding
	for _, f := range qa.Run(s, qa.Policy{}, qa.Default()...).Findings {
		layersSeen[f.Layer] = true
		if f.Schema != domain.Schema || f.Fingerprint == "" {
			t.Errorf("a finding reached the command unsealed: %+v", f)
		}
		if f.Code == string(mf.FindingMissingArgument) {
			compat = f
		}
	}
	for _, want := range []domain.Layer{domain.LayerParity, domain.LayerCompleteness} {
		if !layersSeen[want] {
			t.Errorf("no finding from the %q layer: %v", want, layersSeen)
		}
	}
	if compat.Subject != "amount" || compat.Message == "" ||
		compat.Locus.Locale != "de" || compat.Locus.Key != "checkout.pay" {
		t.Errorf("the kernel's finding lost something on the way to the command: %+v", compat)
	}
}

// Terminology findings reach the command as they always did, through
// the precomputed layer.
func TestPrecomputedFindingsKeepTheirLayer(t *testing.T) {
	f := domain.New(domain.Finding{
		Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: qa.Error,
		Locus: domain.Locus{Key: "cart.checkout", Locale: "de"}, Message: "forbidden term", Subject: "Einkaufswagen",
	})
	r := qa.Run(project(), qa.Policy{RequireComplete: []string{}},
		qa.Precomputed(domain.LayerTerminology, []domain.Finding{f}))
	if len(r.Findings) != 1 {
		t.Fatalf("findings = %+v", r.Findings)
	}
	got := r.Findings[0]
	if got.Layer != domain.LayerTerminology || got.Code != "term_forbidden" || got.Severity != qa.Error ||
		got.Locus.Locale != "de" || got.Locus.Key != "cart.checkout" || got.Subject != "Einkaufswagen" {
		t.Errorf("finding = %+v", got)
	}
	if r.Passed() {
		t.Error("an error finding passed the check")
	}
}

// The catalog message ID travels from the snapshot into every finding's
// locus, in every layer, because that is what a finding's identity is
// hashed over (domain.Fingerprint). It is what makes `glossa check`
// online print the fingerprint the server stores, so a waiver made in
// the terminal matches the pull request.
func TestTheMessageIDReachesEveryFindingsLocus(t *testing.T) {
	s := project()
	for i := range s.Messages {
		s.Messages[i].ID = "msg_" + s.Messages[i].Key
	}
	s.Messages = append(s.Messages, withID(msg("broken", "{oops"), "msg_broken"))
	outdated := tr("cart.items", "de", "{count, plural, one {# Artikel} other {# Artikel}}")
	outdated.Outdated = true
	s.Translations["de"]["cart.items"] = outdated
	s.Translations["de"]["gone"] = tr("gone", "de", "Weg")

	layersSeen := map[domain.Layer]bool{}
	for _, f := range qa.Run(s, qa.Policy{}, qa.Default()...).Findings {
		layersSeen[f.Layer] = true
		switch {
		case f.Locus.Key == "":
			// A finding about the project rather than a message.
		case f.Code == qa.CodeUnknownKey:
			// No source message has this key, so there is no ID to hash:
			// the fallback to the key is the only honest identity.
			if f.Locus.Message != "" {
				t.Errorf("an unknown key was identified: %+v", f.Locus)
			}
		case f.Locus.Message != "msg_"+f.Locus.Key:
			t.Errorf("%s on %s: locus = %+v, want the catalog message ID", f.Code, f.Locus.Key, f.Locus)
		}
	}
	for _, want := range []domain.Layer{domain.LayerStructure, domain.LayerParity, domain.LayerCompleteness} {
		if !layersSeen[want] {
			t.Fatalf("the %q layer reported nothing, so it proved nothing: %v", want, layersSeen)
		}
	}
}

// Offline there are no IDs: the local catalogs know keys. Every locus
// falls back to the key, exactly as domain.Fingerprint documents, and
// the prints an offline run shows are consistent among themselves and
// deliberately not the server's — nobody holds a waiver offline, and a
// key rename is a new finding there.
func TestAnOfflineRunFingerprintsByKey(t *testing.T) {
	offline := qa.Run(project(), qa.Policy{}, qa.Default()...)
	online := project()
	for i := range online.Messages {
		online.Messages[i].ID = "msg_" + online.Messages[i].Key
	}
	prints := map[string]string{}
	for _, f := range qa.Run(online, qa.Policy{}, qa.Default()...).Findings {
		prints[f.Code+" "+f.Locus.Key+" "+f.Locus.Locale+" "+f.Subject] = f.Fingerprint
	}
	for _, f := range offline.Findings {
		if f.Locus.Message != "" {
			t.Errorf("an offline finding carried a message ID: %+v", f.Locus)
		}
		if f.Fingerprint == "" {
			t.Errorf("an offline finding has no fingerprint: %+v", f)
		}
		if same, ok := prints[f.Code+" "+f.Locus.Key+" "+f.Locus.Locale+" "+f.Subject]; ok && same == f.Fingerprint {
			t.Errorf("%s on %s printed the same offline as online (%s); the ID was not hashed",
				f.Code, f.Locus.Key, f.Fingerprint)
		}
	}
}

// withID is a snapshot message with the catalog ID the server gives it.
func withID(m snapshot.Message, id string) snapshot.Message {
	m.ID = id
	return m
}
