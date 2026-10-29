package layers

import (
	"fmt"
	"sort"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Completeness checks that every active message has a translation in
// every locale — required locales (the policy's missing_translations,
// error by default) and others (warning) — made against the current
// source (outdated: warning), and that no translation names a message
// the catalog does not have — a key missing from the source catalog
// offline, a message the catalog has obsoleted on the server.
type Completeness struct{}

// Layer implements Checker.
func (Completeness) Layer() domain.Layer { return domain.LayerCompleteness }

// Check implements Checker.
func (Completeness) Check(p *Project, policy checkpolicy.Policy) []domain.Finding {
	var out []domain.Finding
	have := map[string]bool{}
	for _, l := range p.Locales {
		have[l.Code] = true
	}
	for _, l := range policy.RequireComplete {
		if have[l] {
			continue
		}
		out = append(out, domain.New(domain.Finding{
			Layer: domain.LayerCompleteness, Code: checkpolicy.CodeMissingLocale, Severity: domain.Error,
			Locus: domain.Locus{Locale: l}, Message: "required locale isn't in the project",
		}))
	}
	for _, l := range p.TargetLocales() {
		// The policy decides, not the required list alone: a project can
		// require every locale and still only warn about untranslated
		// keys (checkpolicy.Policy.MissingTranslations).
		severity := policy.Severity(l.Code)
		trs := p.Translations[l.Code]
		for _, m := range p.Messages {
			t, ok := trs[m.Key]
			switch {
			case !ok || t.State == "rejected":
				message := "missing translation"
				if ok {
					message = "translation rejected in review"
				}
				out = append(out, domain.New(domain.Finding{
					Layer: domain.LayerCompleteness, Code: checkpolicy.CodeMissingTranslation, Severity: severity,
					Locus: domain.Locus{
						Message: m.ID, Key: m.Key, Locale: l.Code, Namespace: m.Namespace, File: l.File,
					},
					Message: message,
				}))
			case t.Outdated:
				out = append(out, domain.New(domain.Finding{
					Layer: domain.LayerCompleteness, Code: checkpolicy.CodeOutdatedTranslation, Severity: domain.Warning,
					Locus: domain.Locus{
						Message: m.ID, Key: m.Key, Locale: l.Code, Namespace: m.Namespace, Revision: t.Revision,
					},
					Message:        "made against an older source revision; the source changed since",
					SourceRevision: sourceRevision(t),
					Fix:            &domain.Fix{Kind: domain.FixAdoptSourceChange},
				}))
			}
		}
		for _, t := range p.SortedTranslations(l.Code) {
			if _, ok := p.Message(t.Key); ok {
				continue
			}
			out = append(out, domain.New(domain.Finding{
				Layer: domain.LayerCompleteness, Code: checkpolicy.CodeUnknownKey, Severity: domain.Warning,
				Locus:   domain.Locus{Key: t.Key, Locale: l.Code, Revision: t.Revision, File: t.File},
				Message: "no source message has this ID",
			}))
		}
	}
	return append(out, orphaned(p)...)
}

// orphaned reports the translations whose message the catalog has
// obsoleted, which is the server's shape of `unknown-key`: text for a
// key no active message has.
//
// Each is a warning, like every other `unknown-key`. RFC 0005 §3.6 keeps
// the completeness layer's severities as they were, and nothing this
// layer is given says whether the product still asks for the key — that
// is Context's, and it arrives after the run, when the pull request
// locates the finding at the usage (integration/app `locate`), which may
// place a finding and never regrade one. A project that wants a key its
// code still uses to fail the build raises the code by rule:
// `{layer: completeness, code: unknown-key, severity: error}`.
//
// The locus carries the obsolete message's ID, so the fingerprint is the
// one every surface computes and a waiver made in the terminal holds on
// the pull request.
func orphaned(p *Project) []domain.Finding {
	targets := map[string]bool{}
	for _, l := range p.TargetLocales() {
		targets[l.Code] = true
	}
	var keep []Orphan
	for _, o := range p.Orphans {
		// A key an active message has since taken is that message's,
		// and a locale the project dropped is not one it ships: neither
		// is a key the product asks for in vain.
		if _, active := p.Message(o.Key); active || !targets[o.Locale] {
			continue
		}
		keep = append(keep, o)
	}
	sort.Slice(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if a.MessageID != b.MessageID {
			return a.MessageID < b.MessageID
		}
		return a.Locale < b.Locale
	})
	more := p.MoreOrphans || len(keep) > MaxOrphans
	if len(keep) > MaxOrphans {
		keep = keep[:MaxOrphans]
	}
	out := make([]domain.Finding, 0, len(keep)+1)
	for _, o := range keep {
		out = append(out, domain.New(domain.Finding{
			Layer: domain.LayerCompleteness, Code: checkpolicy.CodeUnknownKey, Severity: domain.Warning,
			Locus: domain.Locus{
				Message: o.MessageID, Key: o.Key, Locale: o.Locale, Namespace: o.Namespace, Revision: o.Revision,
			},
			Message: "translation of a message the catalog has obsoleted: no active message has this key",
		}))
	}
	if more {
		out = append(out, domain.New(domain.Finding{
			Layer: domain.LayerCompleteness, Code: checkpolicy.CodeUnknownKey, Severity: domain.Warning,
			Message: fmt.Sprintf("more than %d translations belong to messages the catalog has obsoleted; "+
				"the check reports the first %d by key", MaxOrphans, MaxOrphans),
		}))
	}
	return out
}
