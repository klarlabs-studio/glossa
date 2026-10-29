package app

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Service implements Release's use cases.
type Service struct {
	tx      Transactor
	source  Source
	objects objectstore.Store
	signer  *domain.Signer
	now     func() time.Time
	logger  *slog.Logger
	// approvals answers who approved a release request; nil refuses
	// every deploy of one (UseApprovals).
	approvals Approvals
}

// Option configures a Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithLogger sets the logger for storage writes that are retried later.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.logger = l } }

// New returns the service.
func New(tx Transactor, source Source, objects objectstore.Store, signer *domain.Signer, opts ...Option) *Service {
	s := &Service{
		tx: tx, source: source, objects: objects, signer: signer,
		now:    func() time.Time { return time.Now().UTC() },
		logger: slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SigningKeys lists the public keys runtimes should trust for this
// deployment's manifests (active first, then retired), after checking
// the caller may read the project's releases.
func (s *Service) SigningKeys(ctx context.Context, project uuid.UUID) ([]domain.PublicKey, error) {
	if err := authz.RequireIn(ctx, authz.ReleasesRead, project); err != nil {
		return nil, err
	}
	if err := s.source.CheckProject(ctx, project); err != nil {
		return nil, err
	}
	return s.signer.PublicKeys(), nil
}

// actor returns the acting principal after checking perm.
func actor(ctx context.Context, perm authz.Permission) (string, error) {
	if err := authz.Require(ctx, perm); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// actorIn returns the acting principal after checking perm in project:
// a project outside the caller's scope is authz.ErrNotVisible, the
// answer for one that does not exist (RFC 0006 §4.1).
func actorIn(ctx context.Context, perm authz.Permission, project uuid.UUID) (string, error) {
	if err := authz.RequireIn(ctx, perm, project); err != nil {
		return "", err
	}
	p, _ := authz.From(ctx)
	return p.Actor.String(), nil
}

// idempotentID derives a create's ID from its Idempotency-Key, or a
// fresh time-ordered one when there is no key.
func idempotentID(operation, scope, by, key string) (uuid.UUID, bool, error) {
	if key == "" {
		return uuid.Must(uuid.NewV7()), false, nil
	}
	if err := idempotency.CheckKey(key); err != nil {
		return uuid.Nil, false, err
	}
	return idempotency.ID(operation, scope, by, key), true, nil
}

// beforeInt reads a page cursor holding a descending number.
func beforeInt(after string) (int, error) {
	if after == "" {
		return int(^uint32(0) >> 1), nil
	}
	n, err := strconv.Atoi(after)
	if err != nil || n < 1 {
		return 0, ErrInvalidPageToken
	}
	return n, nil
}

// afterUUID reads a page cursor holding an ID.
func afterUUID(after string) (uuid.UUID, error) {
	if after == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(after)
	if err != nil {
		return uuid.Nil, ErrInvalidPageToken
	}
	return id, nil
}
