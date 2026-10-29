package domain_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The fingerprint's bytes are a stored format (RFC 0005 §2.1, §2.3).
//
// A waiver names a finding by its fingerprint and nothing else. Change
// how those bytes are computed — add a field, reorder two, drop the
// domain separator, normalize the subject differently — and every
// waiver in every tenant's database silently stops applying: the
// waivers are still there, still reasonable, still listed, and the
// findings they accepted are all back at `error`. Nothing fails, no
// migration runs, and the first anybody hears of it is forty red pull
// requests.
//
// So the bytes are pinned here, as literals. This test is not checking
// that the function works — TestFingerprintIgnoresEverythingButItsFive‌Parts
// does that — it is checking that it has not *changed*. If it fails,
// the question is never "what are the new values"; it is whether the
// change is worth invalidating every stored waiver, and if it is, it
// needs a migration that rewrites them and a note in the RFC.
//
// The cases below are deliberately spread across the shapes a locus
// actually takes: a message known by ID, the same message known only by
// key (an offline `glossa check`), a finding with no locale at all
// (`structure`, `source`), one with a locale and no key
// (`completeness`), a cased subject (`terminology`), a capture region
// (`visual`), and the empty locus. Each exercises a different branch of
// what goes into the hash.
func TestFingerprintGoldenValues(t *testing.T) {
	for _, c := range []struct {
		name    string
		layer   domain.Layer
		code    string
		locus   domain.Locus
		subject string
		want    string
	}{
		{
			name: "parity, message known by ID", layer: domain.LayerParity, code: "argument_missing",
			locus:   domain.Locus{Message: "0192f5a1-0000-0000-0000-000000000001", Key: "checkout.pay", Locale: "fr"},
			subject: "amount", want: "f_c82526b90c24b892",
		},
		{
			name: "parity, the same finding known only by key", layer: domain.LayerParity, code: "argument_missing",
			locus: domain.Locus{Key: "checkout.pay", Locale: "fr"}, subject: "amount",
			want: "f_50e6ac73d3135f60",
		},
		{
			name: "completeness, a key with no locale", layer: domain.LayerCompleteness, code: "unknown_key",
			locus: domain.Locus{Key: "checkout.gone"}, want: "f_ee32f0248df8171e",
		},
		{
			name: "completeness, a locale with no key", layer: domain.LayerCompleteness, code: "missing_translation",
			locus: domain.Locus{Locale: "ja"}, want: "f_56130cf2b211f955",
		},
		{
			name: "terminology, a cased subject", layer: domain.LayerTerminology, code: "term_forbidden",
			locus:   domain.Locus{Message: "0192f5a1-0000-0000-0000-000000000002", Key: "nav.login", Locale: "de"},
			subject: "Login", want: "f_b4c785c25e21f3bf",
		},
		{
			name: "visual, a capture region", layer: domain.LayerVisual, code: "text-clipped",
			locus: domain.Locus{
				Message: "0192f5a1-0000-0000-0000-000000000003", Key: "checkout.pay", Locale: "ja",
				Capture: "0192f5c2-0000-0000-0000-000000000001", Region: "r_18", Route: "/checkout",
			},
			want: "f_af1a06c7bd281073",
		},
		{
			name: "structure, the empty locus", layer: domain.LayerStructure, code: "invalid_message",
			want: "f_8332f0d2d4df0683",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := domain.Fingerprint(c.layer, c.code, c.locus, c.subject); got != c.want {
				t.Errorf("Fingerprint = %s, want %s\n"+
					"This is not a value to update. Every stored waiver names a finding by\n"+
					"this string; changing it silently un-waives all of them (RFC 0005 §2.3).",
					got, c.want)
			}
		})
	}
}

// The shape is part of the format too: a waiver's column checks it
// (`^f_[0-9a-f]{16}$`, migration 0027) and the CLI's `glossa waive`
// validates against it before a round trip, so widening or narrowing
// the hash is a schema change and not a refactor.
func TestFingerprintShapeIsWhatTheColumnAccepts(t *testing.T) {
	fp := domain.Fingerprint(domain.LayerLength, "max_length", domain.Locus{Key: "a", Locale: "fr"}, "")
	if len(fp) != len(domain.FingerprintPrefix)+16 {
		t.Errorf("%q is %d characters; the column accepts f_ and 16 hex digits", fp, len(fp))
	}
	for _, r := range fp[len(domain.FingerprintPrefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("%q is not lowercase hex", fp)
		}
	}
}
