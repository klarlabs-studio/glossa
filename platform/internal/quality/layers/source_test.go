package layers_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The source layer (RFC 0005 §3.5): problematic source copy, before
// anybody translates it. It runs on the source locale only, and every
// code is a warning, because source copy is the product team's call.

// sourceOnly is a project with a source locale and nothing else, which
// is all this layer reads.
func sourceOnly(t *testing.T, ms ...layers.Message) *layers.Project {
	t.Helper()
	return &layers.Project{
		Origin: "server", SourceLocale: "en",
		Locales:      []layers.Locale{{Code: "en", IsSource: true}, {Code: "de"}},
		Messages:     ms,
		Translations: map[string]map[string]layers.Translation{},
	}
}

// ── manual plural ───────────────────────────────────────────────────

// §12.2's case: one `3 item(s)`.
func TestAHandWrittenPluralIsReported(t *testing.T) {
	m := source(t, "cart.items", "en", "{count} item(s) in your cart")
	m.Description = "the cart badge"
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeManualPlural)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Subject != "(s)":
		t.Errorf("subject = %q, want the marker", f.Subject)
	case f.Locus.Locale != "en":
		t.Errorf("locus.locale = %q, want the source locale", f.Locus.Locale)
	case f.Fix == nil:
		t.Error("no fix hint; the layer knows exactly what to suggest")
	}
}

func TestASlashPluralIsReported(t *testing.T) {
	m := source(t, "cart.items", "en", "{count} item/items in your cart")
	m.Description = "the cart badge"
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeManualPlural)
	if f.Subject != "item/items" {
		t.Errorf("subject = %q, want the pair that was written out", f.Subject)
	}
}

// Requiring the second word to extend the first is what keeps "and/or"
// and "km/h" out of it.
func TestASlashThatIsNotAPluralIsNotReported(t *testing.T) {
	m := source(t, "speed.limit", "en", "The limit is {speed} km/h on this road")
	m.Description = "a speed limit"
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeManualPlural)
}

// A message that already selects has said what it meant.
func TestASelectingMessageIsNotAHandWrittenPlural(t *testing.T) {
	m := source(t, "cart.items", "en", "{count, plural, one {# item(s)} other {# items}}")
	m.Description = "the cart badge"
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeManualPlural)
}

// ── ambiguous-short ─────────────────────────────────────────────────

// §12.2's other case: one `ambiguous-short`.
func TestAOneWordMessageWithNoDescriptionIsAmbiguous(t *testing.T) {
	f := one(t, run(layers.Source{}, sourceOnly(t, source(t, "action.open", "en", "Open"))),
		layers.CodeAmbiguousShort)
	switch {
	case f.Severity != domain.Warning:
		t.Errorf("severity = %q, want warning", f.Severity)
	case f.Evidence["usages"] != "unknown":
		t.Errorf("evidence = %v, want it to say the usages were not known", f.Evidence)
	}
}

func TestAOneWordMessageWithADescriptionIsNot(t *testing.T) {
	m := source(t, "action.open", "en", "Open")
	m.Description = "the button that opens the selected file"
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeAmbiguousShort)
}

// Usages only ever withdraw the finding: a label the product asks for
// in exactly one component has one meaning, whatever its length.
func TestAOneWordMessageUsedInOneComponentIsNotAmbiguous(t *testing.T) {
	m := source(t, "action.open", "en", "Open")
	m.Usages = []layers.Usage{{Route: "/files", Component: "FileRow"}, {Route: "/files", Component: "FileRow"}}
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeAmbiguousShort)
}

func TestAOneWordMessageUsedInTwoComponentsIsAmbiguous(t *testing.T) {
	m := source(t, "action.open", "en", "Open")
	m.Usages = []layers.Usage{{Route: "/files", Component: "FileRow"}, {Route: "/inbox", Component: "Toolbar"}}
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeAmbiguousShort)
	if f.Evidence["components"] != 2 {
		t.Errorf("evidence = %v, want the components counted", f.Evidence)
	}
}

func TestASentenceIsNotAmbiguousShort(t *testing.T) {
	none(t, run(layers.Source{}, sourceOnly(t, source(t, "empty.files", "en", "You have no files yet"))),
		layers.CodeAmbiguousShort)
}

// ── missing-description ─────────────────────────────────────────────

func TestAPlaceholderWithNoDescriptionIsReported(t *testing.T) {
	m := source(t, "greeting", "en", "Welcome back, {name}")
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeMissingDescription)
	if f.Severity != domain.Warning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
}

func TestASelectorWithNoDescriptionIsReported(t *testing.T) {
	m := source(t, "cart.items", "en", "{count, plural, one {# item} other {# items}}")
	one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeMissingDescription)
}

func TestAPlainMessageWithNoPlaceholderNeedsNoDescription(t *testing.T) {
	none(t, run(layers.Source{}, sourceOnly(t, source(t, "empty.files", "en", "You have no files yet"))),
		layers.CodeMissingDescription)
}

// ── hardcoded-format ────────────────────────────────────────────────

func TestACurrencySymbolBesideAPlainPlaceholderIsReported(t *testing.T) {
	m := source(t, "cart.total", "en", "Total: ${amount}")
	m.Description = "the cart total"
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeHardcodedFormat)
	if f.Subject != "$" {
		t.Errorf("subject = %q, want the symbol", f.Subject)
	}
}

// A typed placeholder is exactly what the finding asks for, so a
// message that has one is not reported.
func TestATypedPlaceholderIsNotAHardcodedFormat(t *testing.T) {
	m := source(t, "cart.total", "en", "Total: {amount, number}")
	m.Description = "the cart total"
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeHardcodedFormat)
}

// A sentence with a percent sign and no placeholder is a sentence.
func TestAPercentSignWithNoPlaceholderIsNotAFormat(t *testing.T) {
	none(t, run(layers.Source{}, sourceOnly(t, source(t, "sale", "en", "Everything is 50 % off today"))),
		layers.CodeHardcodedFormat)
}

// ── concatenation ───────────────────────────────────────────────────

func TestAMessageEndingInAColonLooksLikeAFragment(t *testing.T) {
	m := source(t, "label.email", "en", "Email address:")
	m.Description = "the field label"
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeConcatenationSuspected)
	if f.Evidence["usages"] != "unknown" {
		t.Errorf("evidence = %v, want it to say the usages were not known", f.Evidence)
	}
}

// With the usages in hand, a fragment that stands alone on its route is
// not being concatenated with anything, and the layer withdraws.
func TestAFragmentAloneOnItsRouteIsNotConcatenation(t *testing.T) {
	m := source(t, "label.email", "en", "Email address:")
	m.Description = "the field label"
	m.Usages = []layers.Usage{{Route: "/signup", Component: "Field"}}
	none(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeConcatenationSuspected)
}

// Two fragments on one route is what concatenation looks like from the
// outside, and the finding says so.
func TestTwoFragmentsOnOneRouteConfirmConcatenation(t *testing.T) {
	a := source(t, "notice.prefix", "en", "You have ")
	a.Description = "first half"
	a.Usages = []layers.Usage{{Route: "/inbox", Component: "Notice"}}
	b := source(t, "notice.suffix", "en", " unread messages:")
	b.Description = "second half"
	b.Usages = []layers.Usage{{Route: "/inbox", Component: "Notice"}}
	fs := run(layers.Source{}, sourceOnly(t, a, b))
	var hits int
	for _, f := range fs {
		if f.Code == layers.CodeConcatenationSuspected {
			hits++
			if f.Evidence["routes"] == nil {
				t.Errorf("evidence = %v, want the shared route named", f.Evidence)
			}
		}
	}
	if hits != 2 {
		t.Errorf("%d concatenation findings, want both halves; the run produced %v", hits, codesOf(fs))
	}
}

func TestAWholeSentenceIsNotAFragment(t *testing.T) {
	none(t, run(layers.Source{}, sourceOnly(t, source(t, "empty.files", "en", "You have no files yet"))),
		layers.CodeConcatenationSuspected)
}

// ── scope and locus ─────────────────────────────────────────────────

// The layer runs on the source and on nothing else: a translation
// cannot be blamed for the message it was given.
func TestTheLayerNeverReportsATranslation(t *testing.T) {
	p := pair(t, "en", "de", "cart.items", "{count} item(s) in your cart", "{count} Artikel(n) im Warenkorb")
	for _, f := range run(layers.Source{}, p) {
		if f.Locus.Locale != "en" {
			t.Errorf("a source finding in %q: %s", f.Locus.Locale, f.Message)
		}
	}
}

func TestASourceFindingCarriesTheCatalogMessageID(t *testing.T) {
	m := source(t, "cart.items", "en", "{count} item(s) in your cart")
	m.Description = "the cart badge"
	f := one(t, run(layers.Source{}, sourceOnly(t, m)), layers.CodeManualPlural)
	switch {
	case f.Locus.Message != "msg_cart.items":
		t.Errorf("locus.message = %q, want the catalog ID", f.Locus.Message)
	case f.Locus.Namespace != "app":
		t.Errorf("locus.namespace = %q, want the message's", f.Locus.Namespace)
	}
	want := domain.Fingerprint(domain.LayerSource, layers.CodeManualPlural, f.Locus, f.Subject)
	if f.Fingerprint != want {
		t.Errorf("fingerprint = %q, want %q", f.Fingerprint, want)
	}
}
