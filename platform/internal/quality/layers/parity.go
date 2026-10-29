package layers

import (
	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Parity checks that every translation is structurally compatible with
// its current source — arguments, selectors, plural categories for the
// target locale and markup — plus the other warnings the server stored
// that only it can compute.
//
// It was called `arguments` in M1 and keeps every one of its codes,
// which stay the MessageFormat kernel's (RFC 0005 §3.1).
//
// One code left in M4: `max-length-exceeded`, which this layer surfaced
// from the stored warning because it was the only layer there was. RFC
// 0005 §3.3 puts it in `length`, and the Length layer both recomputes
// it from the message's own constraint and relays the stored warning
// where the caller has no constraint. Relaying it here too would give a
// project two findings for one problem under two layers, so parity
// stops at the boundary: the kernel's compat codes are its, and
// everything else the server stored goes to the layer that owns it.
type Parity struct{}

// Layer implements Checker.
func (Parity) Layer() domain.Layer { return domain.LayerParity }

// Check implements Checker.
func (Parity) Check(p *Project, _ checkpolicy.Policy) []domain.Finding {
	var out []domain.Finding
	for _, l := range p.TargetLocales() {
		for _, t := range p.SortedTranslations(l.Code) {
			m, ok := p.Message(t.Key)
			if !ok || m.Model == nil || t.Model == nil || t.State == "rejected" {
				continue
			}
			seen := map[string]bool{}
			for _, f := range mf.CheckCompat(*m.Model, *t.Model, l.Code) {
				seen[string(f.Code)] = true
				out = append(out, FromKernel(f, l.Code, m, t))
			}
			// A warning the kernel cannot recompute here is reported as
			// the server left it — except the ones another layer owns,
			// which that layer relays instead.
			for _, w := range t.Warnings {
				if !seen[string(w.Code)] && !ownedElsewhere(string(w.Code)) {
					out = append(out, FromKernel(w, l.Code, m, t))
				}
			}
		}
	}
	return out
}

// ownedElsewhere reports whether a stored warning belongs to a layer
// other than parity. It is one list, so a code cannot be relayed twice
// by two layers that each thought they were the only one.
func ownedElsewhere(code string) bool { return code == CodeMaxLengthExceeded }

// FromKernel is a MessageFormat finding as the parity layer reports it.
//
// It is a constructor and not a conversion: the kernel's subject and
// qualifier survive, and the locus says which message, which locale and
// which revision — all of which the conversion this replaces dropped on
// the way to the server or to the CLI.
func FromKernel(f mf.Finding, locale string, m *Message, t Translation) domain.Finding {
	severity := domain.Warning
	if f.Severity == mf.SeverityError {
		severity = domain.Error
	}
	locus := domain.Locus{
		Key: t.Key, Locale: locale, Revision: t.Revision, File: t.File,
	}
	if m != nil {
		locus.Message, locus.Namespace = m.ID, m.Namespace
	}
	return domain.New(domain.Finding{
		Layer: domain.LayerParity, Code: string(f.Code), Severity: severity, Locus: locus,
		Message: f.Message, Subject: f.Subject, Detail: f.Detail, SourceRevision: sourceRevision(t),
	})
}
