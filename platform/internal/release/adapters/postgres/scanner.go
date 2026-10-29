package postgres

import (
	"context"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/postgres/releasesql"
	"go.klarlabs.de/glossa/platform/internal/release/app"
)

// Scanner implements app.Scanner in system scope: it reads only the
// columns migrations 0015 and 0048 open to glossa_system, and the work it finds
// runs in each tenant's scope.
type Scanner struct {
	uow       *db.UnitOfWork
	publisher db.SystemScope
	keyIndex  db.SystemScope
	rollouts  db.SystemScope
}

// NewScanner returns a Scanner on uow.
func NewScanner(uow *db.UnitOfWork) *Scanner {
	return &Scanner{uow: uow, publisher: db.NewSystemScope("release.publisher"), keyIndex: db.NewSystemScope("release.key_index"),
		rollouts: db.NewSystemScope("release.rollout_sweeper"),
	}
}

var _ app.Scanner = (*Scanner)(nil)

// DuePublishRequests implements app.Scanner.
func (s *Scanner) DuePublishRequests(ctx context.Context, now time.Time, limit int) ([]app.EnvironmentRef, error) {
	var out []app.EnvironmentRef
	err := s.uow.InSystemTx(ctx, s.publisher, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := releasesql.New(tx).ListDuePublishRequests(ctx, releasesql.ListDuePublishRequestsParams{Now: now, MaxRows: int32Of(limit)})
		for _, r := range rows {
			out = append(out, app.EnvironmentRef{Tenant: tenancy.ID(r.TenantID), Project: r.ProjectID, Environment: r.Environment})
		}
		return err
	})
	return out, err
}

// StaleKeyIndexes implements app.Scanner.
func (s *Scanner) StaleKeyIndexes(ctx context.Context, version, limit int) ([]app.KeyRef, error) {
	var out []app.KeyRef
	err := s.uow.InSystemTx(ctx, s.keyIndex, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := releasesql.New(tx).ListStaleKeyIndexes(ctx, releasesql.ListStaleKeyIndexesParams{
			IndexVersion: int16(version), MaxRows: int32Of(limit), //nolint:gosec // a small format number
		})
		for _, r := range rows {
			out = append(out, app.KeyRef{Tenant: tenancy.ID(r.TenantID), Project: r.ProjectID, Key: r.ID})
		}
		return err
	})
	return out, err
}
