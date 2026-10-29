package layers_test

import (
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The length layer (RFC 0005 §3.3): the message's own limit, the
// expansion norm for the pair, and the box a capture measured.

// ── max_length ──────────────────────────────────────────────────────

// The constraint the caller carries is recomputed here, which is what
// makes the rule exist offline at all: before M4 it was only ever the
// server's stored warning.
func TestMaxLengthIsComputedFromTheMessagesOwnConstraint(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant et confirmer")
	p.Messages[0].MaxLength = 16
	f := one(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
	switch {
	case f.Severity != domain.Error:
		t.Errorf("severity = %q, want error", f.Severity)
	case f.Layer != domain.LayerLength:
		t.Errorf("layer = %q, want length", f.Layer)
	case f.Evidence["max_length"] != 16:
		t.Errorf("evidence = %v, want max_length 16", f.Evidence)
	case f.Fix == nil || f.Fix.Kind != domain.FixShorten || f.Fix.To == nil || *f.Fix.To != 16:
		t.Errorf("fix = %+v, want shorten to 16", f.Fix)
	}
}

func TestATranslationInsideItsMaxLengthIsNotReported(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer")
	p.Messages[0].MaxLength = 16
	none(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
}

// A caller with the warning and not the constraint — the server's
// snapshot, which stores what CheckStructure found at write time —
// still gets the finding, and gets it under `length`.
func TestAStoredMaxLengthWarningIsRelayedUnderLength(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant")
	tr := p.Translations["fr"]["checkout.pay"]
	tr.Warnings = []mf.Finding{{
		Code: layers.CodeMaxLengthExceeded, Severity: mf.SeverityWarning, Message: "16 characters, limit 12",
	}}
	p.Translations["fr"]["checkout.pay"] = tr

	f := one(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
	if f.Layer != domain.LayerLength {
		t.Errorf("layer = %q, want length", f.Layer)
	}
	// And parity, which relayed it before M4, no longer does: one
	// problem may not be two findings under two layers.
	none(t, run(layers.Parity{}, p), layers.CodeMaxLengthExceeded)
}

// The constraint wins over the stored warning, so the two paths can
// never both fire for one translation.
func TestTheConstraintAndTheStoredWarningAreOneFinding(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant et confirmer")
	p.Messages[0].MaxLength = 16
	tr := p.Translations["fr"]["checkout.pay"]
	tr.Warnings = []mf.Finding{{Code: layers.CodeMaxLengthExceeded, Severity: mf.SeverityWarning}}
	p.Translations["fr"]["checkout.pay"] = tr
	one(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
}

// ── expansion ───────────────────────────────────────────────────────

func TestATranslationWellOutsideThePairsNormIsExcessive(t *testing.T) {
	p := pair(t, "de", "fr",
		"checkout.summary",
		"Bestellung prüfen",
		"Veuillez vérifier attentivement votre commande avant de confirmer le paiement définitif")
	f := one(t, run(layers.Length{}, p), layers.CodeExpansionExcessive)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Evidence["ratio"] == nil:
		t.Errorf("evidence = %v, want the ratio that decided it", f.Evidence)
	case f.Fix == nil || f.Fix.Kind != domain.FixShorten:
		t.Errorf("fix = %+v, want a shorten hint", f.Fix)
	}
}

func TestATranslationInsideTheNormIsNotReported(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.summary", "Bestellung prüfen", "Vérifier la commande")
	fs := run(layers.Length{}, p)
	none(t, fs, layers.CodeExpansionExcessive)
	none(t, fs, layers.CodeExpansionSuspicious)
}

// Japanese contracts against a German source, and the norm knows it: a
// table that expected every language to grow would flag every correct
// Japanese translation.
func TestJapaneseContractionIsWithinItsNorm(t *testing.T) {
	p := pair(t, "de", "ja", "checkout.summary", "Bestellung prüfen und bezahlen", "注文を確認して支払う")
	fs := run(layers.Length{}, p)
	none(t, fs, layers.CodeExpansionExcessive)
	none(t, fs, layers.CodeExpansionSuspicious)
}

func TestATranslationFarShorterThanItsNormIsSuspicious(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.summary", "Bestellung sorgfältig prüfen und bezahlen", "OK")
	f := one(t, run(layers.Length{}, p), layers.CodeExpansionSuspicious)
	if f.Severity != domain.Warning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
}

// A source too short to measure makes every ratio a cliff. "OK" to
// "D'accord" is 350 % and perfectly correct.
func TestAShortSourceIsNotMeasuredForExpansion(t *testing.T) {
	p := pair(t, "de", "fr", "dialog.ok", "OK", "D'accord")
	fs := run(layers.Length{}, p)
	none(t, fs, layers.CodeExpansionExcessive)
	none(t, fs, layers.CodeExpansionSuspicious)
}

// The norm is per pair, and a language the table does not name gets the
// deliberately wide default rather than a number nobody can justify.
func TestAnUnknownTargetGetsTheWideDefaultNorm(t *testing.T) {
	th := checkpolicy.DefaultLengthThresholds()
	known, unknown := th.NormFor("de", "ja"), th.NormFor("de", "zxx")
	switch {
	case known.High >= 1:
		t.Errorf("ja norm = %+v, want a contracting one", known)
	case unknown.High <= known.High:
		t.Errorf("unknown norm = %+v, want wider than %+v", unknown, known)
	case unknown.Excessive <= unknown.High:
		t.Error("the default norm has no excessive bound above its high one")
	}
}

func TestARegionalTagUsesItsLanguagesNorm(t *testing.T) {
	th := checkpolicy.DefaultLengthThresholds()
	if th.NormFor("de-DE", "fr-CA") != th.NormFor("de", "fr") {
		t.Error("fr-CA does not expand like fr")
	}
}

// ── the layout budget ───────────────────────────────────────────────

// A region measured in one locale is a font metric: its width over the
// characters that filled it. That metric predicts another locale's
// width without a browser.
func TestAPredictedWidthOverTheRegionsBoxIsReported(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant tout de suite")
	p.Regions = []layers.Region{{
		Key: "checkout.pay", Locale: "de", Capture: "cap_1", ID: "r_18", Width: 148, Height: 20,
	}}
	f := one(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Locus.Capture != "cap_1" || f.Locus.Region != "r_18":
		t.Errorf("locus = %+v, want the capture and region it was measured on", f.Locus)
	case f.Evidence["region_width_px"] != 148.0:
		t.Errorf("evidence = %v, want the box's width in CSS pixels", f.Evidence)
	}
}

func TestATranslationThatFitsTheRegionIsNotReported(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer")
	p.Regions = []layers.Region{{
		Key: "checkout.pay", Locale: "de", Capture: "cap_1", ID: "r_18", Width: 148, Height: 20,
	}}
	none(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
}

// A box tall enough to have wrapped is not an advance any more, and the
// layer says nothing rather than guessing.
func TestATallRegionIsNotUsedAsAFontMetric(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant tout de suite")
	p.Regions = []layers.Region{{
		Key: "checkout.pay", Locale: "de", Capture: "cap_1", ID: "r_18", Width: 148, Height: 96,
	}}
	none(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
}

// A region measured in the very locale being checked is not a
// prediction: the capture already saw whether it fits, and saying so is
// the visual layer's job.
func TestARegionMeasuredInTheCheckedLocaleIsNotAPrediction(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant tout de suite")
	p.Regions = []layers.Region{{
		Key: "checkout.pay", Locale: "fr", Capture: "cap_1", ID: "r_18", Width: 60, Height: 20,
	}}
	none(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
}

// Two regions showing one string are one problem: a fingerprint per
// region would move with the ids a capture happens to hand out.
func TestTwoOverflowingRegionsAreOneFinding(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant tout de suite")
	p.Regions = []layers.Region{
		{Key: "checkout.pay", Locale: "de", Capture: "cap_1", ID: "r_1", Width: 148, Height: 20},
		{Key: "checkout.pay", Locale: "de", Capture: "cap_2", ID: "r_2", Width: 120, Height: 20},
	}
	f := one(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
	if f.Evidence["regions"] != 2 {
		t.Errorf("evidence = %v, want both overflowing regions counted", f.Evidence)
	}
}

// A caller with no captures — `glossa check` — simply does not get the
// budget. The layer never invents pixels it did not measure.
func TestWithoutARegionTheBudgetIsNotComputed(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant tout de suite")
	none(t, run(layers.Length{}, p), layers.CodeLayoutOverflowPredicted)
}

// ── the locus ───────────────────────────────────────────────────────

// Wave 4's rule: the locus carries the catalog message ID wherever the
// caller has one, because a fingerprint over a key is not the one the
// other surfaces compute.
func TestALengthFindingCarriesTheCatalogMessageID(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant et confirmer")
	p.Messages[0].MaxLength = 16
	f := one(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
	switch {
	case f.Locus.Message != "msg_checkout.pay":
		t.Errorf("locus.message = %q, want the catalog ID", f.Locus.Message)
	case f.Locus.Namespace != "app":
		t.Errorf("locus.namespace = %q, want the message's", f.Locus.Namespace)
	case f.Locus.Locale != "fr" || f.Locus.Key != "checkout.pay":
		t.Errorf("locus = %+v, want the translation's key and locale", f.Locus)
	case f.Fingerprint == "":
		t.Error("the layer left the finding without an identity")
	}
	if want := domain.Fingerprint(domain.LayerLength, layers.CodeMaxLengthExceeded, f.Locus, ""); f.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", f.Fingerprint, want)
	}
}

// A project the caller read from local catalogs has no message IDs, and
// the finding falls back to the key — the honest answer offline.
func TestOfflineTheFindingFallsBackToTheKey(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant et confirmer")
	p.Messages[0].ID = ""
	p.Messages[0].MaxLength = 16
	f := one(t, run(layers.Length{}, p), layers.CodeMaxLengthExceeded)
	if f.Locus.Message != "" || f.Locus.Key != "checkout.pay" {
		t.Errorf("locus = %+v, want the key alone", f.Locus)
	}
}

// A rejected translation is not a translation, and the completeness
// layer is what reports its absence.
func TestARejectedTranslationIsNotMeasured(t *testing.T) {
	p := pair(t, "de", "fr", "checkout.pay", "Jetzt bezahlen", "Payer maintenant et confirmer")
	p.Messages[0].MaxLength = 16
	tr := p.Translations["fr"]["checkout.pay"]
	tr.State = "rejected"
	p.Translations["fr"]["checkout.pay"] = tr
	if fs := run(layers.Length{}, p); len(fs) != 0 {
		t.Errorf("findings = %v, want none", codesOf(fs))
	}
}
