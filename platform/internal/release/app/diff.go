package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// ReleaseDiff is how head differs from base, locale by locale.
type ReleaseDiff struct {
	// Base is the zero Release when head has nothing to compare with
	// (its environment's first release): everything is added.
	Base    domain.Release
	Head    domain.Release
	Locales []domain.LocaleDiff
}

// Diff compares two releases of a project: which message IDs each locale
// added, changed or removed. base nil means head's parent. It reads the
// releases' artifacts from storage; artifacts both share (same hash) are
// read once and compare equal without parsing.
func (s *Service) Diff(ctx context.Context, project, head uuid.UUID, base *uuid.UUID) (ReleaseDiff, error) {
	if _, err := s.checkProject(ctx, project, authz.ReleasesRead); err != nil {
		return ReleaseDiff{}, err
	}
	var d ReleaseDiff
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if d.Head, err = st.Release(ctx, project, head); err != nil {
			return err
		}
		baseID := d.Head.Parent
		if base != nil {
			baseID = *base
		}
		if baseID == uuid.Nil {
			return nil
		}
		d.Base, err = st.Release(ctx, project, baseID)
		if isNotFound(err) {
			return ErrReleaseNotInProject
		}
		return err
	})
	if err != nil {
		return ReleaseDiff{}, err
	}
	cache := map[string]domain.LocaleMessages{}
	baseMsgs, err := s.messages(ctx, project, d.Base, cache)
	if err != nil {
		return ReleaseDiff{}, err
	}
	headMsgs, err := s.messages(ctx, project, d.Head, cache)
	if err != nil {
		return ReleaseDiff{}, err
	}
	d.Locales = domain.Diff(localeCodes(d.Base), localeCodes(d.Head), baseMsgs, headMsgs)
	return d, nil
}

func localeCodes(r domain.Release) []string {
	out := make([]string, len(r.Content.Locales))
	for i, l := range r.Content.Locales {
		out[i] = l.Code
	}
	return out
}

// messages loads what r ships per locale, all namespaces merged.
func (s *Service) messages(ctx context.Context, project uuid.UUID, r domain.Release, cache map[string]domain.LocaleMessages) (map[string]domain.LocaleMessages, error) {
	out := map[string]domain.LocaleMessages{}
	for locale, namespaces := range r.Content.Artifacts {
		merged := domain.LocaleMessages{}
		for _, ref := range namespaces {
			msgs, err := s.artifact(ctx, project, ref, cache)
			if err != nil {
				return nil, err
			}
			for id, m := range msgs {
				merged[id] = m
			}
		}
		out[locale] = merged
	}
	return out, nil
}

func (s *Service) artifact(ctx context.Context, project uuid.UUID, ref domain.ArtifactRef, cache map[string]domain.LocaleMessages) (domain.LocaleMessages, error) {
	if m, ok := cache[ref.SHA256]; ok {
		return m, nil
	}
	body, err := s.objects.Get(ctx, delivery.ArtifactPath(project.String(), ref.SHA256), delivery.MaxArtifactBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: artifact %s: %v", ErrStorage, ref.SHA256, err)
	}
	if delivery.Digest(body) != ref.SHA256 {
		return nil, fmt.Errorf("%w: artifact %s doesn't match its hash", ErrStorage, ref.SHA256)
	}
	msgs, err := domain.ArtifactMessages(body)
	if err != nil {
		return nil, err
	}
	cache[ref.SHA256] = msgs
	return msgs, nil
}
