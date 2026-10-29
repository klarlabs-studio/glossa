// Package sources adapts Catalog's and Localization's application
// services to Release's Source port: the only way Release reads what a
// project says (RFC 0002 §4). It is Release's anti-corruption layer:
// nothing past it sees the other contexts' types.
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	localizationdomain "go.klarlabs.de/glossa/platform/internal/localization/domain"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Port implements app.Source.
type Port struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
}

// New returns the port.
func New(catalog *catalogapp.Service, localization *localizationapp.Service) *Port {
	return &Port{catalog: catalog, localization: localization}
}

var _ app.Source = (*Port)(nil)

func notFound(err error) error {
	if errors.Is(err, catalogapp.ErrNotFound) || errors.Is(err, localizationapp.ErrNotFound) {
		return app.ErrNotFound
	}
	return err
}

// CheckProject implements app.Source.
func (p *Port) CheckProject(ctx context.Context, project uuid.UUID) error {
	_, err := p.catalog.GetProject(ctx, catalogdomain.ProjectID(project))
	return notFound(err)
}

// CheckPolicy implements app.Source: the check-policy document the
// project stores, which is where the publish gate's requirements come
// from (RFC 0005 §4.1). It is an ordinary authorized Catalog use case,
// so Release learns nothing its caller could not read through the API,
// and a project that stored no document reads as checkpolicy's default.
func (p *Port) CheckPolicy(ctx context.Context, project uuid.UUID) (checkpolicy.Policy, error) {
	pr, err := p.catalog.GetProject(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return checkpolicy.Policy{}, notFound(err)
	}
	return pr.Settings.Policy(), nil
}

// Snapshot implements app.Source: Catalog's active messages joined with
// Localization's translations in states, by message ID. Translations of
// messages the catalog doesn't release (obsolete, proposed on a branch,
// or not yet known) are left out by the build, and so is every branch's
// overlay: a source proposal is never a message's source.
func (p *Port) Snapshot(ctx context.Context, project uuid.UUID, states []string) (domain.Snapshot, error) {
	reviewStates := make([]localizationdomain.ReviewState, 0, len(states))
	for _, s := range states {
		rs, err := localizationdomain.ParseReviewState(s)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("release: policy state: %w", err)
		}
		reviewStates = append(reviewStates, rs)
	}
	src, err := p.catalog.ReleaseSource(ctx, catalogdomain.ProjectID(project))
	if err != nil {
		return domain.Snapshot{}, notFound(err)
	}
	tr, err := p.localization.ReleaseTranslations(ctx, project, reviewStates)
	if err != nil {
		return domain.Snapshot{}, notFound(err)
	}
	snap := domain.Snapshot{
		SourceLocale: src.Project.SourceLocale.String(),
		Fallback:     tr.Fallback,
		Translations: map[string]map[uuid.UUID]domain.Translation{},
	}
	for _, l := range tr.Locales {
		snap.Locales = append(snap.Locales, domain.Locale{Code: l.Code.String(), Direction: string(l.Direction)})
	}
	for _, m := range src.Messages {
		snap.Messages = append(snap.Messages, domain.SourceMessage{
			ID: m.ID.UUID(), Key: string(m.Key), Namespace: string(m.Namespace), Model: m.Source.ModelJSON(),
			Proposed: m.State == catalogdomain.MessageProposed,
		})
	}
	for locale, byMessage := range tr.Translations {
		// Translations of every message the project has, proposed ones
		// included: only a branch build ships those.
		out := make(map[uuid.UUID]domain.Translation, len(byMessage))
		for id, t := range byMessage {
			out[id] = domain.Translation{Model: t.Content.ModelJSON(), Outdated: t.Outdated()}
		}
		snap.Translations[locale.String()] = out
	}
	return snap, nil
}

// BranchesProposing implements app.Source: the names of the open
// branches whose overlay holds the message (a new key of theirs, or a
// source change they propose). Their previews show its translations.
func (p *Port) BranchesProposing(ctx context.Context, project, message uuid.UUID) ([]string, error) {
	names, err := p.catalog.OpenBranchesProposing(ctx, catalogdomain.ProjectID(project), catalogdomain.MessageID(message))
	if err != nil {
		return nil, notFound(err)
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out, nil
}

// BranchOverlay implements app.Source: the branch's new keys whose
// messages are still proposed (a key merged since is live and in the
// snapshot already), with the source this branch pushed, and its source
// proposals for live messages. A branch the catalog doesn't know yet
// (its environment opened before its first push) proposes nothing.
func (p *Port) BranchOverlay(ctx context.Context, project uuid.UUID, branch string) (domain.Overlay, error) {
	o, err := p.catalog.BranchOverlay(ctx, catalogdomain.ProjectID(project), branch)
	if errors.Is(err, catalogapp.ErrNotFound) {
		return domain.Overlay{}, nil
	}
	if err != nil {
		return domain.Overlay{}, err
	}
	out := domain.Overlay{Changes: map[uuid.UUID]json.RawMessage{}}
	for _, pr := range o.Proposals {
		m, ok := o.Messages[pr.MessageID]
		if !ok {
			continue
		}
		switch {
		case pr.Kind == catalogdomain.ProposalNewKey && m.State == catalogdomain.MessageProposed:
			out.Proposed = append(out.Proposed, domain.SourceMessage{
				ID: m.ID.UUID(), Key: string(pr.Key), Namespace: string(m.Namespace), Model: pr.Source.ModelJSON(),
			})
		case pr.Kind == catalogdomain.ProposalSourceChange && m.State == catalogdomain.MessageActive:
			out.Changes[m.ID.UUID()] = pr.Source.ModelJSON()
		}
	}
	return out, nil
}
