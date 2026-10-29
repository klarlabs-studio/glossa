package domain_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func finding() domain.Finding {
	rev := 7
	return domain.New(domain.Finding{
		Layer: domain.LayerParity, Code: "missing-argument", Severity: domain.Error,
		Locus: domain.Locus{
			Key: "checkout.pay", Locale: "fr", Namespace: "checkout",
			File: "src/checkout/PaymentFooter.vue", Line: 42, Column: 7,
			Route: "/checkout/payment", Component: "PaymentFooter",
			Span: &domain.Span{Side: domain.SideTarget, Start: 12, End: 19},
		},
		Message: "the translation doesn't use {amount}", Subject: "amount",
		Detail: "number", SourceRevision: &rev,
	})
}

func TestNewSealsTheFinding(t *testing.T) {
	f := finding()
	if f.Schema != domain.Schema {
		t.Errorf("schema = %q, want %q", f.Schema, domain.Schema)
	}
	if !strings.HasPrefix(f.Fingerprint, domain.FingerprintPrefix) || len(f.Fingerprint) != len(domain.FingerprintPrefix)+16 {
		t.Errorf("fingerprint = %q", f.Fingerprint)
	}
	if domain.New(finding()).Fingerprint != f.Fingerprint {
		t.Error("the same finding fingerprinted twice gave two answers")
	}
}

// The whole point of the fingerprint: an edit that has nothing to do
// with this finding must not churn it. RFC 0005 §2.1 lists what it
// excludes; this is that list, one mutation at a time.
func TestFingerprintIgnoresEverythingButItsFiveParts(t *testing.T) {
	base := finding()
	rev := 9
	unchanged := map[string]func(*domain.Finding){
		"a reworded explanation":        func(f *domain.Finding) { f.Message = "totally different prose" },
		"a reworded message elsewhere":  func(f *domain.Finding) { f.Message = "the source now says something else" },
		"a reformatted file":            func(f *domain.Finding) { f.Locus.File, f.Locus.Line, f.Locus.Column = "src/Other.vue", 900, 1 },
		"a moved component":             func(f *domain.Finding) { f.Locus.Route, f.Locus.Component = "/pay", "Footer" },
		"a new source revision":         func(f *domain.Finding) { f.SourceRevision = &rev },
		"a new translation revision":    func(f *domain.Finding) { f.Locus.Revision = "0192f5b0-0000-0000-0000-000000000000" },
		"a tightened policy":            func(f *domain.Finding) { f.Severity = domain.Warning },
		"a different span":              func(f *domain.Finding) { f.Locus.Span = &domain.Span{Side: domain.SideSource, Start: 0, End: 3} },
		"a qualifier":                   func(f *domain.Finding) { f.Detail = "string" },
		"measurements":                  func(f *domain.Finding) { f.Evidence = map[string]any{"ratio": 1.74} },
		"a fix hint":                    func(f *domain.Finding) { f.Fix = &domain.Fix{Kind: domain.FixReplace, Hint: "Payer"} },
		"a capture":                     func(f *domain.Finding) { f.Locus.Capture, f.Locus.Region = "cap", "r_18" },
		"the subject padded and spaced": func(f *domain.Finding) { f.Subject = "  amount  " },
	}
	for what, mutate := range unchanged {
		got := base
		mutate(&got)
		if fp := domain.New(got).Fingerprint; fp != base.Fingerprint {
			t.Errorf("%s changed the fingerprint: %s -> %s", what, base.Fingerprint, fp)
		}
	}

	changed := map[string]func(*domain.Finding){
		"another layer":   func(f *domain.Finding) { f.Layer = domain.LayerStructure },
		"another code":    func(f *domain.Finding) { f.Code = "extra-argument" },
		"another key":     func(f *domain.Finding) { f.Locus.Key = "checkout.total" },
		"another locale":  func(f *domain.Finding) { f.Locus.Locale = "de" },
		"another subject": func(f *domain.Finding) { f.Subject = "count" },
		"a message ID":    func(f *domain.Finding) { f.Locus.Message = "0192f5a1-0000-0000-0000-000000000000" },
	}
	seen := map[string]string{base.Fingerprint: "the finding itself"}
	for what, mutate := range changed {
		got := base
		mutate(&got)
		fp := domain.New(got).Fingerprint
		if other, ok := seen[fp]; ok {
			t.Errorf("%s collides with %s (%s)", what, other, fp)
		}
		seen[fp] = what
	}
}

// A message ID is used where the caller has one, so a rename never
// re-opens a server-side waiver; a key stands in offline.
func TestFingerprintPrefersTheMessageID(t *testing.T) {
	id := "0192f5a1-0000-0000-0000-000000000000"
	withID := domain.Locus{Message: id, Key: "checkout.pay", Locale: "fr"}
	renamed := domain.Locus{Message: id, Key: "checkout.pay_now", Locale: "fr"}
	a := domain.Fingerprint(domain.LayerParity, "missing-argument", withID, "amount")
	b := domain.Fingerprint(domain.LayerParity, "missing-argument", renamed, "amount")
	if a != b {
		t.Errorf("a rename changed the fingerprint of a message the server knows by ID: %s != %s", a, b)
	}
	keyOnly := domain.Fingerprint(domain.LayerParity, "missing-argument",
		domain.Locus{Key: "checkout.pay", Locale: "fr"}, "amount")
	if keyOnly == a {
		t.Error("the key and the ID fingerprinted the same; one of them is not being hashed")
	}
}

func TestNormalizeSubject(t *testing.T) {
	for in, want := range map[string]string{
		" Log  in\tnow\n": "Log in now",
		"amount":          "amount",
		"":                "",
		"é":               "é",
		"é":              "é", // NFD composes to NFC
	} {
		if got := domain.NormalizeSubject(in); got != want {
			t.Errorf("NormalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
