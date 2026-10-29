package layers_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The style layer (RFC 0005 §3.2): the mechanical fields of the
// effective style guide, and nothing else. The guide's prose rules are
// prompt material for the agent and evidence for the linguistic layer —
// "a regex over a rationale would be a lie about what the system knows"
// — and layers.StyleGuide cannot carry them at all.

func guided(p *layers.Project, locale string, g layers.StyleGuide) *layers.Project {
	if p.Styles == nil {
		p.Styles = map[string]layers.StyleGuide{}
	}
	p.Styles[locale] = g
	return p
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// ── formality ───────────────────────────────────────────────────────

// §12.2's case: one German translation using `du` under a `Sie` guide.
func TestAGermanTranslationUsingDuUnderASieGuideIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "greeting", "Your order is ready", "Deine Bestellung ist fertig"),
		"de", layers.StyleGuide{Version: "tenant+de", Formality: "formal"})
	f := one(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Subject != "Deine":
		t.Errorf("subject = %q, want the informal form that was found", f.Subject)
	case f.Evidence["style_guide"] != "tenant+de":
		t.Errorf("evidence = %v, want the guide's version for provenance", f.Evidence)
	case f.Locus.Span == nil:
		t.Error("no span; the word is in the authored text and can be underlined")
	}
}

func TestAGermanTranslationUsingSieUnderASieGuideIsNotReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "greeting", "Your order is ready", "Ihre Bestellung ist fertig"),
		"de", layers.StyleGuide{Version: "tenant+de", Formality: "formal"})
	none(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
}

// German is matched as written, because capitalized `Sie` is the formal
// pronoun and lowercase `sie` is "she" and "they".
func TestGermanFormalityIsMatchedCaseSensitively(t *testing.T) {
	p := guided(pair(t, "en", "de", "note", "She confirmed the order", "Sie hat die Bestellung bestätigt"),
		"de", layers.StyleGuide{Version: "tenant+de", Formality: "informal"})
	// `Sie` here *is* the formal pronoun's spelling, so the finding is
	// correct; what the case sensitivity buys is the opposite case.
	one(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)

	q := guided(pair(t, "en", "de", "note", "She confirmed the order", "sie hat die Bestellung bestätigt"),
		"de", layers.StyleGuide{Version: "tenant+de", Formality: "informal"})
	none(t, run(layers.Style{}, q), layers.CodeFormalityMismatch)
}

func TestFrenchTuUnderAVousGuideIsReported(t *testing.T) {
	p := guided(pair(t, "en", "fr", "greeting", "Your order is ready", "Ta commande est prête"),
		"fr", layers.StyleGuide{Version: "tenant+fr", Formality: "formal"})
	one(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
}

// A language Glossa has no forms of address for gets no finding rather
// than a guess (intent §41).
func TestALanguageWithNoCuratedFormsGetsNoFormalityFinding(t *testing.T) {
	p := guided(pair(t, "en", "ja", "greeting", "Your order is ready", "ご注文の準備ができました"),
		"ja", layers.StyleGuide{Version: "tenant+ja", Formality: "formal"})
	none(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
}

// A guide that names the forms itself is believed over the table.
func TestAGuideMayNameItsOwnForbiddenForms(t *testing.T) {
	p := guided(pair(t, "en", "ja", "greeting", "Your order is ready", "注文できたよ"),
		"ja", layers.StyleGuide{Version: "tenant+ja", Formality: "formal", Forbidden: []string{"よ"}})
	one(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
}

// ── typography ──────────────────────────────────────────────────────

func TestAStraightQuoteUnderAGuillemetGuideIsReported(t *testing.T) {
	p := guided(pair(t, "en", "fr", "notice", `Press "Save" to continue`, `Appuyez sur "Enregistrer" pour continuer`),
		"fr", layers.StyleGuide{Version: "tenant+fr", QuoteOpen: "«", QuoteClose: "»"})
	f := one(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
	if f.Subject != `"` {
		t.Errorf("subject = %q, want the mark that was found", f.Subject)
	}
}

func TestTheGuidesOwnQuotationMarksAreNotReported(t *testing.T) {
	p := guided(pair(t, "en", "fr", "notice", `Press "Save" to continue`,
		"Appuyez sur «Enregistrer» pour continuer"),
		"fr", layers.StyleGuide{Version: "tenant+fr", QuoteOpen: "«", QuoteClose: "»"})
	none(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
}

func TestThreeFullStopsUnderAnEllipsisGuideAreReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "loading", "Loading...", "Wird geladen..."),
		"de", layers.StyleGuide{Version: "tenant+de", Ellipsis: "…"})
	one(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
}

func TestAHyphenWhereTheGuideWantsAnEmDashIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "note", "Ready - and paid", "Fertig - und bezahlt"),
		"de", layers.StyleGuide{Version: "tenant+de", Dash: "—"})
	one(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
}

func TestAMissingSpaceBeforeAUnitIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "battery", "Battery at 80 %", "Akku bei 80%"),
		"de", layers.StyleGuide{Version: "tenant+de", SpaceBeforeUnit: yes()})
	one(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
}

func TestASpaceBeforeAUnitTheGuideJoinsIsReported(t *testing.T) {
	p := guided(pair(t, "de", "en", "battery", "Akku bei 80 %", "Battery at 80 %"),
		"en", layers.StyleGuide{Version: "tenant+en", SpaceBeforeUnit: no()})
	one(t, run(layers.Style{}, p), layers.CodeTypographyMismatch)
}

// ── conventions beyond CLDR ─────────────────────────────────────────

func TestANumberAgainstTheGuidesOwnSeparatorIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "total", "Total 1,234.50", "Gesamt 1,234.50"),
		"de", layers.StyleGuide{Version: "tenant+de", Decimal: ","})
	f := one(t, run(layers.Style{}, p), layers.CodeConventionMismatch)
	if f.Evidence["wanted_decimal"] != "," {
		t.Errorf("evidence = %v, want the guide's separator", f.Evidence)
	}
}

// Where the guide says nothing, the locale layer's CLDR answer stands
// and this layer is silent: the two must never both grade one comma.
func TestWithoutAGuideConventionTheStyleLayerSaysNothingAboutNumbers(t *testing.T) {
	p := guided(pair(t, "en", "de", "total", "Total 1,234.50", "Gesamt 1,234.50"),
		"de", layers.StyleGuide{Version: "tenant+de"})
	none(t, run(layers.Style{}, p), layers.CodeConventionMismatch)
}

func TestADateAgainstTheGuidesOrderIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "ends", "Ends 12/31/2024", "Endet 12/31/2024"),
		"de", layers.StyleGuide{Version: "tenant+de", DateOrder: "dmy"})
	one(t, run(layers.Style{}, p), layers.CodeConventionMismatch)
}

// ── punctuation ─────────────────────────────────────────────────────

func TestATrailingSpaceTheGuideForbidsIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "label", "Email address", "E-Mail-Adresse "),
		"de", layers.StyleGuide{Version: "tenant+de", ForbidTrailingSpace: true})
	one(t, run(layers.Style{}, p), layers.CodePunctuationMismatch)
}

func TestADoubledSpaceTheGuideForbidsIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "label", "Order is ready", "Bestellung  ist fertig"),
		"de", layers.StyleGuide{Version: "tenant+de", ForbidDoubleSpace: true})
	one(t, run(layers.Style{}, p), layers.CodePunctuationMismatch)
}

// The rule is measured against the source, not against a list of
// sentences: a full stop the source does not have.
func TestAFullStopTheSourceDoesNotHaveIsReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "label", "Your order is ready", "Ihre Bestellung ist fertig."),
		"de", layers.StyleGuide{Version: "tenant+de", ForbidAddedFinalStop: true})
	one(t, run(layers.Style{}, p), layers.CodePunctuationMismatch)
}

func TestAFullStopTheSourceAlsoHasIsNotReported(t *testing.T) {
	p := guided(pair(t, "en", "de", "label", "Your order is ready.", "Ihre Bestellung ist fertig."),
		"de", layers.StyleGuide{Version: "tenant+de", ForbidAddedFinalStop: true})
	none(t, run(layers.Style{}, p), layers.CodePunctuationMismatch)
}

// ── no guide, no findings ───────────────────────────────────────────

// A locale with no effective guide gets nothing. That is not a green:
// the run still names the layer, so a reader can tell "clean" from
// "nothing to check against".
func TestALocaleWithNoGuideGetsNoFindings(t *testing.T) {
	p := pair(t, "en", "de", "greeting", "Your order is ready", "Deine Bestellung ist fertig")
	if fs := run(layers.Style{}, p); len(fs) != 0 {
		t.Errorf("findings = %v, want none without a guide", codesOf(fs))
	}
}

// ── locus ───────────────────────────────────────────────────────────

func TestAStyleFindingCarriesTheCatalogMessageID(t *testing.T) {
	p := guided(pair(t, "en", "de", "greeting", "Your order is ready", "Deine Bestellung ist fertig"),
		"de", layers.StyleGuide{Version: "tenant+de", Formality: "formal"})
	f := one(t, run(layers.Style{}, p), layers.CodeFormalityMismatch)
	if f.Locus.Message != "msg_greeting" {
		t.Errorf("locus.message = %q, want the catalog ID", f.Locus.Message)
	}
	want := domain.Fingerprint(domain.LayerStyle, layers.CodeFormalityMismatch, f.Locus, f.Subject)
	if f.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", f.Fingerprint, want)
	}
}
