// Package qa is `glossa check`'s side of the Quality context: it turns
// the CLI's snapshot into the project a layer checks, runs the layers,
// and renders the result as `glossa check --json` has always rendered
// it.
//
// The QA itself lives in internal/quality (RFC 0005 §14 decision 1):
// the layers, the finding and the run are all there, so `glossa check`,
// the Glossa pull-request check and the server's jobs share one
// implementation and cannot disagree. What is left here is the
// adapter — the CLI's snapshot in, a quality run out.
//
// Nothing is flattened on the way out any more. Wave 3 rebuilt the
// command on the library, so a run hands back app.Report and the
// command reports domain.Finding itself: the locus, the spans and the
// evidence that the old wire shape dropped survive to the terminal,
// and the terminal and the pull request cannot describe one finding
// two ways.
package qa

import (
	"go.klarlabs.de/glossa/platform/internal/cli/snapshot"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The policy and its vocabulary live in the kernel, because the Glossa
// PR check decides the same question on the server and must decide it
// the same way (RFC 0004 §6.4). These are aliases, not copies: there is
// one policy, and `glossa check` and the pull request share it.

// Severity ranks a finding.
type Severity = checkpolicy.Severity

// Severities.
const (
	Error   = checkpolicy.Error
	Warning = checkpolicy.Warning
)

// Finding codes Glossa adds to the kernel's (compat findings keep the
// kernel's codes, e.g. missing-argument).
const (
	CodeInvalidMessage      = checkpolicy.CodeInvalidMessage
	CodeInvalidTranslation  = checkpolicy.CodeInvalidTranslation
	CodeMissingTranslation  = checkpolicy.CodeMissingTranslation
	CodeOutdatedTranslation = checkpolicy.CodeOutdatedTranslation
	CodeUnknownKey          = checkpolicy.CodeUnknownKey
	CodeMissingLocale       = checkpolicy.CodeMissingLocale
)

// Policy decides what fails a check: `require_complete` and `fail_on`,
// shared with the server's PR check.
type Policy = checkpolicy.Policy

// Checker is one layer of QA.
type Checker = layers.Checker

// StyleGuide is the mechanical half of a locale's effective style
// guide, which is all the style layer grades (RFC 0005 §3.2).
type StyleGuide = layers.StyleGuide

// Default is the deterministic QA every check runs.
func Default() []Checker { return layers.Default() }

// Precomputed is a Checker reporting findings computed elsewhere, such
// as the terminology layer, which asks the server.
func Precomputed(layer domain.Layer, fs []domain.Finding) Checker {
	return layers.Precomputed(layer, fs)
}

// Report is a check's result, as the Quality library sums it.
type Report = qualityapp.Report

// Run checks s with the checkers and sums what they found.
func Run(s *snapshot.Snapshot, p Policy, checkers ...Checker) Report {
	return RunProject(Project(s), p, checkers...)
}

// RunProject checks an already-built project, for a caller that needed
// it before the run: `glossa capture --check` resolves its probe
// findings' keys against the project (layers.Project.Identify) before it
// promotes them, and the run must grade that very project.
func RunProject(p *layers.Project, policy Policy, checkers ...Checker) Report {
	return qualityapp.Run(p, policy, checkers...)
}

// Option is something a run knows about the project beyond its
// snapshot.
type Option func(*layers.Project)

// WithStyles carries the effective style guides the run resolved, by
// locale — what the style layer grades against, and what it has nothing
// to say without.
//
// The server's own snapshot port fills the same map through its own
// Styles port (quality/adapters/snapshot.WithStyles); this is the
// terminal's side of it, so the two surfaces grade one project against
// one set of guides. A locale missing from the map is a locale with no
// mechanical guide, which the layer reads as "nothing to check against"
// — not as a clean bill.
func WithStyles(g map[string]StyleGuide) Option {
	return func(p *layers.Project) {
		if len(g) > 0 {
			p.Styles = g
		}
	}
}

// Project is the snapshot as a layer sees it.
//
// The catalog message IDs come with it. A snapshot read from the server
// carries one per message, and a layer puts it in the finding's locus,
// which is what domain.Fingerprint hashes a finding's identity over: the
// terminal and the server therefore compute the same fingerprint for the
// same finding, and a waiver made against either matches the other.
// A snapshot read from the local catalogs has no IDs, and there a
// finding is fingerprinted by key — the honest answer offline, where no
// catalog said what the key is called.
//
// What a snapshot cannot carry comes in as an Option: the effective
// style guides are resolved per locale against the server, not read out
// of the catalogs, so a run that has them hands them over here.
func Project(s *snapshot.Snapshot, opts ...Option) *layers.Project {
	p := &layers.Project{
		Origin: s.Origin, SourceLocale: s.SourceLocale,
		Translations: make(map[string]map[string]layers.Translation, len(s.Translations)),
	}
	for _, l := range s.Locales {
		p.Locales = append(p.Locales, layers.Locale{Code: l.Code, IsSource: l.IsSource, File: l.File})
	}
	for _, m := range s.Messages {
		p.Messages = append(p.Messages, layers.Message{
			ID: m.ID, Key: m.Key, Namespace: m.Namespace, Revision: m.Revision, Model: m.Model,
			Invalid: invalid(m.Invalid), File: m.File,
			// The authored text, the limit and the description come with
			// the message because the length and source layers read
			// them: a span is in bytes of the authored text,
			// `max-length-exceeded` is computed from the limit rather
			// than waited for, and the source layer reads the
			// description's absence. A local catalog carries none of the
			// three, and there those layers report what they can.
			Text: m.Text, MaxLength: m.MaxLength, Description: m.Description,
		})
	}
	for locale, trs := range s.Translations {
		out := make(map[string]layers.Translation, len(trs))
		for key, t := range trs {
			out[key] = layers.Translation{
				Key: t.Key, Locale: t.Locale, Model: t.Model, State: t.State,
				SourceRevision: t.SourceRevision, Outdated: t.Outdated, Warnings: t.Warnings,
				Invalid: invalid(t.Invalid), File: t.File, Text: t.Text,
			}
		}
		p.Translations[locale] = out
	}
	for _, o := range s.Orphans {
		p.Orphans = append(p.Orphans, layers.Orphan{
			MessageID: o.MessageID, Key: o.Key, Namespace: o.Namespace, Locale: o.Locale, Revision: o.Revision,
		})
	}
	p.MoreOrphans = s.MoreOrphans
	for _, o := range opts {
		o(p)
	}
	return p
}

func invalid(i *snapshot.Invalid) *layers.Invalid {
	if i == nil {
		return nil
	}
	return &layers.Invalid{Code: i.Code, Detail: i.Detail}
}
