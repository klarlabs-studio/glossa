package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// CaptureImage is a capture's stored image.
type CaptureImage struct {
	domain.Image
	// Open reads the re-encoded PNG: ErrCaptureNotFound when retention
	// deleted it meanwhile, ErrStorageUnavailable when storage fails.
	Open func() (io.ReadCloser, error)
}

// CaptureImage returns a capture's image, read through the API: the
// object store never hands out a URL (RFC 0004 §3.3). The image is
// content-addressed, so its digest is a strong validator. Needs
// catalog.read.
//
// An assigned member sees the image of a capture that shows a message
// their units are in — a screenshot is part of what translating a unit
// needs (RFC 0006 §3.3) — and no other.
func (s *Service) CaptureImage(ctx context.Context, project, capture uuid.UUID) (CaptureImage, error) {
	vis, err := authz.Visible(ctx, authz.CatalogRead, project)
	if err != nil {
		return CaptureImage{}, err
	}
	if err := s.catalog.Project(ctx, project); err != nil {
		return CaptureImage{}, err
	}
	if s.objects == nil {
		return CaptureImage{}, errNoImages
	}
	var (
		c     domain.Capture
		shown = vis.All()
	)
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		if c, err = st.Capture(ctx, capture); err != nil || shown || c.ProjectID != project {
			return err
		}
		// Capture comes back without its regions, so whether it shows a
		// message the caller may see is asked of the store. Reading
		// c.Regions here answered "no" for every capture, and an
		// assigned member saw no screenshot at all, not even of their
		// own units.
		shown, err = st.CaptureShows(ctx, capture, vis.Messages())
		return err
	})
	if errors.Is(err, ErrNotFound) || (err == nil && (c.ProjectID != project || !shown)) {
		return CaptureImage{}, ErrCaptureNotFound
	}
	if err != nil {
		return CaptureImage{}, err
	}
	t, _ := tenancy.FromContext(ctx)
	key := domain.ImageKey(t, project, c.Image.Digest)
	return CaptureImage{Image: c.Image, Open: func() (io.ReadCloser, error) {
		r, err := s.objects.Open(ctx, key)
		switch {
		case errors.Is(err, objectstore.ErrNotFound):
			return nil, ErrCaptureNotFound
		case err != nil:
			return nil, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
		}
		return r, nil
	}}, nil
}

// MessageCapturesPage is a message's current captures, up to a limit.
type MessageCapturesPage struct {
	MessageID uuid.UUID
	// Captures show the message; each holds only its regions.
	Captures []CaptureView
	// Truncated says more captures exist than the limit.
	Truncated bool
}

// CapturesOfKey returns the current captures that show the message a
// key names now (ErrMessageNotFound), with its regions on each: the
// default branch first, then by application, route, locale and the
// widest viewport. q.Branch selects a branch view, as for usages.
// Needs catalog.read.
func (s *Service) CapturesOfKey(ctx context.Context, project uuid.UUID, key string, q UsageQuery) (MessageCapturesPage, error) {
	id, err := s.messageOf(ctx, project, key)
	if err != nil {
		return MessageCapturesPage{}, err
	}
	view, err := s.messageView(ctx, project, id, q.Branch)
	if err != nil {
		return MessageCapturesPage{}, err
	}
	limit := q.limit()
	out := MessageCapturesPage{MessageID: id, Captures: []CaptureView{}}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		current, err := currentBuilds(ctx, st, project, view)
		if err != nil || len(current) == 0 {
			return err
		}
		got, err := st.MessageCaptures(ctx, id, current, limit+1)
		if err != nil {
			return err
		}
		if len(got) > limit {
			got, out.Truncated = got[:limit], true
		}
		out.Captures = append(out.Captures, got...)
		return nil
	})
	return out, err
}

// CaptureCoverage counts the view's active messages and how many of
// them a current capture shows (RFC 0004 §3). The pull-request comment
// reports it as "captured / not captured", and the coverage metric is
// the default branch's own answer. Needs catalog.read.
type CaptureCoverage struct {
	// Active counts the project's active messages; Captured, how many
	// have a visible region on a current capture.
	Active, Captured int
	// CurrentBuilds counts the builds considered: with none, nothing is
	// captured because nothing was uploaded yet.
	CurrentBuilds int
}

// NotCaptured is the messages no current capture shows.
func (c CaptureCoverage) NotCaptured() int { return c.Active - c.Captured }

// CaptureCoverage measures a view's capture coverage.
func (s *Service) CaptureCoverage(ctx context.Context, project uuid.UUID, branch string) (CaptureCoverage, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project); err != nil {
		return CaptureCoverage{}, err
	}
	view, err := parseView(branch)
	if err != nil {
		return CaptureCoverage{}, err
	}
	active, err := s.catalog.ActiveMessages(ctx, project)
	if err != nil {
		return CaptureCoverage{}, err
	}
	captured := map[uuid.UUID]bool{}
	out := CaptureCoverage{Active: len(active)}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		current, err := currentBuilds(ctx, st, project, view)
		if err != nil || len(current) == 0 {
			return err
		}
		out.CurrentBuilds = len(current)
		ids, err := st.CapturedMessages(ctx, current)
		for _, id := range ids {
			captured[id] = true
		}
		return err
	})
	if err != nil {
		return CaptureCoverage{}, err
	}
	for _, m := range active {
		if captured[m.ID] {
			out.Captured++
		}
	}
	return out, nil
}

// measureCaptureCoverage records the share of a project's active
// messages with a visible region on a current default-branch capture.
func (s *Service) measureCaptureCoverage(ctx context.Context, project uuid.UUID) error {
	c, err := s.CaptureCoverage(ctx, project, "")
	if err != nil {
		return err
	}
	t, _ := tenancy.FromContext(ctx)
	s.metrics.CaptureCoverage(t, project, c.Active, c.Captured)
	return nil
}
