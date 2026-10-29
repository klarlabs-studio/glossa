package app

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// Service implements Knowledge's use cases.
type Service struct {
	tx           Transactor
	translations Translations
	projects     Projects
	messages     Messages
	logger       *slog.Logger
	now          func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithMessages gives the service Catalog's message port, which the
// unit-addressed lookup (UnitTMMatches) needs.
func WithMessages(m Messages) Option { return func(s *Service) { s.messages = m } }

// WithLogger sets the logger for background work.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// New returns the service.
func New(tx Transactor, translations Translations, projects Projects, opts ...Option) *Service {
	s := &Service{
		tx: tx, translations: translations, projects: projects, logger: slog.New(slog.DiscardHandler),
		now: func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// actor returns the acting principal after checking perm.
func actor(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// Knowledge is tenant-wide (project nil) or a project's, and project
// scope (RFC 0006 §4.1) reaches it through these: a project's rows are
// seen and written only inside the caller's scope, and tenant-wide
// rows, which apply to every project, are written only by a caller
// limited to none. An assigned member (§3.3) reads the style guide and
// termbase for a locale one of their units is in, and nothing else.

// rowScope answers a row owned by a project outside the caller's scope
// as not found; tenant-wide rows (project nil) are everyone's to read.
func rowScope(ctx context.Context, project *uuid.UUID) error {
	if project == nil {
		return nil
	}
	return authz.InProject(ctx, *project)
}

// writeScope returns the acting principal after checking perm for a
// write to project's knowledge, or — project nil — to the tenant-wide
// knowledge every project shares, which nobody limited to some projects
// may change.
func writeScope(ctx context.Context, perm authz.Permission, project *uuid.UUID) (string, error) {
	if project == nil {
		if err := authz.RequireUnscoped(ctx, perm); err != nil {
			return "", err
		}
	} else if err := authz.RequireIn(ctx, perm, *project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// readFor checks perm for reading the knowledge that applies to text in
// project (nil: tenant-wide only) and locale: for an assigned member
// only in a project and a locale one of their units is in.
func readFor(ctx context.Context, perm authz.Permission, project *uuid.UUID, locale bcp47.Tag) error {
	if p, ok := authz.From(ctx); !ok || !p.Assigned() {
		if project == nil {
			return authz.Require(ctx, perm)
		}
		return authz.RequireIn(ctx, perm, *project)
	}
	if project == nil || locale.IsZero() {
		return authz.Require(ctx, perm) // refuses an assigned member
	}
	l, err := authz.ParseLocale(locale.String())
	if err != nil {
		return err
	}
	return authz.RequireLocaleIn(ctx, perm, *project, l)
}

// listScope checks perm for a list and returns the projects whose rows
// it may hold; a filter naming one project outside them is not found.
func listScope(ctx context.Context, perm authz.Permission, project *uuid.UUID) ([]uuid.UUID, error) {
	scope, err := authz.Projects(ctx, perm)
	if err != nil {
		return nil, err
	}
	if err := rowScope(ctx, project); err != nil {
		return nil, err
	}
	return scope.IDs(), nil
}

// tmScope checks perm for a TM read over scope: a project outside the
// caller's scope is not found, and reaching every project's units is
// refused to a caller limited to some.
func tmScope(ctx context.Context, perm authz.Permission, project *uuid.UUID, allProjects bool) error {
	if allProjects {
		if err := authz.RequireUnscoped(ctx, perm); err != nil {
			return err
		}
	}
	if project == nil {
		return authz.Require(ctx, perm)
	}
	return authz.RequireIn(ctx, perm, *project)
}

// requireProject checks that a project scope names an existing project.
func (s *Service) requireProject(ctx context.Context, project *uuid.UUID) error {
	if project == nil {
		return nil
	}
	_, err := s.projects.Project(ctx, *project)
	return err
}

// idempotentID derives a create's ID from its Idempotency-Key, or a new
// time-ordered ID when there is none.
func idempotentID(ctx context.Context, operation, by, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	p, _ := authz.From(ctx)
	return idempotency.ID(operation, p.Tenant.String(), by, key), nil
}

// checkIfMatch applies optimistic concurrency: a change based on
// another version fails.
func checkIfMatch(version int, ifMatch *int) error {
	switch {
	case ifMatch == nil:
		return ErrPreconditionRequired
	case *ifMatch != version:
		return ErrPreconditionFailed
	}
	return nil
}

func invalidPageToken() error {
	return problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this list issued")
}

// afterUUID reads a page cursor holding an ID.
func afterUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, invalidPageToken()
	}
	return id, nil
}

// beforeVersion reads a page cursor holding a version number.
func beforeVersion(s string) (int, error) {
	if s == "" {
		return int(^uint32(0) >> 1), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, invalidPageToken()
	}
	return n, nil
}
