// Package snapshot adapts Catalog's and Localization's application
// services to Quality's Snapshot port: the project as the layers read
// it, plus the policy that grades them.
//
// It reads the two snapshots the release build already reads —
// Catalog's active source and Localization's translations — through
// their own application services, as RFC 0002 §4 requires. Each keeps
// checking the caller's permissions, so a check runs with exactly what
// its caller could have read through the API and nothing more: an MCP
// session on a read-only token, `catalog.read` and `translations.read`,
// sees the same project a person with those permissions sees.
//
// Nothing here decides anything. The layers are Quality's, the policy
// is the project's, and this package only puts the catalog in front of
// them in the shape they expect.
package snapshot

import (
	"context"
	"errors"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// Origin says where a checked project was read from. `glossa check`
// reports "local"; a run on the server reports this.
const Origin = "server"

// Styles resolves a locale's effective style guide, reduced to the
// mechanical fields the style layer grades (RFC 0005 §3.2). It is
// optional: a server built without it runs every other layer, and the
// style layer reports nothing because there is nothing to check
// against — which the run says, by naming the layer it ran.
//
// *style.Port satisfies it.
type Styles interface {
	EffectiveStyle(ctx context.Context, project uuid.UUID, locale string) (layers.StyleGuide, bool)
}

// Port implements app.Snapshot over the contexts that hold a project's
// text and the rules it is written to.
type Port struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	styles       Styles
}

// Option configures a Port.
type Option func(*Port)

// WithStyles gives the port the effective style guides, which is what
// the style layer has nothing to say without.
func WithStyles(s Styles) Option {
	return func(p *Port) { p.styles = s }
}

// New returns the port.
func New(c *catalogapp.Service, l *localizationapp.Service, opts ...Option) *Port {
	p := &Port{catalog: c, localization: l}
	for _, o := range opts {
		o(p)
	}
	return p
}

var _ app.Snapshot = (*Port)(nil)

// Snapshot implements app.Snapshot.
func (p *Port) Snapshot(ctx context.Context, project uuid.UUID) (app.ProjectSnapshot, error) {
	src, err := p.catalog.ReleaseSource(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return app.ProjectSnapshot{}, notFound(err)
	}
	// Every review state but rejected: a check grades what the project
	// has, including text still waiting for a reviewer, exactly as
	// `glossa check` grades the catalogs on disk. Rejected text is not
	// a translation, and the completeness layer must see its absence.
	//
	// LiveTranslations and not ReleaseTranslations: the obsolete
	// messages' translations are read on their own below, one bounded
	// page of them, rather than all of them here only to be dropped.
	trs, err := p.localization.LiveTranslations(ctx, project, usableStates)
	if err != nil {
		return app.ProjectSnapshot{}, notFound(err)
	}
	out := &layers.Project{
		Origin:       Origin,
		SourceLocale: trs.SourceLocale.String(),
		Translations: make(map[string]map[string]layers.Translation, len(trs.Translations)),
	}
	for _, l := range trs.Locales {
		out.Locales = append(out.Locales, layers.Locale{Code: l.Code.String(), IsSource: l.IsSource})
		if l.IsSource || p.styles == nil {
			continue
		}
		// A guide that states no mechanical rule is not recorded, so the
		// style layer can tell a locale it has rules for from one it
		// does not. A resolution that fails is the same case: the layer
		// says nothing rather than grading against a guide it could not
		// read.
		if g, ok := p.styles.EffectiveStyle(ctx, project, l.Code.String()); ok {
			if out.Styles == nil {
				out.Styles = map[string]layers.StyleGuide{}
			}
			out.Styles[l.Code.String()] = g
		}
	}
	keys := make(map[uuid.UUID]catalogdomain.Message, len(src.Messages))
	for _, m := range src.Messages {
		keys[uuid.UUID(m.ID)] = m
		out.Messages = append(out.Messages, layers.Message{
			ID: uuid.UUID(m.ID).String(), Key: string(m.Key), Namespace: string(m.Namespace),
			Revision: m.Revision, Model: model(m.Source.Model), Text: m.Source.Text,
			// The limit comes with the message because the length layer
			// computes `max-length-exceeded` from it rather than waiting
			// for the warning the server stored at write time (RFC 0005
			// §3.3), and the description because the source layer reads
			// its absence (§3.5).
			MaxLength: maxLength(m.MaxLength), Description: m.Description,
		})
	}
	for locale, byID := range trs.Translations {
		texts := make(map[string]layers.Translation, len(byID))
		for id, v := range byID {
			// A translation of a message the source snapshot does not
			// carry is not part of what ships. The ones of obsolete
			// messages are read apart, as orphans; what is left here is
			// a message the projection has not caught up with.
			m, ok := keys[id]
			if !ok {
				continue
			}
			texts[string(m.Key)] = translation(string(m.Key), locale.String(), m.Revision, v)
		}
		out.Translations[locale.String()] = texts
	}
	if err := p.orphans(ctx, project, out); err != nil {
		return app.ProjectSnapshot{}, notFound(err)
	}
	return app.ProjectSnapshot{Project: out, Policy: src.Project.Settings.Policy()}, nil
}

// usableStates are the review states a check grades: every one but
// rejected. `glossa check` reads the same three (cli/snapshot).
var usableStates = []localizationdomain.ReviewState{
	localizationdomain.StateDraft, localizationdomain.StateNeedsReview, localizationdomain.StateApproved,
}

// orphans reads the translations of messages the catalog has obsoleted,
// which the completeness layer reports as `unknown-key`.
//
// It is bounded: one page of layers.MaxOrphans per chunk of locales,
// from Localization's own translation listing, in its (key, message ID,
// locale) order, never followed past the first page. That is exactly
// the read `glossa check` makes through the API (cli/snapshot), so the
// two surfaces read the same orphans and the layer cuts them the same
// way. A project that has obsoleted thousands of messages costs a check
// one page per twenty locales, not thousands of rows.
func (p *Port) orphans(ctx context.Context, project uuid.UUID, out *layers.Project) error {
	var targets []string
	for _, l := range out.TargetLocales() {
		targets = append(targets, l.Code)
	}
	states := make([]string, len(usableStates))
	for i, s := range usableStates {
		states[i] = string(s)
	}
	obsolete := "obsolete"
	for start := 0; start < len(targets); start += localizationapp.MaxListedLocales {
		chunk := targets[start:min(start+localizationapp.MaxListedLocales, len(targets))]
		views, next, err := p.localization.ListProjectTranslations(ctx, project, localizationapp.TranslationFilter{
			Locales: chunk, States: states, MessageState: &obsolete,
		}, pagination.Page{Size: layers.MaxOrphans})
		if err != nil {
			return err
		}
		if next != nil {
			out.MoreOrphans = true
		}
		for _, v := range views {
			out.Orphans = append(out.Orphans, layers.Orphan{
				MessageID: v.MessageID.String(), Key: v.Key, Namespace: v.Namespace,
				Locale: v.Locale.String(), Revision: v.ID.String(),
			})
		}
	}
	return nil
}

// translation is one stored translation as a layer reads it.
func translation(key, locale string, sourceRevision int, v localizationapp.TranslationView) layers.Translation {
	t := layers.Translation{
		Key: key, Locale: locale, Model: model(v.Content.Model), State: string(v.State),
		Revision: v.ID.String(), SourceRevision: v.SourceRevision,
		Outdated: v.Translation.Outdated(sourceRevision),
		// The authored text travels with the model because a finding's
		// span is in bytes of *it* — the string a person edits — and not
		// of the concatenation of the model's text elements.
		Text: v.Content.Text,
	}
	// The warnings Localization stored with the text are findings only
	// it can compute (max-length among them), and the parity layer
	// reports them rather than recomputing them.
	for _, w := range v.Warnings {
		t.Warnings = append(t.Warnings, w)
	}
	return t
}

// maxLength is the message's limit in characters, 0 for none.
func maxLength(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// model returns the parsed message, or nil where there is none — a
// layer reads a nil model as text it cannot check. Stored content is
// restored with its model, so nil here means the row never had one.
func model(m mf.Message) *mf.Message {
	if m.Type == "" {
		return nil
	}
	return &m
}

// notFound translates the contexts' own not-found errors into Quality's.
func notFound(err error) error {
	if errors.Is(err, catalogapp.ErrNotFound) || errors.Is(err, localizationapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}
