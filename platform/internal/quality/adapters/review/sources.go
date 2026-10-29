package review

import (
	"context"
	"errors"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// The ports, over the application services that own the data.
//
// Every read below is an ordinary authorized use case of the context it
// belongs to, so a review sees exactly what its caller could have read
// through the API and nothing more (RFC 0002 §4). Nothing here touches
// another context's tables, and nothing here decides anything: the
// expansion is in review.go, the opinions are the model's, and the
// grading is the policy's.

// NewCatalog returns the Catalog port over Catalog's service.
func NewCatalog(svc *catalogapp.Service) Catalog { return &catalogPort{svc: svc} }

type catalogPort struct{ svc *catalogapp.Service }

var _ Catalog = (*catalogPort)(nil)

// Messages implements Catalog with the project's releasable source: the
// active messages, in key order, with the ID a finding's fingerprint is
// hashed over. Obsolete messages are not reviewed, for the same reason
// they are not released — nothing ships them, so nobody reads them.
//
// The source text is the canonical MF2 rendering of the stored model,
// not the text as authored: a catalog written in MF1 is reviewed in the
// one syntax the layer is defined over (RFC 0002 §5).
func (p *catalogPort) Messages(ctx context.Context, project uuid.UUID) ([]Message, error) {
	src, err := p.svc.ReleaseSource(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return nil, notFound(err)
	}
	out := make([]Message, 0, len(src.Messages))
	for _, m := range src.Messages {
		text, err := mf.Stringify(m.Source.Model)
		// A message whose stored model will not render is one the parity
		// layer already reports. It is left out rather than sent to a
		// model as an empty string, which would buy an opinion about
		// nothing.
		if err != nil {
			continue
		}
		out = append(out, Message{
			ID: uuid.UUID(m.ID).String(), Key: string(m.Key), Namespace: string(m.Namespace),
			Description: m.Description, Revision: m.Revision, Source: text,
		})
	}
	return out, nil
}

// NewLocalization returns the Localization port over Localization's
// service.
func NewLocalization(svc *localizationapp.Service) Localization { return &localizationPort{svc: svc} }

type localizationPort struct{ svc *localizationapp.Service }

var _ Localization = (*localizationPort)(nil)

// reviewable are the review states a linguistic review looks at: every
// state but rejected, exactly as the server-side check reads them. Text
// still waiting for a reviewer is text somebody may ship, and telling
// them about it before they approve it is the point of the layer.
var reviewable = []localizationdomain.ReviewState{
	localizationdomain.StateDraft, localizationdomain.StateNeedsReview, localizationdomain.StateApproved,
}

// Translations implements Localization, keyed by catalog message ID so
// the expansion can join it with Catalog's messages without either
// context knowing the other's keys.
func (p *localizationPort) Translations(
	ctx context.Context, project uuid.UUID, locales []string,
) (Translated, error) {
	snap, err := p.svc.ReleaseTranslations(ctx, project, reviewable)
	if err != nil {
		return Translated{}, notFound(err)
	}
	want := make(map[string]bool, len(locales))
	for _, l := range locales {
		want[l] = true
	}
	out := Translated{SourceLocale: snap.SourceLocale.String(), ByLocale: map[string]map[string]Translation{}}
	for locale, byID := range snap.Translations {
		code := locale.String()
		if !want[code] {
			continue
		}
		texts := make(map[string]Translation, len(byID))
		for id, v := range byID {
			text, err := mf.Stringify(v.Content.Model)
			if err != nil {
				continue
			}
			texts[id.String()] = Translation{
				Text: text, Revision: v.ID.String(), SourceRevision: v.SourceRevision,
			}
		}
		out.ByLocale[code] = texts
	}
	return out, nil
}

// NewIntelligence returns the Intelligence port over Intelligence's
// service.
func NewIntelligence(svc *intelligenceapp.Service) Intelligence { return &intelligencePort{svc: svc} }

type intelligencePort struct{ svc *intelligenceapp.Service }

var _ Intelligence = (*intelligencePort)(nil)

// ProviderConsent implements Intelligence with the tenant's own
// setting, read where it is kept (RFC 0003 §7).
func (p *intelligencePort) ProviderConsent(ctx context.Context) (bool, error) {
	s, err := p.svc.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	return s.ProviderConsent, nil
}

// Layer implements Intelligence by building the linguistic layer over
// the tenant's routing runtime: its providers, its policy, its prices
// and the monthly budget guard that bounds the whole review
// (RFC 0005 §10). It is built per call because a router is per tenant
// and a tenant is per request.
func (p *intelligencePort) Layer(ctx context.Context, project uuid.UUID) (Layer, error) {
	router, err := p.svc.LinguisticRouter(ctx, project)
	if err != nil {
		return nil, err
	}
	return linguistic.New(router)
}

// notFound translates the contexts' own not-found errors into
// Quality's, as the snapshot adapter beside this one does.
func notFound(err error) error {
	if errors.Is(err, catalogapp.ErrNotFound) || errors.Is(err, localizationapp.ErrNotFound) {
		return app.ErrProjectNotFound
	}
	return err
}
