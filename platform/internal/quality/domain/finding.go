// Package domain is the Quality context's model (RFC 0005 §2): one
// finding, wherever it comes from.
//
// Before M4 a finding had seven shapes — messageformat.Finding,
// Localization's stored warnings, Knowledge's and Intelligence's
// TermFinding, cli/terminology.Finding, cli/qa.Finding and
// integration/app.CheckFinding — and every conversion between them
// dropped something: the byte spans on the way to the CLI, the subject
// and the qualifier on the way to the server, the file and the line
// almost everywhere. RFC 0005 §14 decision 1 replaces all seven with
// this one, so the terminal, the pull request and the server cannot
// disagree about what was found or where.
//
// The type carries no behaviour beyond its identity: a layer says what
// is wrong with which message in which locale, and Context fills the
// locus's file, line, route and component in at report time.
package domain

import (
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

// Schema is the wire schema every finding names
// (runtimes/testdata/schemas/finding.v1.schema.json).
const Schema = "glossa.finding/v1"

// Layer is one of the QA layers of RFC 0005 §3. It is selectable by
// name in the policy and on the command line, so the spelling is part
// of the contract.
type Layer string

// The layers.
const (
	// LayerStructure: every source message and translation parses into a
	// valid MessageFormat 2 model.
	LayerStructure Layer = "structure"
	// LayerParity: a translation is structurally compatible with its
	// source — arguments, selectors, plural categories, markup. It was
	// called `arguments` in M1 and keeps every one of its codes
	// (RFC 0005 §3.1).
	LayerParity Layer = "parity"
	// LayerCompleteness: every active message has a usable, current
	// translation in every locale the policy requires.
	LayerCompleteness Layer = "completeness"
	// LayerTerminology: the translation respects the termbase.
	LayerTerminology Layer = "terminology"
	// LayerStyle: the mechanical fields of the effective style guide.
	LayerStyle Layer = "style"
	// LayerLength: max_length, expansion ratio and the layout budget.
	LayerLength Layer = "length"
	// LayerLocale: locale-convention and bidi breaches.
	LayerLocale Layer = "locale"
	// LayerSource: problematic source copy.
	LayerSource Layer = "source"
	// LayerVisual: what a capture shows.
	LayerVisual Layer = "visual"
	// LayerLinguistic: what a model suspects. Advisory: a policy may not
	// raise it to Error (RFC 0005 §3.8).
	LayerLinguistic Layer = "linguistic"
)

// Layers lists every layer, in report order.
var Layers = []Layer{
	LayerStructure, LayerParity, LayerCompleteness, LayerTerminology, LayerStyle,
	LayerLength, LayerLocale, LayerSource, LayerVisual, LayerLinguistic,
}

// Valid reports whether l is a layer.
func (l Layer) Valid() bool {
	for _, k := range Layers {
		if k == l {
			return true
		}
	}
	return false
}

// Advisory reports whether a policy is forbidden from raising this
// layer to Error (RFC 0005 §14 decision 10: a build never fails on an
// opinion).
//
// The kernel decides it, because the policy is what has to refuse the
// rule, and a second list here could drift from the one that refuses.
func (l Layer) Advisory() bool { return checkpolicy.Advisory(string(l)) }

// Severity ranks a finding. It is the check policy's severity, not a
// second vocabulary: checkpolicy decides what fails a run, and a
// finding it cannot rank is a finding that cannot be graded.
type Severity = checkpolicy.Severity

// Severities a finding may carry. Waived is a rendering of a finding
// and not a third rank a layer may emit (RFC 0005 §2.1): a waived
// finding is still computed and still reported, counted on its own and
// never able to fail a check — which checkpolicy.Policy.Fails already
// gives, because it fails on Error and Warning only.
const (
	Error            = checkpolicy.Error
	Warning          = checkpolicy.Warning
	Waived  Severity = "waived"
)

// Side says which text a span points into.
type Side string

// Sides.
const (
	SideSource Side = "source"
	SideTarget Side = "target"
)

// Span delimits the offending words in the source or the target, in
// bytes. It is what lets Studio and the overlay underline them instead
// of the whole string; Knowledge's terminology QA has computed it since
// M2 and every conversion threw it away.
type Span struct {
	Side  Side `json:"side"`
	Start int  `json:"start"`
	End   int  `json:"end"`
}

// Locus is everything that locates a finding. Every field is optional
// and every field means the same thing in every layer.
//
// The catalog fields come from the layer. The context fields — File,
// Line, Column, Route, Component, Capture and Region — come from
// Context at report time and never from a layer, which is what turns
// almost every finding into a GitHub annotation instead of only the
// ones that happen to be unknown keys.
type Locus struct {
	// Message is the catalog message's ID. It is empty where the caller
	// has only the key, which is the case for an offline `glossa check`.
	Message string `json:"message,omitempty"`
	// Key is the message key.
	Key string `json:"key,omitempty"`
	// Locale is the locale the finding is about; empty for a finding
	// about the project rather than a translation.
	Locale string `json:"locale,omitempty"`
	// Revision is the translation revision the finding was computed
	// against.
	Revision string `json:"revision,omitempty"`
	// Namespace is the message's namespace.
	Namespace string `json:"namespace,omitempty"`
	// File and Line are where the product asks for the message, from
	// Context's usages, or the local catalog file offline.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Column is the usage's column, where one is known.
	Column int `json:"column,omitempty"`
	// Route and Component are where the message renders.
	Route     string `json:"route,omitempty"`
	Component string `json:"component,omitempty"`
	// Capture and Region name the capture region a visual finding is
	// about.
	Capture string `json:"capture,omitempty"`
	Region  string `json:"region,omitempty"`
	// Span delimits the offending words.
	Span *Span `json:"span,omitempty"`
}

// Located reports whether the locus points at a line of the product's
// source, which is what an annotation needs.
func (l Locus) Located() bool { return l.File != "" && l.Line > 0 }

// FixKind is the kind of fix a layer can propose.
type FixKind string

// Fix kinds.
const (
	// FixReplace: replace the text with exact text.
	FixReplace FixKind = "replace"
	// FixShorten: shorten the text to at most To characters.
	FixShorten FixKind = "shorten"
	// FixUseTerm: use the concept's preferred term (Term names it).
	FixUseTerm FixKind = "use-term"
	// FixAdoptSourceChange: remake the translation against the current
	// source.
	FixAdoptSourceChange FixKind = "adopt-source-change"
)

// Fix is a hint, never an action: nothing applies one without a person
// or an explicit --fix (RFC 0005 §9).
type Fix struct {
	Kind FixKind `json:"kind"`
	// To is the length FixShorten asks for.
	To *int `json:"to,omitempty"`
	// Term is the preferred term's ID, for FixUseTerm.
	Term string `json:"term,omitempty"`
	// Hint is the suggested text, where a layer can produce one.
	Hint string `json:"hint,omitempty"`
}

// Finding is one problem, from any layer, on any surface.
type Finding struct {
	// Schema is always Schema.
	Schema string `json:"schema"`
	// Fingerprint identifies the finding across re-runs and surfaces
	// (see Fingerprint). New computes it; nothing else should set it.
	Fingerprint string `json:"fingerprint"`
	Layer       Layer  `json:"layer"`
	// Code is the rule. It is stable and shared with the CLI and the PR
	// check; the MessageFormat kernel's codes keep their spelling.
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Locus    Locus    `json:"locus"`
	// Message explains the finding to a person. Its wording is not
	// stable and it is deliberately not fingerprinted.
	Message string `json:"message"`
	// Subject is the thing the finding is about: the argument, the
	// markup element, the term used. It is part of the fingerprint,
	// because two missing arguments in one message are two findings.
	Subject string `json:"subject,omitempty"`
	// Detail qualifies the code — the MessageFormat kernel's Detail,
	// e.g. "missing-fallback-variant" or "number->string".
	Detail string `json:"detail,omitempty"`
	// Evidence is what a layer measured: RFC 0005 §2.1's `detail`
	// object, spelled `evidence` here so that the kernel's string Detail
	// — which every conversion before M4 dropped — keeps its own name
	// and both survive.
	Evidence map[string]any `json:"evidence,omitempty"`
	// Fix is a structured hint, where the layer can produce one.
	Fix *Fix `json:"fix,omitempty"`
	// SourceRevision is the source revision the finding was computed
	// against. A waiver dies when it changes (RFC 0005 §2.3).
	SourceRevision *int `json:"source_revision,omitempty"`
	// Waiver is the ID of the waiver that accepted this finding, set
	// when Severity is Waived.
	Waiver string `json:"waiver,omitempty"`
}

// New returns f sealed: its schema named and its fingerprint computed.
// Every layer builds its findings through it, so no surface can emit
// one without an identity.
func New(f Finding) Finding {
	f.Schema = Schema
	f.Fingerprint = Fingerprint(f.Layer, f.Code, f.Locus, f.Subject)
	return f
}

// Located reports whether the finding can become an annotation.
func (f Finding) Located() bool { return f.Locus.Located() }

// Waive returns f as the waiver w accepts it: severity Waived, with the
// waiver named. The finding is not hidden and not dropped, because a
// number that goes down without the product getting better is the
// failure mode of every suppression system (RFC 0005 §14 decision 5).
func (f Finding) Waive(waiverID string) Finding {
	f.Severity = Waived
	f.Waiver = waiverID
	return f
}
