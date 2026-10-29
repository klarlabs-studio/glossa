package cli

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/cli/qa"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// `glossa check`'s effective style guides (RFC 0005 §3.2).
//
// The style layer grades a translation against the mechanical half of
// its locale's effective style guide: the formality, the quotation
// marks, the dash, the ellipsis, the unit space, the number and date
// conventions the project states beyond CLDR. That guide is a merge of
// every guide in scope, and only the server can compute it — there is no
// copy of it in the catalogs, and there is nothing in a JSON file to
// derive it from.
//
// So the run fetches it, one read per target locale, and carries it into
// the project it grades. This is the terminal's side of what the server
// already does for its own runs: quality/adapters/snapshot fills
// `Project.Styles` through a Styles port, and without the same map here
// `layers.Style.Check` finds no guide for any locale and returns before
// it reads a word.
//
// ── what to do about not knowing ────────────────────────────────────
//
// Two things look alike and are not, and §12.2 turns on telling them
// apart (intent §41: a layer that cannot run must never read as one that
// passed).
//
//   - A locale whose effective guide states no mechanical rule — a guide
//     of nothing but prose, or no guide at all. That is a real answer
//     about that locale: the layer ran, had nothing to check it against,
//     and reported nothing. It is not a gap, and a project that keeps no
//     style guides must not exit 4 forever because of it.
//
//   - No way to ask. Offline, or against a server that could not be
//     reached, the guides cannot be resolved at all. The layer did not
//     run, and reporting nothing would be a check claiming to have
//     graded style it never read. That is `unavailable_layers` and exit
//     4, the mechanism wave 3 built for exactly this.
//
// The first is decided by the guide itself (qa.StyleGuideOf answers
// false where nothing mechanical is stated, the same predicate the
// server's port uses). The second is decided here.

// readStyles resolves the effective style guide for each target locale
// and carries it into the run, or names the layer as one this run could
// not compute.
//
// It is a no-op for a run that was never going to compute the layer:
// `--layer` left it out, or a policy rule switched it off. A layer
// nobody asked for has not been lost.
func (inv *invocation) readStyles(ctx context.Context, run *checkSubject, f checkFlags, policy checkpolicy.Policy) {
	if !f.wantsLayer(domain.LayerStyle) || !domain.Computes(policy, "", domain.LayerStyle) {
		return
	}
	if run.client == nil {
		run.unavailable = append(run.unavailable, unavailableJSON{
			Layer: domain.LayerStyle, Why: run.noStyleServerWhy()})
		return
	}
	styles := map[string]qa.StyleGuide{}
	for _, l := range run.snapshot.TargetLocales() {
		// The namespace is left empty on purpose, as it is on the
		// server: the layer grades a project's locales, and narrowing to
		// one namespace's guide would give it to every message in the
		// project, which is worse than the project's own.
		g, err := run.client.EffectiveStyle(ctx, run.scope.Tenant,
			remote.StyleScope{Project: run.scope.Project, Locale: l.Code})
		if err != nil {
			// One locale's guide that could not be read makes the
			// layer's answer incomplete, and `unavailable_layers` names
			// layers, not locales. Naming the whole layer is the honest
			// answer at the mechanism's granularity — a partial style
			// report that reads as a complete one is the failure mode
			// §4.4 exists to prevent.
			run.unavailable = append(run.unavailable, unavailableJSON{Layer: domain.LayerStyle,
				Why: "the effective style guide for " + l.Code + " couldn't be read: " + asError(err).Error()})
			return
		}
		if guide, ok := qa.StyleGuideOf(g); ok {
			styles[l.Code] = guide
		}
	}
	run.styles = styles
}

// noStyleServerWhy says why a run with no server has no guides.
func (s *checkSubject) noStyleServerWhy() string {
	if s.degraded {
		return "the server was out of reach, so the project's style guides could not be read"
	}
	return "the effective style guide is the server's, and this run has no server"
}
