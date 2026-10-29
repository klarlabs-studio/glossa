package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// BuildQuery narrows a project's builds.
type BuildQuery struct {
	// Application is an application's slug; empty lists every
	// application's builds.
	Application string
}

// ListBuilds pages through a project's builds, newest first, with the
// unknown keys each holds. Needs catalog.read.
func (s *Service) ListBuilds(ctx context.Context, project uuid.UUID, q BuildQuery, page pagination.Page) ([]BuildRecord, *string, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project); err != nil {
		return nil, nil, err
	}
	after, err := parseBuildCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var application *uuid.UUID
	if q.Application != "" {
		id, err := s.catalog.Application(ctx, project, q.Application)
		if err != nil {
			return nil, nil, err
		}
		application = &id
	} else if err := s.catalog.Project(ctx, project); err != nil {
		return nil, nil, err
	}
	var rows []BuildRecord
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.ListBuilds(ctx, project, application, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(b BuildRecord) string {
		return b.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + b.ID.String()
	})
	return items, next, nil
}

func parseBuildCursor(s string) (*BuildCursor, error) {
	if s == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(s, "|")
	t, err1 := time.Parse(time.RFC3339Nano, at)
	u, err2 := uuid.Parse(id)
	if !ok || err1 != nil || err2 != nil {
		return nil, invalidPageToken()
	}
	return &BuildCursor{CreatedAt: t, ID: u}, nil
}

// UsagePage is a page of the usages in a view's current builds.
type UsagePage struct {
	Usages []UsageView
	Next   *string
}

// ListUsages pages through the usages in the current builds of a view
// (the default branch's, or branch's with the default branch's where it
// didn't rebuild) on a route, in a component or in a file — the
// messages on a route or in a component (RFC 0004 §8). Filters combine;
// none lists every current usage. Needs catalog.read.
func (s *Service) ListUsages(ctx context.Context, project uuid.UUID, branch string, f UsageFilter, page pagination.Page) (UsagePage, error) {
	view, err := s.readView(ctx, project, branch)
	if err != nil {
		return UsagePage{}, err
	}
	after, err := parseUsageCursor(page.After)
	if err != nil {
		return UsagePage{}, err
	}
	var rows []UsageView
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		current, err := currentBuilds(ctx, st, project, view)
		if err != nil || len(current) == 0 {
			return err
		}
		rows, err = st.ListUsages(ctx, current, f, after, page.Limit())
		return err
	})
	if err != nil {
		return UsagePage{}, err
	}
	items, next := pagination.Trim(rows, page, func(u UsageView) string {
		return u.BuildID.String() + "|" + strconv.Itoa(u.Position)
	})
	return UsagePage{Usages: items, Next: next}, nil
}

func parseUsageCursor(s string) (UsageCursor, error) {
	if s == "" {
		return UsageCursor{}, nil
	}
	build, pos, ok := strings.Cut(s, "|")
	id, err1 := uuid.Parse(build)
	n, err2 := strconv.Atoi(pos)
	if !ok || err1 != nil || err2 != nil || n < 0 {
		return UsageCursor{}, invalidPageToken()
	}
	return UsageCursor{Build: id, Position: n}, nil
}

// CoLocated returns up to limit messages shown together with message
// in the default branch's current builds (RFC 0004 §8): those used on
// one of its routes or rendered on one of its captures, most shared
// first. Needs catalog.read.
func (s *Service) CoLocated(ctx context.Context, project, message uuid.UUID, limit int) ([]uuid.UUID, error) {
	if err := authz.RequireIn(ctx, authz.CatalogRead, project); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxUsageLimit {
		return nil, fmt.Errorf("%w: limit must be 1–%d", ErrInvalidQuery, MaxUsageLimit)
	}
	var out []uuid.UUID
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		current, err := currentBuilds(ctx, st, project, "")
		if err != nil || len(current) == 0 {
			return err
		}
		out, err = st.CoLocatedMessages(ctx, message, current, limit)
		return err
	})
	return out, err
}
