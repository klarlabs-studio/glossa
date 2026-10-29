package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// Service implements Catalog's use cases.
type Service struct {
	tx         Transactor
	coverage   TranslationCoverage
	projection MessageProjection
	impact     TranslationImpact
	locales    ProjectLocales
	scanner    Scanner
	now        func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithScanner enables SweepAllProposals across tenants (the daily
// catalog.proposals job). Without one, the sweep runs only per tenant.
func WithScanner(sc Scanner) Option { return func(s *Service) { s.scanner = sc } }

// New returns the service. Wire Localization's coverage with
// SetCoverage once both contexts exist.
func New(tx Transactor, opts ...Option) *Service {
	s := &Service{tx: tx, now: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetCoverage wires the translation coverage port. The composition root
// calls it once at startup, before serving.
func (s *Service) SetCoverage(c TranslationCoverage) { s.coverage = c }

// SetProjection wires the message projection a bulk upsert updates in
// its transaction. The composition root calls it once at startup.
func (s *Service) SetProjection(p MessageProjection) { s.projection = p }

// SetLocales wires the port a check policy's required locales are
// validated against. The composition root calls it once at startup;
// without one, they are checked for shape only.
func (s *Service) SetLocales(l ProjectLocales) { s.locales = l }

// SetImpact wires the translation impact port a branch's status report
// counts outdated translations with. The composition root calls it once
// at startup; without one, reports leave the counts out.
func (s *Service) SetImpact(i TranslationImpact) { s.impact = i }

// author returns the acting principal after checking perm.
func author(ctx context.Context, perm authz.Permission) (domain.Author, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return domain.Author(p.Actor.String()), nil
}

// authorIn returns the acting principal after checking perm in project:
// a project outside the caller's scope is authz.ErrNotVisible, the
// answer for one that does not exist (RFC 0006 §4.1).
func authorIn(ctx context.Context, perm authz.Permission, project uuid.UUID) (domain.Author, error) {
	if err := authz.RequireIn(ctx, perm, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return domain.Author(p.Actor.String()), nil
}

// idempotentID derives a create's ID from its Idempotency-Key, or
// returns uuid.Nil when there is no key.
func idempotentID(operation, scope string, by domain.Author, key string) (uuid.UUID, error) {
	if key == "" {
		return uuid.Nil, nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, err
	}
	return idempotency.ID(operation, scope, string(by), key), nil
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

// beforeRevision reads a page cursor holding a revision number.
func beforeRevision(s string) (int, error) {
	if s == "" {
		return int(^uint32(0) >> 1), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, invalidPageToken()
	}
	return n, nil
}
