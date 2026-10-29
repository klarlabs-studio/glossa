package sources

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// Translations adapts Localization's application service to
// tools.Translations.
type Translations struct{ localization *localizationapp.Service }

// NewTranslations returns the adapter.
func NewTranslations(s *localizationapp.Service) *Translations { return &Translations{localization: s} }

var _ tools.Translations = (*Translations)(nil)

// Translation implements tools.Translations.
func (a *Translations) Translation(
	ctx context.Context, project uuid.UUID, key, locale string,
) (tools.Translation, error) {
	v, err := a.localization.GetTranslation(ctx, project, key, locale)
	if err != nil {
		return tools.Translation{}, notFound(err, localizationapp.ErrNotFound, localizationapp.ErrLocaleNotFound)
	}
	out := tools.Translation{
		MessageID: v.MessageID.String(), Key: key, Locale: v.Locale.String(),
		Text: v.Content.Text, Syntax: string(v.Content.Syntax), State: string(v.State),
		Origin: string(v.Origin), By: v.By, SourceRevision: v.SourceRevision,
		CurrentSourceRevision: v.CurrentSourceRevision, Outdated: v.Outdated(), Revision: v.Revision,
	}
	// Only the codes: the warnings' prose is Quality's to report, and
	// findings_list returns it properly.
	for _, w := range v.Warnings {
		out.Warnings = append(out.Warnings, string(w.Code))
	}
	return out, nil
}

// ProposedState is the review state every agent-written translation
// gets, whatever the project's routing policy says.
//
// This constant is the whole invariant of RFC 0005 §7.4, and it is a
// constant rather than a decision because there is nothing to decide.
// Localization's WritePolicy would otherwise approve new text outright
// in a project that does not require review — which is right for a
// person with `translations.write` and wrong for a token: review is a
// human decision and no scope grants `translations.review`. Asking for
// `needs_review` explicitly is allowed for any writer (it is not a
// reviewer-only state), so this never depends on what the caller may
// do, only on what it is.
const ProposedState = string(localizationdomain.StateNeedsReview)

var (
	_ tools.TranslationWriter = (*Translations)(nil)
	_ tools.LocaleWriter      = (*Translations)(nil)
)

// ProposeTranslation implements tools.TranslationWriter: Localization's
// own translation write, with the review state pinned to
// ProposedState and the provenance recorded as agent-written, with the
// tool that wrote it.
func (a *Translations) ProposeTranslation(
	ctx context.Context, project uuid.UUID, in tools.TranslationProposal,
) (tools.ProposedTranslation, error) {
	current, found, err := a.current(ctx, project, in)
	if err != nil {
		return tools.ProposedTranslation{}, err
	}
	// Proposing exactly what is already stored changes nothing, and must
	// not: sending an approved translation back to review because an
	// agent repeated itself would make an idempotent call destructive.
	// Localization would otherwise read unchanged text plus the state
	// this adapter always asks for as a review decision. A caller whose
	// base_revision is stale is still told so first — it asked to be.
	if found && onCurrent(in, current) && current.Content.Text == in.Text && sameSyntax(in.Syntax, current) {
		return proposed(in, current, localizationapp.WriteUnchanged), nil
	}
	ifMatch := baseRevision(in, current, found)
	state := ProposedState
	v, status, err := a.localization.PutTranslation(ctx, project, in.Key, in.Locale,
		localizationapp.TranslationInput{
			Text: in.Text, Syntax: in.Syntax, SourceRevision: in.SourceRevision,
			// The two fields that make this a proposal and not an edit.
			State: &state,
			// `agent`, not `ai` (RFC 0005 §7.3): an autonomous agent
			// writing through a long-lived token is a different act from
			// a person clicking "translate with AI", and the revision log
			// has to be able to say which without reading JSON.
			Origin: string(localizationdomain.OriginAgent),
			// The detail says which agent surface and tool the text
			// arrived through, never who the model was: no provider
			// crosses MCP (RFC 0005 §7.4).
			OriginDetail: json.RawMessage(`{"via":"mcp","tool":"translation_propose"}`),
		}, ifMatch)
	if err != nil {
		return tools.ProposedTranslation{}, proposalError(err)
	}
	return proposed(in, v, status), nil
}

// proposed renders a written (or unchanged) translation.
func proposed(
	in tools.TranslationProposal, v localizationapp.TranslationView, status localizationapp.WriteStatus,
) tools.ProposedTranslation {
	out := tools.ProposedTranslation{
		Translation: tools.Translation{
			MessageID: v.MessageID.String(), Key: in.Key, Locale: v.Locale.String(),
			Text: v.Content.Text, Syntax: string(v.Content.Syntax), State: string(v.State),
			Origin: string(v.Origin), By: v.By, SourceRevision: v.SourceRevision,
			CurrentSourceRevision: v.CurrentSourceRevision, Outdated: v.Outdated(), Revision: v.Revision,
		},
		Status: string(status),
	}
	for _, w := range v.Warnings {
		out.Warnings = append(out.Warnings, string(w.Code))
	}
	return out
}

// current reads the translation that stands now, if there is one.
func (a *Translations) current(
	ctx context.Context, project uuid.UUID, in tools.TranslationProposal,
) (localizationapp.TranslationView, bool, error) {
	v, err := a.localization.GetTranslation(ctx, project, in.Key, in.Locale)
	if errors.Is(err, localizationapp.ErrNotFound) {
		return localizationapp.TranslationView{}, false, nil
	}
	if err != nil {
		return localizationapp.TranslationView{}, false, proposalError(err)
	}
	return v, v.Revision > 0, nil
}

// sameSyntax reports whether the proposal is in the stored text's own
// syntax. "" means the project's default, which is what the stored text
// was written in unless it said otherwise.
func sameSyntax(asked string, current localizationapp.TranslationView) bool {
	return asked == "" || asked == string(current.Content.Syntax)
}

// onCurrent reports whether the proposal's own optimistic lock, if it
// brought one, still matches what stands.
func onCurrent(in tools.TranslationProposal, current localizationapp.TranslationView) bool {
	return in.BaseRevision == nil || *in.BaseRevision == current.Revision
}

// baseRevision turns the proposal's optimistic lock into Localization's
// If-Match. An agent that read the translation first passes the
// revision it saw and is told when the text moved on; one that did not
// is proposing onto whatever stands now, so the revision that stands
// now is used. Either way the write itself is guarded, and a proposal
// never silently replaces a newer one it never saw.
func baseRevision(
	in tools.TranslationProposal, current localizationapp.TranslationView, found bool,
) *int {
	if in.BaseRevision != nil {
		return in.BaseRevision
	}
	if !found {
		// Nothing stored: the write must create, which If-Match absent
		// is exactly what says.
		return nil
	}
	rev := current.Revision
	return &rev
}

// proposalError maps Localization's refusals. A precondition failure is
// the agent's to retry after re-reading; a locale the project does not
// have, or a message it does not know, is a not-found like any other.
func proposalError(err error) error {
	switch {
	case errors.Is(err, localizationapp.ErrPreconditionFailed),
		errors.Is(err, localizationapp.ErrPreconditionRequired),
		errors.Is(err, localizationapp.ErrStaleVersion),
		errors.Is(err, localizationapp.ErrConcurrentWrite):
		return &app.InvalidArgumentError{
			Argument: "base_revision",
			Reason:   "the translation changed since it was read; read it again with translation_get and propose onto it",
		}
	case errors.Is(err, localizationdomain.ErrSourceLocale):
		return &app.InvalidArgumentError{
			Argument: "locale", Reason: "is the project's source locale, which is written with message_upsert",
		}
	}
	return notFound(err, localizationapp.ErrNotFound, localizationapp.ErrLocaleNotFound)
}

// AddLocale implements tools.LocaleWriter.
func (a *Translations) AddLocale(ctx context.Context, project uuid.UUID, code string) (tools.AddedLocale, error) {
	l, created, err := a.localization.AddLocale(ctx, project, code)
	if err != nil {
		return tools.AddedLocale{}, notFound(err, localizationapp.ErrNotFound)
	}
	return tools.AddedLocale{
		Locale: l.Code.String(), Direction: string(l.Direction()), Created: created,
	}, nil
}
