package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/observability"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Subscriber is the outbox subscriber name the projection runs under.
// It is persisted with each event's delivered_to, so it never changes.
const Subscriber = "audit.record"

// Service implements the Audit context's use cases.
type Service struct {
	store   Store
	history History
	logger  *slog.Logger
	keys    *domain.KeySet
	metrics Metrics
}

// Option configures the service.
type Option func(*Service)

// WithHistory gives the service the outbox history the backfill reads.
func WithHistory(h History) Option { return func(s *Service) { s.history = h } }

// WithExportKeys gives the service the deployment's audit key set: the
// key exports are signed with and the public keys they verify with.
func WithExportKeys(k *domain.KeySet) Option { return func(s *Service) { s.keys = k } }

// ExportKeys is the audit key set, nil when none is configured. The
// export jobs (RFC 0006 wave 5) sign with its active key and serve its
// public keys as a glossa.audit.keys/1 document at
// /.well-known/glossa-audit-keys.json.
func (s *Service) ExportKeys() *domain.KeySet { return s.keys }

// WithLogger sets the logger. Audit logs ids, counts and actions, never
// a summary's values.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// New returns the service on store.
func New(store Store, opts ...Option) *Service {
	s := &Service{store: store, logger: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(s)
	}
	return s
}

var _ Recorder = (*Service)(nil)

// Subscribe subscribes the projection to every event type domain.
// Projections names. The registry test in domain is what makes that
// every event type there is.
// It is a batch subscriber (#89): a claimed batch's events of one tenant
// are appended in one transaction, with one chain lock and head read.
func (s *Service) Subscribe(reg *outbox.Registry) error {
	return reg.SubscribeBatch(Subscriber, s, domain.ProjectedTypes()...)
}

var _ outbox.BatchHandler = (*Service)(nil)

// HandleBatch projects a run of one tenant's delivered events into its
// chain, in the order given, in one transaction: one entry per event,
// none for an event already recorded. An event that cannot become an
// entry fails alone and permanently; if the append fails, every other
// event reports it, and the dispatcher delivers them one at a time.
func (s *Service) HandleBatch(ctx context.Context, ds []outbox.Delivery) []error {
	errs := make([]error, len(ds))
	drafts := make([]domain.Draft, 0, len(ds))
	var in []int // index in ds of each draft
	for i, d := range ds {
		draft, err := domain.FromEvent(d)
		if err == nil {
			err = draft.Validate()
		}
		if err != nil {
			errs[i] = outbox.Permanent(err)
			continue
		}
		drafts = append(drafts, draft)
		in = append(in, i)
	}
	if _, err := s.Append(ctx, drafts...); err != nil {
		for _, i := range in {
			errs[i] = outbox.BatchFailure(err)
		}
	}
	return errs
}

// HandleEvent projects one delivered event into the tenant's chain. It
// is idempotent on the event id: a redelivery appends nothing.
func (s *Service) HandleEvent(ctx context.Context, d outbox.Delivery) error {
	draft, err := domain.FromEvent(d)
	if err != nil {
		return outbox.Permanent(err)
	}
	if _, err := s.Append(ctx, draft); err != nil {
		if errors.Is(err, domain.ErrInvalidEntry) {
			return outbox.Permanent(err)
		}
		return err
	}
	return nil
}

// RecordSignIn implements Recorder.
func (s *Service) RecordSignIn(ctx context.Context, in domain.SignIn) error {
	if in.RequestID == "" {
		in.RequestID, _ = observability.RequestIDFromContext(ctx)
	}
	_, err := s.Append(ctx, in.Draft())
	return err
}

// RecordToolCall implements Recorder.
func (s *Service) RecordToolCall(ctx context.Context, c domain.ToolCall) error {
	if c.RequestID == "" {
		c.RequestID, _ = observability.RequestIDFromContext(ctx)
	}
	_, err := s.Append(ctx, c.Draft())
	return err
}

// Append records drafts, in order, at the end of the chain of ctx's
// tenant, skipping any whose event id is already recorded. It returns
// the entries it appended. Every draft is checked before anything is
// written, and all are written in one transaction or none is: one lock,
// one head read, the hashes chained in memory, one insert.
func (s *Service) Append(ctx context.Context, drafts ...domain.Draft) ([]domain.Entry, error) {
	if len(drafts) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(drafts))
	for _, d := range drafts {
		if err := d.Validate(); err != nil {
			return nil, err
		}
		ids = append(ids, d.EventID)
	}
	var appended []domain.Entry
	err := s.store.InChain(ctx, func(ctx context.Context, c Chain) error {
		appended = appended[:0]
		head, err := c.Head(ctx)
		if err != nil {
			return err
		}
		recorded, err := c.Recorded(ctx, ids)
		if err != nil {
			return err
		}
		for _, d := range drafts {
			if recorded[d.EventID] {
				continue
			}
			e, err := domain.Append(c.Tenant(), head, d)
			if err != nil {
				return err
			}
			recorded[d.EventID] = true // a duplicate within one batch
			head = e.Head()
			appended = append(appended, e)
		}
		return c.InsertAll(ctx, appended)
	})
	if err != nil {
		return nil, fmt.Errorf("audit: append: %w", err)
	}
	s.appended(len(appended))
	return appended, nil
}

// VerifyReport is the result of verifying a tenant's stored chain.
type VerifyReport struct {
	Entries int64
	// Head is the last entry verified.
	Head domain.Head
}

// verifyPage is how many entries Verify reads at a time.
const verifyPage = 1000

// Verify recomputes the whole chain of ctx's tenant from its first
// entry. It returns a *domain.ChainError at the first entry that does
// not follow, and the count verified up to there.
func (s *Service) Verify(ctx context.Context) (VerifyReport, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return VerifyReport{}, errors.New("audit: verify needs a tenant")
	}
	v := domain.NewVerifier(tenant.UUID(), domain.Head{})
	var after int64
	for {
		page, err := s.store.Entries(ctx, after, verifyPage)
		if err != nil {
			return VerifyReport{}, fmt.Errorf("audit: read the chain: %w", err)
		}
		for _, e := range page {
			if err := v.Next(e); err != nil {
				s.verified(err)
				return VerifyReport{Entries: v.Count(), Head: v.Head()}, err
			}
			after = e.Sequence
		}
		if len(page) < verifyPage {
			return VerifyReport{Entries: v.Count(), Head: v.Head()}, nil
		}
	}
}
