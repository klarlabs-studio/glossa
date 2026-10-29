package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// principalPublisher is the background principal that publishes due
// branch environments: it reads the catalog and translations the way a
// publish does, and publishes releases, nothing else.
const principalPublisher = "release.publisher"

// publisherBatch bounds the due requests one pass takes.
const publisherBatch = 50

// Publisher runs due publish requests (RequestBranchPublish) across
// tenants, each in its tenant's scope as the background principal
// release.publisher. Due means due on the Service's clock.
type Publisher struct {
	s        *Service
	scanner  Scanner
	interval time.Duration
}

// NewPublisher returns a publisher that looks for due requests every
// interval (default 5 s).
func NewPublisher(s *Service, scanner Scanner, interval time.Duration) *Publisher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Publisher{s: s, scanner: scanner, interval: interval}
}

// Run publishes due requests until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		if _, err := p.RunOnce(ctx); err != nil && ctx.Err() == nil {
			p.s.logger.ErrorContext(ctx, "release: publishing due branch environments", slog.Any("error", err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// RunOnce publishes the requests due now and returns how many it
// published. One failing request doesn't hold up the others; their
// errors are joined.
func (p *Publisher) RunOnce(ctx context.Context) (int, error) {
	refs, err := p.scanner.DuePublishRequests(ctx, p.s.now(), publisherBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, ref := range refs {
		published, err := p.publish(ctx, ref)
		if err != nil {
			p.s.logger.WarnContext(ctx, "release: branch environment not published",
				slog.String("project_id", ref.Project.String()), slog.String("environment", ref.Environment), slog.Any("error", err))
			errs = append(errs, err)
		}
		if published {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func (p *Publisher) publish(ctx context.Context, ref EnvironmentRef) (bool, error) {
	ctx = tenancy.ContextWithTenant(context.WithoutCancel(ctx), ref.Tenant)
	bg, err := authz.Background(ctx, principalPublisher,
		authz.ReleasesRead, authz.ReleasesPublish, authz.CatalogRead, authz.TranslationsRead)
	if err != nil {
		return false, err
	}
	return p.s.PublishDue(bg, ref.Project, ref.Environment)
}

// keyIndexBatch bounds the keys one pass of RewriteKeyIndexes reads.
const keyIndexBatch = 200

// RewriteKeyIndexes rewrites the index objects of active keys last
// written in an older format — after migration 0015, every existing
// key's, which gains its scope — each from its row in its tenant's
// scope, and returns how many it wrote. It is the key index maintenance
// task glossa-server runs at startup: idempotent, and a key it can't
// write (storage down) is left for the next run while the edge reads
// the old object as the scope the key migrated to.
func (s *Service) RewriteKeyIndexes(ctx context.Context, scanner Scanner) (int, error) {
	n := 0
	for {
		refs, err := scanner.StaleKeyIndexes(ctx, keyIndexVersion, keyIndexBatch)
		if err != nil || len(refs) == 0 {
			return n, err
		}
		for _, ref := range refs {
			tctx := tenancy.ContextWithTenant(ctx, ref.Tenant)
			if err := s.SyncDeliveryKey(tctx, ref.Project, ref.Key); err != nil {
				return n, err
			}
			n++
		}
	}
}
