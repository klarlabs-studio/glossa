package layers_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The locale layer (RFC 0005 §3.4). Everything decidable is decided
// from CLDR through x/text; what CLDR does not state is curated, and a
// locale the curated table does not name gets nothing rather than a
// guess.

// ── numbers ─────────────────────────────────────────────────────────

// §12.2's case: a French translation writing `1,234.50`.
func TestAFrenchTranslationWritingAnEnglishNumberIsReported(t *testing.T) {
	p := pair(t, "de", "fr", "cart.total", "Gesamtsumme 1.234,50 EUR", "Total 1,234.50 EUR")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Subject != "1,234.50":
		t.Errorf("subject = %q, want the literal that decided it", f.Subject)
	case f.Evidence["locale_decimal"] != ",":
		t.Errorf("evidence = %v, want French's decimal separator from CLDR", f.Evidence)
	case f.Evidence["decided_by"] != layers.EvidenceCLDR:
		t.Errorf("evidence = %v, want it attributed to CLDR", f.Evidence)
	case f.Locus.Span == nil:
		t.Error("no span; the literal is in the authored text and can be pointed at")
	}
}

func TestAGermanTranslationWritingAGermanNumberIsNotReported(t *testing.T) {
	p := pair(t, "en", "de", "cart.total", "Total 1,234.50 EUR", "Gesamtsumme 1.234,50 EUR")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
}

// `1,234` is one thousand two hundred and thirty-four in English and
// one point two three four in German. Nothing in the string says which
// was meant, so nothing is reported: a check that guessed would be
// wrong half the time on the most common shape there is.
func TestAnAmbiguousNumberIsNotGraded(t *testing.T) {
	p := pair(t, "en", "de", "cart.total", "Total 1,234 items", "Gesamtsumme 1,234 Artikel")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
}

func TestAVersionNumberIsNotAPrice(t *testing.T) {
	p := pair(t, "de", "fr", "about.version", "Version 1.2.3 installiert", "Version 1.2.3 installée")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
}

// ── bidi ────────────────────────────────────────────────────────────

// §12.2's other case: an Arabic translation with a stray U+202B.
func TestAnArabicTranslationWithAStrayBidiControlIsReported(t *testing.T) {
	p := pair(t, "de", "ar", "checkout.pay", "Jetzt bezahlen", "‫ادفع الآن")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeBidiStrayControl)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Subject != "U+202B":
		t.Errorf("subject = %q, want the control's code point", f.Subject)
	case !strings.Contains(f.Message, "U+202B"):
		t.Errorf("message = %q, want the control named", f.Message)
	}
}

func TestACleanArabicTranslationIsNotReported(t *testing.T) {
	p := pair(t, "de", "ar", "checkout.pay", "Jetzt bezahlen", "ادفع الآن")
	fs := run(layers.LocaleLayer{}, p)
	none(t, fs, layers.CodeBidiStrayControl)
	none(t, fs, layers.CodeBidiLeadingRun)
}

// An RTL translation that opens with a left-to-right run reorders in
// the product unless something isolates it.
func TestAnRTLTranslationLedByAnLTRRunIsReported(t *testing.T) {
	p := pair(t, "de", "ar", "cart.items", "Artikel im Warenkorb", "SKU-42 عنصر في السلة")
	one(t, run(layers.LocaleLayer{}, p), layers.CodeBidiLeadingRun)
}

// ── digits ──────────────────────────────────────────────────────────

// Latin digits are never reported: every locale reads them, and CLDR's
// default numbering system is what a locale prefers, not all it takes.
func TestLatinDigitsInAnArabicTranslationAreNotReported(t *testing.T) {
	p := pair(t, "de", "ar", "cart.count", "24 Artikel im Warenkorb", "24 عنصرا في السلة")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeDigitShaping)
}

// Digits from a numbering system the locale does not use are.
func TestDevanagariDigitsInAnArabicTranslationAreReported(t *testing.T) {
	p := pair(t, "de", "ar", "cart.count", "24 Artikel im Warenkorb", "२४ عنصرا في السلة")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeDigitShaping)
	if f.Subject != "Devanagari" {
		t.Errorf("subject = %q, want the digit set that was found", f.Subject)
	}
}

func TestArabicIndicDigitsInAnArabicTranslationAreNotReported(t *testing.T) {
	p := pair(t, "de", "ar", "cart.count", "24 Artikel im Warenkorb", "٢٤ عنصرا في السلة")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeDigitShaping)
}

// ── spacing ─────────────────────────────────────────────────────────

func TestAPlainSpaceBeforeFrenchPunctuationIsReported(t *testing.T) {
	p := pair(t, "de", "fr", "form.error", "Ist das richtig", "Est-ce correct ?")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeSpacingConvention)
	if f.Evidence["decided_by"] != layers.EvidenceCurated {
		t.Errorf("evidence = %v, want it attributed to Glossa's curated data, not to CLDR", f.Evidence)
	}
}

func TestANarrowNoBreakSpaceBeforeFrenchPunctuationIsCorrect(t *testing.T) {
	p := pair(t, "de", "fr", "form.error", "Ist das richtig", "Est-ce correct ?")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeSpacingConvention)
}

// A locale the curated table does not name gets no spacing findings at
// all. Intent §41: do not pretend every language is equally supported.
func TestALocaleWithNoCuratedSpacingGetsNoSpacingFinding(t *testing.T) {
	p := pair(t, "de", "pl", "form.error", "Ist das richtig", "Czy to prawda ?")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeSpacingConvention)
}

// ── dates ───────────────────────────────────────────────────────────

func TestADateTypedIntoTheTextIsReported(t *testing.T) {
	p := pair(t, "de", "fr", "trial.ends", "Testphase endet am 31.12.2024", "L'essai se termine le 31/12/2024")
	one(t, run(layers.LocaleLayer{}, p), layers.CodeDateLiteral)
}

// ── untranslated ────────────────────────────────────────────────────

func TestTextIdenticalToItsSourceInAnotherScriptIsSuspected(t *testing.T) {
	p := pair(t, "de", "ar", "checkout.confirm", "Bestellung bestätigen", "Bestellung bestätigen")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeUntranslatedSuspected)
	if f.Evidence["target_script"] != "Arab" {
		t.Errorf("evidence = %v, want the two scripts that decided it", f.Evidence)
	}
}

// "OK" is "OK" in every locale on earth.
func TestAShortStringIdenticalToItsSourceIsNotSuspected(t *testing.T) {
	p := pair(t, "de", "ar", "dialog.ok", "OK", "OK")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeUntranslatedSuspected)
}

// The same string in a locale with the same script is not evidence of
// anything; that is the linguistic layer's question.
func TestTheSameScriptIsNotEvidenceOfAnUntranslatedString(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.confirm", "Bestellung bestätigen", "Bestellung bestätigen")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeUntranslatedSuspected)
}

// ── locus ───────────────────────────────────────────────────────────

func TestALocaleFindingCarriesTheCatalogMessageID(t *testing.T) {
	p := pair(t, "de", "fr", "cart.total", "Gesamtsumme 1.234,50 EUR", "Total 1,234.50 EUR")
	f := one(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
	if f.Locus.Message != "msg_cart.total" {
		t.Errorf("locus.message = %q, want the catalog ID", f.Locus.Message)
	}
	want := domain.Fingerprint(domain.LayerLocale, layers.CodeNumberConvention, f.Locus, f.Subject)
	if f.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", f.Fingerprint, want)
	}
}

// A locale x/text has no data for gets nothing that needed CLDR, rather
// than a finding the layer cannot justify.
func TestAnUnknownLocaleGetsNoCLDRDecidedFinding(t *testing.T) {
	p := pair(t, "de", "zxx", "cart.total", "Gesamtsumme 1.234,50 EUR", "Total 1,234.50 EUR")
	none(t, run(layers.LocaleLayer{}, p), layers.CodeNumberConvention)
}
