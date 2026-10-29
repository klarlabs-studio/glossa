package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres/integrationsql"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

// Checks implements app.CheckQueue in the system scope
// integration.github, which migration 0021 opens to the check table.
//
// The queue is system-scoped for the same reason the webhook inbox is:
// a worker claims the oldest due row whatever tenant it belongs to, and
// only afterwards enters that tenant's scope to read its catalog.
type Checks struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

var _ app.CheckQueue = (*Checks)(nil)

// NewChecks returns the check queue on uow.
func NewChecks(uow *db.UnitOfWork) *Checks {
	return &Checks{uow: uow, scope: db.NewSystemScope("integration.github")}
}

// check maps a row onto the domain aggregate.
func check(r integrationsql.IntegrationGithubCheck) domain.Check {
	c := domain.Check{
		ID: r.ID, TenantID: r.TenantID, InstallationID: r.InstallationID, RepositoryID: r.RepositoryID,
		PullRequest: int(r.PullRequest), Branch: r.Branch, HeadSHA: r.HeadSha, FromFork: r.FromFork,
		CommentID: r.CommentID,
		State:     domain.CheckState(r.State), Conclusion: r.Conclusion, Attempts: int(r.Attempts),
		Failure: r.Failure, RequestedAt: r.RequestedAt.UTC(), AvailableAt: r.AvailableAt.UTC(),
		UpdatedAt: r.UpdatedAt.UTC(),
	}
	if r.ClaimToken.Valid {
		c.ClaimToken = r.ClaimToken.UUID
	}
	if r.CompletedAt.Valid {
		at := r.CompletedAt.Time.UTC()
		c.CompletedAt = &at
	}
	if r.OpenedAt.Valid {
		c.OpenedAt = r.OpenedAt.Time.UTC()
	}
	// A ledger we cannot read is a report we lost, never a check we
	// cannot run: the worst it costs is a repeated annotation once.
	_ = json.Unmarshal(r.Runs, &c.Targets)
	return c
}

// runsJSON marshals the per-connection targets, always as an object so
// the column's check constraint holds.
func runsJSON(targets map[uuid.UUID]domain.CheckTarget) json.RawMessage {
	if len(targets) == 0 {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(targets)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// Open implements app.CheckQueue.
func (c *Checks) Open(ctx context.Context, in domain.Check) (domain.Check, error) {
	var out domain.Check
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		row, err := integrationsql.New(tx).OpenCheck(ctx, integrationsql.OpenCheckParams{
			ID: in.ID, TenantID: in.TenantID, InstallationID: in.InstallationID,
			RepositoryID: in.RepositoryID, PullRequest: int32(in.PullRequest), //nolint:gosec // a PR number fits
			Branch: in.Branch, HeadSha: in.HeadSHA, FromFork: in.FromFork,
			OpenedAt: timestamptz(in.OpenedAt), Now: in.RequestedAt,
		})
		if err != nil {
			return err
		}
		out = check(row)
		return nil
	})
	if err != nil {
		return domain.Check{}, fmt.Errorf("integration: open the check: %w", err)
	}
	return out, nil
}

// Rerun implements app.CheckQueue.
func (c *Checks) Rerun(ctx context.Context, repository int64, headSHA string, now time.Time) (int, error) {
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).RerunCheck(ctx, integrationsql.RerunCheckParams{
			RepositoryID: repository, HeadSha: headSHA, Now: now,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("integration: re-request the check: %w", err)
	}
	return int(n), nil
}

// Wake implements app.CheckQueue.
func (c *Checks) Wake(ctx context.Context, repositories []int64, branch string, now time.Time) (int, error) {
	if len(repositories) == 0 {
		return 0, nil
	}
	name := pgtype.Text{}
	if branch != "" {
		name = pgtype.Text{String: branch, Valid: true}
	}
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).WakeChecks(ctx, integrationsql.WakeChecksParams{
			RepositoryIds: repositories, Branch: name, Now: now,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("integration: wake the checks: %w", err)
	}
	return int(n), nil
}

// Claim implements app.CheckQueue.
func (c *Checks) Claim(ctx context.Context, now time.Time, lease time.Duration) (domain.Check, bool, error) {
	var (
		out domain.Check
		ok  bool
	)
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		row, err := integrationsql.New(tx).ClaimCheck(ctx, integrationsql.ClaimCheckParams{Now: now, LeaseSeconds: lease.Seconds()})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, ok = check(row), true
		return nil
	})
	if err != nil {
		return domain.Check{}, false, fmt.Errorf("integration: claim a check: %w", err)
	}
	return out, ok, nil
}

// Save implements app.CheckQueue.
func (c *Checks) Save(ctx context.Context, in domain.Check, available time.Time) error {
	return c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		_, err := integrationsql.New(tx).SaveCheck(ctx, integrationsql.SaveCheckParams{
			CommentID: in.CommentID, Runs: runsJSON(in.Targets), State: string(in.State),
			Conclusion: in.Conclusion, Failure: in.Failure, AvailableAt: available,
			CompletedAt: ts(in.CompletedAt), Now: in.UpdatedAt, ID: in.ID, ClaimToken: in.ClaimToken,
		})
		return err
	})
}

// Retry implements app.CheckQueue.
func (c *Checks) Retry(ctx context.Context, in domain.Check, now time.Time, delay time.Duration, failure string) error {
	return c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		_, err := integrationsql.New(tx).RetryCheck(ctx, integrationsql.RetryCheckParams{
			DelaySeconds: delay.Seconds(), Failure: failure, Now: now,
			ID: in.ID, ClaimToken: in.ClaimToken,
		})
		return err
	})
}

// Expire implements app.CheckQueue.
func (c *Checks) Expire(ctx context.Context, deadline, now time.Time) (int, error) {
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).ExpireChecks(ctx, integrationsql.ExpireChecksParams{
			Now: now, Deadline: deadline,
		})
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("integration: expire the checks: %w", err)
	}
	return int(n), nil
}

// Depth implements app.CheckQueue.
func (c *Checks) Depth(ctx context.Context) (int, error) {
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).CheckDepth(ctx)
		return err
	})
	return int(n), err
}

// DropRepository implements app.CheckQueue.
func (c *Checks) DropRepository(ctx context.Context, repository int64) (int, error) {
	var n int64
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		var err error
		n, err = integrationsql.New(tx).DropRepositoryChecks(ctx, repository)
		return err
	})
	return int(n), err
}

// Check implements app.CheckQueue.
func (c *Checks) Check(ctx context.Context, repository int64, pullRequest int) (domain.Check, error) {
	var out domain.Check
	err := c.uow.InSystemTx(ctx, c.scope, func(ctx context.Context, tx *db.SystemTx) error {
		row, err := integrationsql.New(tx).GetCheck(ctx, integrationsql.GetCheckParams{
			RepositoryID: repository, PullRequest: int32(pullRequest), //nolint:gosec // a PR number fits
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return app.ErrNotFound
		}
		if err != nil {
			return err
		}
		out = check(row)
		return nil
	})
	return out, err
}
