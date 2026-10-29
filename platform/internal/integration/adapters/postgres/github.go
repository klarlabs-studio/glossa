package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres/integrationsql"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// GitHubTransactor implements app.GitHubTransactor over the same unit
// of work as the job store.
type GitHubTransactor struct{ uow *db.UnitOfWork }

// NewGitHubTransactor returns a transactor on uow.
func NewGitHubTransactor(uow *db.UnitOfWork) *GitHubTransactor { return &GitHubTransactor{uow: uow} }

var _ app.GitHubTransactor = (*GitHubTransactor)(nil)

// InGitHub implements app.GitHubTransactor.
func (t *GitHubTransactor) InGitHub(ctx context.Context, fn func(context.Context, app.GitHubStore) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &githubStore{q: integrationsql.New(tx)})
	})
}

type githubStore struct{ q *integrationsql.Queries }

var _ app.GitHubStore = (*githubStore)(nil)

// uniqueViolation reports whether err is Postgres's 23505 on one of the
// named constraints, which is how a claim on an installation another
// tenant holds comes back: the index is global, and row-level security
// hides the row that caused it, so nothing about the other tenant leaks.
func uniqueViolation(err error, constraints ...string) bool {
	var pge *pgconn.PgError
	if !errors.As(err, &pge) || pge.Code != "23505" {
		return false
	}
	for _, c := range constraints {
		if pge.ConstraintName == c {
			return true
		}
	}
	return false
}

// ── install intents ──────────────────────────────────────────────────

func (s *githubStore) InsertInstallIntent(ctx context.Context, id uuid.UUID, person string, stateHash []byte, created, expires time.Time) error {
	n, err := s.q.InsertInstallIntent(ctx, integrationsql.InsertInstallIntentParams{
		ID: id, Person: person, StateHash: stateHash, CreatedAt: created, ExpiresAt: expires,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("integration: the install intent %s already exists", id)
	}
	return nil
}

func (s *githubStore) RedeemInstallIntent(ctx context.Context, stateHash []byte, now time.Time) (app.RedeemedIntent, error) {
	r, err := s.q.RedeemInstallIntent(ctx, integrationsql.RedeemInstallIntentParams{StateHash: stateHash, Now: ts(&now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return app.RedeemedIntent{}, app.ErrInstallStateInvalid
	}
	if err != nil {
		return app.RedeemedIntent{}, err
	}
	return app.RedeemedIntent{ID: r.ID, TenantID: r.TenantID, Person: r.Person}, nil
}

// ── installations ────────────────────────────────────────────────────

func installationOf(r integrationsql.IntegrationGithubInstallation) (domain.Installation, error) {
	state, err := domain.ParseInstallationState(r.State)
	if err != nil {
		return domain.Installation{}, err
	}
	return domain.Installation{
		ID: r.ID, GitHubID: r.InstallationID, AccountID: r.AccountID, AccountLogin: r.AccountLogin,
		AccountType: r.AccountType, State: state, ConnectedBy: r.ConnectedBy,
		ConnectedAt: r.ConnectedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}

func (s *githubStore) InsertInstallation(ctx context.Context, i domain.Installation) error {
	n, err := s.q.InsertInstallation(ctx, integrationsql.InsertInstallationParams{
		ID: i.ID, InstallationID: i.GitHubID, AccountID: i.AccountID, AccountLogin: i.AccountLogin,
		AccountType: i.AccountType, State: string(i.State), ConnectedBy: i.ConnectedBy,
		ConnectedAt: i.ConnectedAt, UpdatedAt: i.UpdatedAt,
	})
	if uniqueViolation(err, "integration_github_installations_github") || (err == nil && n == 0) {
		return app.ErrInstallationClaimed
	}
	return err
}

func (s *githubStore) Installation(ctx context.Context, id uuid.UUID) (domain.Installation, error) {
	r, err := s.q.GetInstallation(ctx, id)
	if err != nil {
		return domain.Installation{}, storeError(err)
	}
	return installationOf(r)
}

func (s *githubStore) InstallationByGitHubID(ctx context.Context, githubID int64) (domain.Installation, error) {
	r, err := s.q.GetInstallationByGitHubID(ctx, githubID)
	if err != nil {
		return domain.Installation{}, storeError(err)
	}
	return installationOf(r)
}

func (s *githubStore) Installations(ctx context.Context) ([]domain.Installation, error) {
	rows, err := s.q.ListInstallations(ctx)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Installation, 0, len(rows))
	for _, r := range rows {
		i, err := installationOf(r)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

func (s *githubStore) SetInstallationState(ctx context.Context, githubID int64, state domain.InstallationState, login string, now time.Time) error {
	n, err := s.q.UpdateInstallationState(ctx, integrationsql.UpdateInstallationStateParams{
		InstallationID: githubID, State: string(state), AccountLogin: login, UpdatedAt: now,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *githubStore) DeleteInstallation(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteInstallation(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ── Git connections ──────────────────────────────────────────────────

func connectionOf(r integrationsql.IntegrationGitConnection) domain.GitConnection {
	return domain.GitConnection{
		ID: r.ID, InstallationID: r.InstallationID, RepositoryID: r.RepositoryID, RepositoryName: r.RepositoryName,
		ProjectID: r.ProjectID, ApplicationID: r.ApplicationID, DefaultBranch: r.DefaultBranch, Path: r.Path,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func connectionsOf(rows []integrationsql.IntegrationGitConnection) []domain.GitConnection {
	out := make([]domain.GitConnection, 0, len(rows))
	for _, r := range rows {
		out = append(out, connectionOf(r))
	}
	return out
}

func (s *githubStore) InsertGitConnection(ctx context.Context, c domain.GitConnection) error {
	n, err := s.q.InsertGitConnection(ctx, integrationsql.InsertGitConnectionParams{
		ID: c.ID, InstallationID: c.InstallationID, RepositoryID: c.RepositoryID, RepositoryName: c.RepositoryName,
		ProjectID: c.ProjectID, ApplicationID: c.ApplicationID, DefaultBranch: c.DefaultBranch, Path: c.Path,
		CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	})
	if uniqueViolation(err, "integration_git_connections_repo_path") || (err == nil && n == 0) {
		return app.ErrConnectionExists
	}
	return err
}

func (s *githubStore) GitConnection(ctx context.Context, id uuid.UUID) (domain.GitConnection, error) {
	r, err := s.q.GetGitConnection(ctx, id)
	if err != nil {
		return domain.GitConnection{}, storeError(err)
	}
	return connectionOf(r), nil
}

func (s *githubStore) GitConnections(ctx context.Context, f app.ConnectionFilter) ([]domain.GitConnection, error) {
	rows, err := s.q.ListGitConnections(ctx, integrationsql.ListGitConnectionsParams{
		InstallationID: nullUUID(f.Installation), ProjectID: nullUUID(f.Project),
	})
	if err != nil {
		return nil, storeError(err)
	}
	return connectionsOf(rows), nil
}

func (s *githubStore) ConnectionsForRepository(ctx context.Context, repositoryID int64) ([]domain.GitConnection, error) {
	rows, err := s.q.ConnectionsForRepository(ctx, repositoryID)
	if err != nil {
		return nil, storeError(err)
	}
	return connectionsOf(rows), nil
}

func (s *githubStore) SaveGitConnection(ctx context.Context, c domain.GitConnection) error {
	n, err := s.q.UpdateGitConnection(ctx, integrationsql.UpdateGitConnectionParams{
		ID: c.ID, ProjectID: c.ProjectID, ApplicationID: c.ApplicationID,
		DefaultBranch: c.DefaultBranch, Path: c.Path, UpdatedAt: c.UpdatedAt,
	})
	if uniqueViolation(err, "integration_git_connections_repo_path") {
		return app.ErrConnectionExists
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *githubStore) DeleteGitConnection(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.DeleteGitConnection(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

// ── the webhook inbox ────────────────────────────────────────────────

// Inbox implements app.DeliveryInbox in the system scope
// integration.github, which migration 0019 opens to the deliveries
// table, the installation → tenant mapping and the intent sweep.
type Inbox struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewInbox returns the inbox on uow.
func NewInbox(uow *db.UnitOfWork) *Inbox {
	return &Inbox{uow: uow, scope: db.NewSystemScope("integration.github")}
}

var _ app.DeliveryInbox = (*Inbox)(nil)

// Store implements app.DeliveryInbox.
func (i *Inbox) Store(ctx context.Context, d domain.Delivery) (bool, error) {
	var stored bool
	err := i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		n, err := integrationsql.New(tx).StoreDelivery(ctx, integrationsql.StoreDeliveryParams{
			DeliveryID: d.ID, Event: d.Event, Action: d.Action, InstallationID: d.GitHubInstallationID,
			Payload: d.Payload, ReceivedAt: d.ReceivedAt,
		})
		stored = n > 0
		return err
	})
	if err != nil {
		return false, fmt.Errorf("integration: store the delivery: %w", err)
	}
	return stored, nil
}

// Claim implements app.DeliveryInbox.
func (i *Inbox) Claim(ctx context.Context, lease time.Duration) (app.ClaimedDelivery, bool, error) {
	var (
		out app.ClaimedDelivery
		ok  bool
	)
	err := i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		r, err := integrationsql.New(tx).ClaimDelivery(ctx, lease.Seconds())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = app.ClaimedDelivery{
			ID: r.DeliveryID, Event: r.Event, Action: r.Action, GitHubInstallationID: r.InstallationID,
			TenantID: uuidPtr(r.TenantID), Payload: r.Payload, Attempts: int(r.Attempts), Token: r.ClaimToken.UUID,
		}
		ok = true
		return nil
	})
	if err != nil {
		return app.ClaimedDelivery{}, false, fmt.Errorf("integration: claim a delivery: %w", err)
	}
	return out, ok, nil
}

// Settle implements app.DeliveryInbox.
func (i *Inbox) Settle(ctx context.Context, id string, token uuid.UUID, state domain.DeliveryState,
	tenant *uuid.UUID, failure string, now time.Time,
) error {
	return i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		_, err := integrationsql.New(tx).SettleDelivery(ctx, integrationsql.SettleDeliveryParams{
			DeliveryID: id, ClaimToken: token, State: string(state), TenantID: nullUUID(tenant),
			Failure: failure, ProcessedAt: ts(&now),
		})
		return err
	})
}

// Retry implements app.DeliveryInbox.
func (i *Inbox) Retry(ctx context.Context, id string, token uuid.UUID, delay time.Duration, failure string) error {
	return i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		_, err := integrationsql.New(tx).RetryDelivery(ctx, integrationsql.RetryDeliveryParams{
			DeliveryID: id, ClaimToken: token, DelaySeconds: delay.Seconds(), Failure: failure,
		})
		return err
	})
}

// Owner implements app.DeliveryInbox.
func (i *Inbox) Owner(ctx context.Context, githubID int64) (app.InstallationOwner, bool, error) {
	var (
		out app.InstallationOwner
		ok  bool
	)
	err := i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		r, err := integrationsql.New(tx).ResolveInstallationTenant(ctx, githubID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		state, err := domain.ParseInstallationState(r.State)
		if err != nil {
			return err
		}
		out, ok = app.InstallationOwner{Tenant: tenancy.ID(r.TenantID), State: state}, true
		return nil
	})
	if err != nil {
		return app.InstallationOwner{}, false, fmt.Errorf("integration: resolve the installation's tenant: %w", err)
	}
	return out, ok, nil
}

// Depth implements app.DeliveryInbox.
func (i *Inbox) Depth(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	err := i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := integrationsql.New(tx).DeliveryDepth(ctx)
		for _, r := range rows {
			out[r.Event] = int(r.Pending)
		}
		return err
	})
	return out, err
}

// Sweep implements app.DeliveryInbox.
func (i *Inbox) Sweep(ctx context.Context, before time.Time) (int, int, error) {
	var deliveries, intents int64
	err := i.uow.InSystemTx(ctx, i.scope, func(ctx context.Context, tx *db.SystemTx) error {
		q := integrationsql.New(tx)
		var err error
		if deliveries, err = q.DeleteDeliveriesBefore(ctx, before); err != nil {
			return err
		}
		intents, err = q.DeleteExpiredInstallIntents(ctx)
		return err
	})
	return int(deliveries), int(intents), err
}

// ── the repository directory ─────────────────────────────────────────

// Repositories implements app.RepositoryDirectory in the same system
// scope, which migration 0024 opens to the Git connections' mapping
// columns: tenant, repository, project, application and path.
type Repositories struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewRepositories returns the directory on uow.
func NewRepositories(uow *db.UnitOfWork) *Repositories {
	return &Repositories{uow: uow, scope: db.NewSystemScope("integration.github")}
}

var _ app.RepositoryDirectory = (*Repositories)(nil)

// ConnectionsForRepository implements app.RepositoryDirectory.
func (r *Repositories) ConnectionsForRepository(
	ctx context.Context, repositoryID int64,
) ([]app.RepositoryConnection, error) {
	var out []app.RepositoryConnection
	err := r.uow.InSystemTx(ctx, r.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := integrationsql.New(tx).ResolveRepositoryConnections(ctx, repositoryID)
		if err != nil {
			return err
		}
		out = make([]app.RepositoryConnection, len(rows))
		for i, row := range rows {
			out[i] = app.RepositoryConnection{
				Tenant: tenancy.ID(row.TenantID), Project: row.ProjectID,
				Application: row.ApplicationID, Path: row.Path,
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("integration: resolve repository %d: %w", repositoryID, err)
	}
	return out, nil
}
