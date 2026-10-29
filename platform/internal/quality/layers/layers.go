// Package layers is the QA every surface runs, one package-level type
// per layer behind the Checker port (RFC 0005 §2.1, §3).
//
// The deterministic layers run everywhere, from this code: in `glossa
// check` (offline, over the local catalogs), in the Glossa pull-request
// check, in the server's write-time job and live in Studio. A finding a
// translator sees while typing is the finding that fails the build,
// because there is one implementation and three callers.
//
// A layer is given a Project — the catalog as the caller sees it — and
// the project's check policy, and answers with findings. It never
// reaches for a file, a database or a provider: `glossa check` must be
// cheap, offline-capable, deterministic and free (RFC 0005 §14
// decision 2), and everything a model decides is a job elsewhere.
//
// Project is Quality's own input type rather than the CLI's snapshot,
// so the server can build one from the catalog without the context
// depending on the command.
package layers

import (
	"sort"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Invalid is text that doesn't parse, with the kernel's error code.
type Invalid struct {
	Code   string
	Detail string
}

// Locale is one of the project's locales.
type Locale struct {
	Code     string
	IsSource bool
	// File is the local catalog it came from; empty on the server.
	File string
}

// Message is a source message.
type Message struct {
	// ID is the catalog message's ID. Empty where the caller has only
	// keys, which is the case for an offline `glossa check`.
	ID        string
	Key       string
	Namespace string
	Revision  int
	Model     *mf.Message
	Invalid   *Invalid
	File      string
	// Text is the message as it was authored, in its own syntax. It is
	// what a finding's span points into, in bytes; a caller that kept
	// only the model leaves it empty, and its layers report no span
	// rather than one into a string nobody has.
	Text string
	// MaxLength bounds the rendered translation in characters; 0 means
	// the message has no limit. It is the constraint behind
	// `max-length-exceeded`, which the length layer computes where the
	// caller carries it and relays from the stored warning where it does
	// not (RFC 0005 §3.3).
	MaxLength int
	// Description is what a translator is told about the message. The
	// source layer reads its absence (RFC 0005 §3.5); no layer reads its
	// contents.
	Description string
	// Usages are the places the product asks for the message, from
	// Context (RFC 0004 §8). The source layer *refines* with them and
	// never needs them: a caller with none — an offline `glossa check`
	// over local catalogs — still gets the layer, and the findings say
	// in their evidence what was not known.
	Usages []Usage
}

// Usage is one place the product asks for a message.
type Usage struct {
	Route     string
	Component string
	File      string
	Line      int
}

// Translation is a message's text in one locale.
type Translation struct {
	Key    string
	Locale string
	Model  *mf.Message
	// State is the review state; empty for local files.
	State string
	// Revision is the translation revision's ID, where the caller knows it.
	Revision string
	// SourceRevision is the source revision the translation was made
	// against.
	SourceRevision int
	Outdated       bool
	// Warnings are the findings the server stored with the translation,
	// which only it can compute (max-length-exceeded).
	Warnings []mf.Finding
	Invalid  *Invalid
	File     string
	// Text is the translation as it was authored, in its own syntax, and
	// what a finding's span points into.
	Text string
}

// Region is one rendered message's measured box on a capture: what the
// layout budget of RFC 0005 §3.3 is computed from, without a browser.
//
// The box was measured while the page showed Locale's text, so the two
// together are a font metric — the advance that region's font gave that
// many characters — and that is the metric the length layer predicts
// other locales' widths from. It is why a region carries the locale it
// was measured in, and why a region measured over text nobody has is
// not a metric at all.
type Region struct {
	// Key is the message that rendered in the box.
	Key string
	// Locale is the locale the page was rendered in when it was
	// measured.
	Locale string
	// Capture and ID identify the region for the locus.
	Capture string
	ID      string
	// Width and Height are the box, in CSS pixels and never device
	// pixels (checkpolicy.LengthThresholds says why).
	Width  float64
	Height float64
	// AdvancePerRunePx is the font's measured average advance per rune,
	// where the caller took one directly. Zero means it did not, and the
	// layer derives the metric from the box and the text it held.
	AdvancePerRunePx float64
}

// StyleGuide is the mechanical half of a locale's effective style guide
// (RFC 0003 §2.3), which is the only half the style layer checks.
//
// The guide's prose rules are deliberately absent from this type. RFC
// 0005 §3.2 is explicit that they are prompt material for the
// translation agent and evidence for the linguistic layer, not rules a
// regular expression may grade: "a regex over a rationale would be a
// lie about what the system knows". A type that cannot carry them is a
// type that cannot lie about them.
type StyleGuide struct {
	// Version identifies the merged guide, for provenance in a finding's
	// evidence.
	Version string
	// Formality is "formal", "informal" or empty.
	Formality string
	// Pronouns are the forms of address the guide asks for ("Sie",
	// "vous"); Forbidden the ones it rules out ("du", "tu"). A guide
	// that states a formality and names no forms gets the locale's own,
	// from FormalityForms.
	Pronouns  []string
	Forbidden []string
	// QuoteOpen and QuoteClose are the quotation marks the guide asks
	// for. Empty means it says nothing about them.
	QuoteOpen  string
	QuoteClose string
	// Dash is the dash the guide asks for between words; Ellipsis the
	// ellipsis character it asks for instead of three full stops.
	Dash     string
	Ellipsis string
	// SpaceBeforeUnit says whether a number and its unit are separated.
	// Nil means the guide says nothing.
	SpaceBeforeUnit *bool
	// Decimal and Group are the separators the guide states *beyond*
	// CLDR: a project that writes its numbers one way whatever the
	// locale's default is. Empty defers to the locale layer, which is
	// where CLDR's own answer is graded — the two layers must not both
	// grade the same character.
	Decimal string
	Group   string
	// DateOrder is the date convention the guide states beyond CLDR:
	// "ymd", "dmy" or "mdy". Empty says nothing.
	DateOrder string
	// ForbidTrailingSpace, ForbidDoubleSpace and ForbidAddedFinalStop
	// are RFC 0005 §3.2's punctuation rules. The last is measured
	// against the source — a full stop the source does not have — and
	// not against a list of sentences.
	ForbidTrailingSpace  bool
	ForbidDoubleSpace    bool
	ForbidAddedFinalStop bool
}

// Stated reports whether the guide states any mechanical rule at all.
// A guide that is nothing but prose rules is a guide this layer has
// nothing to check, and saying so is not the same as saying the
// translations are clean.
func (g StyleGuide) Stated() bool {
	switch {
	case g.Formality != "", len(g.Pronouns) > 0, len(g.Forbidden) > 0:
		return true
	case g.QuoteOpen != "", g.Dash != "", g.Ellipsis != "", g.SpaceBeforeUnit != nil:
		return true
	case g.Decimal != "", g.Group != "", g.DateOrder != "":
		return true
	case g.ForbidTrailingSpace, g.ForbidDoubleSpace, g.ForbidAddedFinalStop:
		return true
	}
	return false
}

// Project is the catalog a layer checks: its locales, its active source
// messages and their translations.
type Project struct {
	// Origin says where it was read from ("server", "local").
	Origin string
	// SourceLocale is the locale the messages are written in.
	SourceLocale string
	// Locales has the source locale first, then the rest by code.
	Locales []Locale
	// Messages are the active messages.
	Messages []Message
	// Translations maps locale → key → translation.
	Translations map[string]map[string]Translation
	// Regions are the measured boxes of the captures the caller read,
	// which is what the length layer's layout budget needs. A caller
	// with no capture — `glossa check` — carries none, and the budget is
	// simply not computed: a check that guessed at pixels it never
	// measured would be worse than one that says nothing.
	Regions []Region
	// Styles are the effective style guides by locale, already resolved
	// through the tenant → project → locale → namespace stack. A locale
	// with no guide is a locale the style layer has nothing to say
	// about.
	Styles map[string]StyleGuide
	// Orphans are translations whose message the catalog has obsoleted:
	// text a release no longer ships, for a key the catalog no longer
	// has. The completeness layer reports each as `unknown-key`. Only a
	// caller that reads the server has them — local catalogs know no
	// obsolete messages, and there a translation with no source is
	// simply in Translations under a key Messages lacks.
	//
	// A caller reads one page of at most MaxOrphans of them per listing,
	// in (key, message ID, locale) order, and sets MoreOrphans when a
	// listing had another page: a project that has obsoleted thousands of
	// messages must not make every check read thousands of dead
	// translations.
	Orphans     []Orphan
	MoreOrphans bool

	index map[string]int
}

// Orphan is a translation whose message the catalog has obsoleted.
//
// It carries identity and nothing to grade: no text, no model. An
// orphan is reported for existing, not for what it says, so a reader
// has no reason to parse it and the layer no reason to look.
type Orphan struct {
	// MessageID is the obsolete message's catalog ID. The message still
	// has one, and it is what the finding's fingerprint is hashed over,
	// so every surface that reads the server — the terminal, the
	// server's own run, the pull request that renders it — computes the
	// same one.
	MessageID string
	Key       string
	Namespace string
	Locale    string
	// Revision is the translation's ID, where the caller knows it.
	Revision string
}

// MaxOrphans bounds the orphaned translations one check reports. Past
// it the completeness layer reports the first MaxOrphans by key and one
// finding that says there are more.
//
// The bound is what a reader reads, not only what the layer reports:
// both readers — the server's snapshot and the CLI's — ask the same
// translation listing for one page of MaxOrphans per chunk of locales
// (the listing's largest page), in the listing's own order, and this
// layer sorts and cuts what they read. The terminal and the pull request
// therefore cannot disagree about which ones were reported.
const MaxOrphans = 100

// Style is locale's effective style guide, and whether there is one.
func (p *Project) Style(locale string) (StyleGuide, bool) {
	g, ok := p.Styles[locale]
	return g, ok
}

// SourceLocaleOf is the project's source locale as a Locale, and
// whether the caller named one. The source layer runs on it and on
// nothing else (RFC 0005 §3.5).
func (p *Project) SourceLocaleOf() (Locale, bool) {
	for _, l := range p.Locales {
		if l.IsSource {
			return l, true
		}
	}
	if p.SourceLocale != "" {
		return Locale{Code: p.SourceLocale, IsSource: true}, true
	}
	return Locale{}, false
}

// RegionsFor are the measured regions of key, in a stable order.
func (p *Project) RegionsFor(key string) []Region {
	var out []Region
	for _, r := range p.Regions {
		if r.Key == key {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Locale != out[j].Locale {
			return out[i].Locale < out[j].Locale
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Message returns the source message with key.
func (p *Project) Message(key string) (*Message, bool) {
	if p.index == nil {
		p.index = make(map[string]int, len(p.Messages))
		for i, m := range p.Messages {
			p.index[m.Key] = i
		}
	}
	i, ok := p.index[key]
	if !ok {
		return nil, false
	}
	return &p.Messages[i], true
}

// Identify gives findings measured outside the layers the catalog
// identity their fingerprint is hashed over: the message ID of the key
// each one names, where this project knows one (RFC 0005 §2.1).
//
// Two layers need it. The visual layer's measurements are taken in a
// browser, which holds keys and not the catalog, so a probe finding
// arrives with a key alone; the terminology layer's come from the
// server's termbase check, which reports keys as well. A fingerprint
// over a key is not the one the server computes for the same finding,
// which would leave a waiver made in the terminal not matching the pull
// request. Both of the server's own paths resolve the ID before they
// mint — app.CaptureFindings.findings at the capture ingest, the
// integration context's terminology findings for the PR check — and this
// is how the CLI resolves it.
//
// A finding that already names a message is left alone, and so is one
// whose key this project has never seen: that one keeps falling back to
// the key, which is what domain.Fingerprint does with an empty message.
//
// Findings are copied, never edited in place. A finding that was already
// minted is re-minted, because its identity moved with its locus; one
// that carries no fingerprint yet is left for its layer to mint (the
// visual layer's probe findings, which PromoteVisual seals first).
func (p *Project) Identify(fs []domain.Finding) []domain.Finding {
	out := make([]domain.Finding, 0, len(fs))
	for _, f := range fs {
		if f.Locus.Message == "" && f.Locus.Key != "" {
			if m, ok := p.Message(f.Locus.Key); ok && m.ID != "" {
				f.Locus.Message = m.ID
				if f.Fingerprint != "" {
					f = domain.New(f)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// TargetLocales are the locales other than the source.
func (p *Project) TargetLocales() []Locale {
	var out []Locale
	for _, l := range p.Locales {
		if !l.IsSource {
			out = append(out, l)
		}
	}
	return out
}

// SortedTranslations are a locale's translations by key, so a layer's
// findings come out in a stable order whatever the map iteration did.
func (p *Project) SortedTranslations(locale string) []Translation {
	trs := p.Translations[locale]
	out := make([]Translation, 0, len(trs))
	for _, t := range trs {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Checker is one layer of QA.
type Checker interface {
	// Layer names the layer, which is what a policy and a command line
	// select on.
	Layer() domain.Layer
	// Check answers with the layer's findings, in a stable order.
	Check(p *Project, policy checkpolicy.Policy) []domain.Finding
}

// Default is the deterministic QA a check always runs: every layer that
// needs nothing but the project it is handed — no network, no database,
// no browser and no provider.
//
// Seven of the ten layers of RFC 0005 §3 are here. The three that are
// not are the three that cannot be: `terminology` asks the server's
// termbase, `visual` is measured in a browser and `linguistic` is a
// model's opinion, so all three arrive as Precomputed findings from the
// caller that could fetch them.
//
// A layer with nothing to read is silent, not absent. The style layer
// with no guide, the length layer with no measured regions and the
// locale layer in a locale whose conventions it has no data for all
// report nothing — the honest answer, and not a green one, because the
// run still names the layer in Report.Layers and a reader can tell
// "clean" from "not looked at".
func Default() []Checker {
	return []Checker{
		Structure{}, Parity{}, Completeness{}, Style{}, Length{}, LocaleLayer{}, Source{},
	}
}

// Precomputed is a Checker reporting findings computed elsewhere, such
// as the terminology layer, which asks the server.
func Precomputed(layer domain.Layer, fs []domain.Finding) Checker {
	return precomputed{layer: layer, findings: fs}
}

type precomputed struct {
	layer    domain.Layer
	findings []domain.Finding
}

func (c precomputed) Layer() domain.Layer { return c.layer }

func (c precomputed) Check(*Project, checkpolicy.Policy) []domain.Finding { return c.findings }

// sourceRevision is a pointer to t's source revision, which is what a
// waiver is measured against. A translation the caller knows nothing
// about carries none.
func sourceRevision(t Translation) *int {
	if t.SourceRevision == 0 {
		return nil
	}
	rev := t.SourceRevision
	return &rev
}
