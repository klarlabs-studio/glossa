package v0

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// A restored v0.3 database, read directly (RFC 0006 §7.2). v0.3's read
// API exposes neither key descriptions, who last changed a translation,
// its change history, its users nor its locale labels; its schema does.
// Nothing here writes: the connection is read-only, every read happens
// in one REPEATABLE READ READ ONLY transaction (one consistent snapshot),
// and row-level security is off for the session so that a role RLS
// would filter gets an error rather than fewer rows.

// Snapshot is everything the importer reads from one v0.3 project.
type Snapshot struct {
	Restore      Restore
	Tenant       Tenant
	Project      Project
	Locales      []Locale
	Keys         []Key
	Translations []Row
	Users        []User
	History      []Change
	// Uncarried counts the rows of what isn't imported, for the report.
	Uncarried Uncarried
}

// Tenant is v0.3's tenants row of the project.
type Tenant struct{ ID, Slug, Name string }

// Project is v0.3's projects row.
type Project struct{ ID, Slug, Name, DefaultLocale string }

// Locale is a v0.3 locales row.
type Locale struct {
	Code    string
	Label   string
	Enabled bool
}

// Key is a v0.3 keys row.
type Key struct {
	Key         string
	Description string
	FirstSeenAt time.Time
}

// Row is a v0.3 translations row with its key and locale code, and the
// actor of the newest audit_log row for it when that row wrote the
// current value (v0.3 leaves updated_by NULL for AI and API-key writes;
// the audit row still says "ai" and which provider).
type Row struct {
	ID        string
	Key       string
	Locale    string
	Value     string
	Status    string
	UpdatedBy string // users.id, or "" when v0.3 recorded none
	UpdatedAt time.Time
	// LastActorKind and LastActorLabel come from the matching audit row;
	// empty when there is none.
	LastActorKind  string
	LastActorLabel string
}

// User is a v0.3 users row of the project's tenant. password_hash is
// never read.
type User struct {
	ID        string
	Email     string
	Role      string
	Locales   []string
	CreatedAt time.Time
}

// Change is a v0.3 audit_log row. Key and Locale are empty when its
// translation no longer exists (or it named none): v0.3's audit_log has
// no foreign key, so such a row can't be tied to a project.
type Change struct {
	ID            int64
	TranslationID string
	Key           string
	Locale        string
	Before        *string
	After         *string
	ChangedBy     string
	ActorKind     string
	ActorLabel    string
	ChangedAt     time.Time
}

// Uncarried counts the v0.3 rows of this project (or its tenant) that
// the import does not carry.
type Uncarried struct {
	APIKeys         int
	AIProviders     int
	AnalyticsEvents int
}

// DescribeDSN renders a DSN without its credentials, for reports.
func DescribeDSN(dsn string) string {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "(unparseable DSN)"
	}
	return "postgres://" + net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))) + "/" + cfg.Database
}

// ReadRestore connects to dsn, refuses it unless it is a marked restore
// with v0.3's schema, and reads project (in tenant, when given; needed
// only when two tenants have a project with that slug).
func ReadRestore(ctx context.Context, dsn, tenant, project string) (Snapshot, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return Snapshot{}, fmt.Errorf("--v0-db: %w", err)
	}
	cfg.RuntimeParams["application_name"] = "glossa-import-v0"
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["row_security"] = "off"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	facts, err := readMarker(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	var s Snapshot
	if s.Restore, err = decideRestore(facts); err != nil {
		return Snapshot{}, err
	}
	if err := checkSchema(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if err := s.read(ctx, tx, tenant, project); err != nil {
		return Snapshot{}, classify(err)
	}
	return s, nil
}

// ProjectNotFoundError: no v0.3 project with that slug (in that tenant).
type ProjectNotFoundError struct{ Tenant, Project string }

func (e *ProjectNotFoundError) Error() string {
	if e.Tenant != "" {
		return fmt.Sprintf("the restore has no project %q in tenant %q", e.Project, e.Tenant)
	}
	return fmt.Sprintf("the restore has no project %q", e.Project)
}

// AmbiguousProjectError: the slug exists in several tenants.
type AmbiguousProjectError struct {
	Project string
	Tenants []string
}

func (e *AmbiguousProjectError) Error() string {
	return fmt.Sprintf("project %q exists in tenants %v; name one with --v0-tenant", e.Project, e.Tenants)
}

func (s *Snapshot) read(ctx context.Context, tx pgx.Tx, tenant, project string) error {
	if err := s.readProject(ctx, tx, tenant, project); err != nil {
		return err
	}
	steps := []func(context.Context, pgx.Tx) error{s.readLocales, s.readKeys, s.readTranslations, s.readUsers, s.readHistory, s.readUncarried}
	for _, step := range steps {
		if err := step(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Snapshot) readProject(ctx context.Context, tx pgx.Tx, tenant, project string) error {
	rows, err := tx.Query(ctx, `
SELECT p.id::text, p.slug, p.name, p.default_locale, t.id::text, t.slug, t.name
FROM projects p JOIN tenants t ON t.id = p.tenant_id
WHERE p.slug = $1 AND ($2 = '' OR t.slug = $2)
ORDER BY t.slug`, project, tenant)
	if err != nil {
		return err
	}
	var found []Snapshot
	for rows.Next() {
		var c Snapshot
		if err := rows.Scan(&c.Project.ID, &c.Project.Slug, &c.Project.Name, &c.Project.DefaultLocale,
			&c.Tenant.ID, &c.Tenant.Slug, &c.Tenant.Name); err != nil {
			rows.Close()
			return err
		}
		found = append(found, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	switch len(found) {
	case 0:
		return &ProjectNotFoundError{Tenant: tenant, Project: project}
	case 1:
		s.Project, s.Tenant = found[0].Project, found[0].Tenant
		return nil
	}
	e := &AmbiguousProjectError{Project: project}
	for _, c := range found {
		e.Tenants = append(e.Tenants, c.Tenant.Slug)
	}
	return e
}

func (s *Snapshot) readLocales(ctx context.Context, tx pgx.Tx) error {
	rows, _ := tx.Query(ctx, `SELECT code, label, enabled FROM locales WHERE project_id = $1 ORDER BY code`, s.Project.ID)
	var err error
	s.Locales, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Locale, error) {
		var l Locale
		return l, r.Scan(&l.Code, &l.Label, &l.Enabled)
	})
	return err
}

func (s *Snapshot) readKeys(ctx context.Context, tx pgx.Tx) error {
	rows, _ := tx.Query(ctx, `SELECT key, coalesce(description, ''), first_seen_at FROM keys WHERE project_id = $1 ORDER BY key`, s.Project.ID)
	var err error
	s.Keys, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Key, error) {
		var k Key
		err := r.Scan(&k.Key, &k.Description, &k.FirstSeenAt)
		k.FirstSeenAt = k.FirstSeenAt.UTC()
		return k, err
	})
	return err
}

func (s *Snapshot) readTranslations(ctx context.Context, tx pgx.Tx) error {
	rows, _ := tx.Query(ctx, `
SELECT t.id::text, k.key, l.code, t.value, t.status, coalesce(t.updated_by::text, ''), t.updated_at,
       coalesce(la.actor_kind, ''), coalesce(la.actor_label, '')
FROM translations t
JOIN keys k ON k.id = t.key_id
JOIN locales l ON l.id = t.locale_id
LEFT JOIN LATERAL (
  SELECT a.actor_kind, a.actor_label, a.after_value
  FROM audit_log a WHERE a.translation_id = t.id
  ORDER BY a.changed_at DESC, a.id DESC LIMIT 1
) la ON la.after_value IS NOT DISTINCT FROM t.value
WHERE k.project_id = $1
ORDER BY k.key, l.code`, s.Project.ID)
	var err error
	s.Translations, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var t Row
		err := r.Scan(&t.ID, &t.Key, &t.Locale, &t.Value, &t.Status, &t.UpdatedBy, &t.UpdatedAt, &t.LastActorKind, &t.LastActorLabel)
		t.UpdatedAt = t.UpdatedAt.UTC()
		return t, err
	})
	return err
}

func (s *Snapshot) readUsers(ctx context.Context, tx pgx.Tx) error {
	rows, _ := tx.Query(ctx, `SELECT id::text, email, role, locales, created_at FROM users WHERE tenant_id = $1 ORDER BY email`, s.Tenant.ID)
	var err error
	s.Users, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
		var u User
		err := r.Scan(&u.ID, &u.Email, &u.Role, &u.Locales, &u.CreatedAt)
		u.CreatedAt = u.CreatedAt.UTC()
		return u, err
	})
	return err
}

// readHistory reads the tenant's audit_log rows that belong to this
// project's translations, and the rows whose translation is gone (no
// project can be told for those; each import of the tenant plans them,
// and v0_id makes importing them twice a no-op).
func (s *Snapshot) readHistory(ctx context.Context, tx pgx.Tx) error {
	rows, _ := tx.Query(ctx, `
SELECT a.id, coalesce(a.translation_id::text, ''), coalesce(k.key, ''), coalesce(l.code, ''),
       a.before_value, a.after_value, coalesce(a.changed_by::text, ''), a.actor_kind, a.actor_label, a.changed_at
FROM audit_log a
LEFT JOIN translations t ON t.id = a.translation_id
LEFT JOIN keys k ON k.id = t.key_id
LEFT JOIN locales l ON l.id = t.locale_id
WHERE a.tenant_id = $1 AND (k.project_id = $2 OR t.id IS NULL)
ORDER BY a.changed_at, a.id`, s.Tenant.ID, s.Project.ID)
	var err error
	s.History, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Change, error) {
		var c Change
		err := r.Scan(&c.ID, &c.TranslationID, &c.Key, &c.Locale, &c.Before, &c.After, &c.ChangedBy, &c.ActorKind, &c.ActorLabel, &c.ChangedAt)
		c.ChangedAt = c.ChangedAt.UTC()
		return c, err
	})
	return err
}

func (s *Snapshot) readUncarried(ctx context.Context, tx pgx.Tx) error {
	return tx.QueryRow(ctx, `
SELECT (SELECT count(*) FROM project_api_keys WHERE project_id = $1),
       (SELECT count(*) FROM ai_translation_providers WHERE tenant_id = $2),
       (SELECT count(*) FROM analytics_events WHERE project_id = $1)`, s.Project.ID, s.Tenant.ID).
		Scan(&s.Uncarried.APIKeys, &s.Uncarried.AIProviders, &s.Uncarried.AnalyticsEvents)
}

// IsRefusal reports whether err is the importer refusing the database
// itself (not a restore, not v0.3's schema, filtered by RLS) rather than
// failing to reach it.
func IsRefusal(err error) bool {
	var (
		nr *NotARestoreError
		se *SchemaError
		rs *RowSecurityError
	)
	return errors.As(err, &nr) || errors.As(err, &se) || errors.As(err, &rs)
}
