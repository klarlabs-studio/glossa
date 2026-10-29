//go:build integration

package db_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ── RLS guard ────────────────────────────────────────────────────────
//
// The guard inspects the catalog after all migrations have run, so it
// covers every table any bounded context will ever add. A new table
// either carries tenant_id and is locked down, or is consciously listed
// below with a reason. There is no third option.

// tenantRoots are tenant-owned tables keyed by the tenant id itself.
var tenantRoots = []string{"tenants"}

// globalTables hold no tenant data and are outside row-level security.
// Each entry needs a reason. Prefer systemTables.
var globalTables = map[string]string{
	"schema_migrations": "golang-migrate bookkeeping; no grants to glossa_app",
}

// systemTables hold deployment-wide data that exists before a tenant is
// known. They have no tenant_id, yet still ENABLE + FORCE row-level
// security; glossa_app may not write them and may read them only through
// a PUBLIC SELECT policy scoped by app_current_tenant(). Each entry needs
// a reason.
var systemTables = map[string]string{
	"identity_people":              "a person spans all their tenants and signs in before one is chosen",
	"identity_sessions":            "a session cookie is resolved before the tenant is known",
	"identity_email_links":         "sign-in and reset links are redeemed before the tenant is known",
	"identity_totp":                "a person's second factor, checked at sign-in",
	"identity_passkeys":            "a person's passkeys, checked at sign-in",
	"identity_webauthn_ceremonies": "passkey challenges in flight, before the tenant is known",
	"identity_login_attempts":      "brute-force counters per email, checked at sign-in",
	"identity_device_authorizations": "a device signing a person in (RFC 0006 §7.2) asks and polls before any tenant is known, " +
		"and the session it receives spans all the person's tenants",
	"system_leases": "which replica leads a periodic job, and when it last ran; a deployment's own bookkeeping, never a tenant's",
}

// systemPolicies are the only policies allowed to target a role other
// than PUBLIC. Each opens a table to a tenantless path; keep it short.
var systemPolicies = map[string][]string{
	// The outbox relay claims and settles events across tenants.
	"outbox_events": {"outbox_events_system_select", "outbox_events_system_update"},
	// GET /v1/me lists the tenants a person belongs to.
	"tenants": {"tenants_system_select"},
	// Validating /v1/tenants/{tenant} against the session's memberships,
	// and finding invitations for a verified email at sign-in.
	"identity_members": {"identity_members_system_select"},
	// Intelligence's job workers claim across tenants (system scope
	// intelligence.jobs) under each tenant's concurrency cap.
	"intelligence_jobs":     {"intelligence_jobs_system_select", "intelligence_jobs_system_update"},
	"intelligence_settings": {"intelligence_settings_system_select"},
	// Integration's job workers claim import and export jobs, and its
	// retention sweep deletes expired files, across tenants (system
	// scope integration.jobs).
	"integration_jobs": {"integration_jobs_system_select", "integration_jobs_system_update"},
	// The audit export workers claim jobs and the retention sweep finds
	// expired exports across tenants (migration 0053).
	"audit_export_jobs": {"audit_export_jobs_system_select", "audit_export_jobs_system_update"},
	// The GitHub webhook inbox (system scope integration.github,
	// RFC 0004 §6.2). A delivery is stored before any tenant is known —
	// GitHub signs it, the tenant follows from the installation — so the
	// endpoint and the worker reach the whole table; the tenant policy
	// beside this one shows a tenant its own settled deliveries once
	// tenant_id is set. Resolving the installation reads the mapping and
	// the state only, and the sweep drops expired install intents.
	"integration_github_deliveries":    {"integration_github_deliveries_system"},
	"integration_github_installations": {"integration_github_installations_system_select"},
	"integration_github_install_intents": {
		"integration_github_install_intents_system_select",
		"integration_github_install_intents_system_delete",
	},
	// The Glossa PR check's queue (the same system scope, RFC 0004
	// §6.4). The check worker claims the oldest due row whatever tenant
	// it belongs to and only then enters that tenant's scope to read its
	// catalog, exactly as the inbox worker does; the tenant policy beside
	// this one shows a tenant its own checks.
	"integration_github_checks": {"integration_github_checks_system"},
	// Context's retention sweep finds the projects holding builds across
	// tenants (system scope context.retention; tenant_id and project_id
	// only), then purges each in its tenant's scope.
	"context_builds": {"context_builds_system_select"},
	// The purge scheduler's own lease: which replica leads a periodic
	// job (system scope scheduler.lease). No tenant, no glossa_app grant.
	"system_leases": {"system_leases_system"},
	// Release's publisher finds due branch-environment publishes (system
	// scope release.publisher), and its key index task the keys whose
	// index object predates scopes (release.key_index): IDs, times and
	// format versions only, read-only; the work runs in tenant scope.
	"release_publish_requests": {"release_publish_requests_system_select"},
	"release_delivery_keys":    {"release_delivery_keys_system_select"},
	// The rollout sweep finds active rollouts past their max_duration
	// and counts the active ones (system scope release.rollout_sweeper,
	// RFC 0006 §5.2): ids, status and expiry only, read-only; aborting
	// one runs in its tenant's scope.
	"release_rollouts": {"release_rollouts_system_select"},
	// The daily catalog.proposals job finds the tenants holding proposed
	// messages whose branches all closed long ago (system scope
	// catalog.proposals): branch identity and closing time, proposal
	// links and message states only, read-only; the sweep itself runs in
	// each tenant's scope.
	"catalog_branches":  {"catalog_branches_system_select"},
	"catalog_proposals": {"catalog_proposals_system_select"},
	"catalog_messages":  {"catalog_messages_system_select"},
	// The daily quality.sweep job finds the tenants holding a waiver
	// past its date or a check run past its 90 days (system scope
	// quality.sweep, RFC 0005 §2.2, §2.3): the expiry columns and a
	// start time only, read-only. No reason, no fingerprint, no ref —
	// nothing a finding says about anybody's copy. Both sweeps
	// themselves run in each tenant's own scope.
	"quality_waivers":    {"quality_waivers_system_select"},
	"quality_check_runs": {"quality_check_runs_system_select"},
	// Workflow's timer sweep finds the tenants holding an instance whose
	// timer has fallen due (system scope workflow.timers, RFC 0006
	// §2.3): the tenant, the status and the timer columns only,
	// read-only. Raising each timer runs in the tenant's own scope.
	"workflow_instances": {"workflow_instances_system_select"},
	// The same sweep counts the live assignments for
	// glossa_assignments_open{overdue} (RFC 0006 §10.1, migration 0055):
	// the state and the due date only, read-only. No assignee, no
	// project, no unit.
	"workflow_assignments": {"workflow_assignments_system_select"},
	// Resolving a bearer token's tenant by hash; bumping last_used_at.
	"identity_api_tokens": {"identity_api_tokens_system_select", "identity_api_tokens_system_touch"},
	// A CORS preflight carries no credentials, so "is this a registered
	// preview origin?" is answered before any tenant is known
	// (RFC 0004 §5.2); the grant's own project binding does the rest.
	"identity_preview_origins": {"identity_preview_origins_system_select"},
	// An in-context grant names its own tenant, like an API token, so it
	// is resolved in system scope; using one bumps last_used_at, and the
	// sweep drops the ones past their fifteen minutes.
	"identity_in_context_grants": {
		"identity_in_context_grants_system_select",
		"identity_in_context_grants_system_touch",
		"identity_in_context_grants_system_sweep",
	},
	// A CI token names its own tenant too (RFC 0004 §6.3), so it is
	// resolved the same way; using one bumps last_used_at, and the sweep
	// drops the ones past their half hour.
	"identity_ci_tokens": {
		"identity_ci_tokens_system_select",
		"identity_ci_tokens_system_touch",
		"identity_ci_tokens_system_sweep",
	},
	// The GitHub Actions OIDC exchange arrives with no tenant: which
	// tenant a CI run belongs to is what its verified repository_id
	// proves, and that mapping is the Git connections (RFC 0004 §6.3).
	// The read is the mapping columns only — no repository name, no
	// creator — so a verified run learns which of its own projects it
	// may act on and nothing about anyone else's.
	"integration_git_connections": {"integration_git_connections_system_select"},
	// Identity's global tables are system scope only (see systemTables).
	"identity_people":                {"identity_people_system"},
	"identity_sessions":              {"identity_sessions_system"},
	"identity_email_links":           {"identity_email_links_system"},
	"identity_totp":                  {"identity_totp_system"},
	"identity_passkeys":              {"identity_passkeys_system"},
	"identity_webauthn_ceremonies":   {"identity_webauthn_ceremonies_system"},
	"identity_login_attempts":        {"identity_login_attempts_system"},
	"identity_device_authorizations": {"identity_device_authorizations_system"},
}

type tableSecurity struct {
	name                 string
	hasTenantID          bool
	rowSecurity, forced  bool
	hasTenantAllPolicy   bool
	nonTenantPolicies    []string
	nonPublicPolicyNames []string
	// publicPolicies are PUBLIC policies as "name:cmd".
	publicPolicies []string
	// appWrites and appReads report glossa_app's table or column privileges.
	appWrites, appReads bool
}

// guardedTables must exist and be tenant-owned: tables whose loss of
// isolation would leak unreleased copy (RFC 0004 §4.1's branch overlay
// holds text that exists only on feature branches). The guard checks
// every table anyway; listing them here also fails if a migration drops
// one or loses its tenant_id.
var guardedTables = []string{
	"catalog_branches", "catalog_proposals",
	// A Git connection names a tenant's repositories and projects, and
	// an installation names their GitHub account (RFC 0004 §6.1).
	"integration_github_installations", "integration_git_connections",
	// The audit trail says who did what in a tenant (RFC 0006 §6.1).
	"audit_entries",
}

func TestRLSGuard(t *testing.T) {
	tables := loadTableSecurity(t)
	if len(tables) == 0 {
		t.Fatal("no tables found; did migrations run?")
	}
	for _, name := range guardedTables {
		i := slices.IndexFunc(tables, func(ts tableSecurity) bool { return ts.name == name })
		if i < 0 || !tables[i].hasTenantID {
			t.Errorf("%s: missing, or has no tenant_id", name)
		}
	}
	for _, tbl := range tables {
		t.Run(tbl.name, func(t *testing.T) { checkTable(t, tbl) })
	}
}

func checkTable(t *testing.T, tbl tableSecurity) {
	tenantOwned := tbl.hasTenantID || slices.Contains(tenantRoots, tbl.name)
	if _, ok := systemTables[tbl.name]; ok && !tenantOwned {
		checkSystemTable(t, tbl)
		return
	}
	if !tenantOwned {
		if _, ok := globalTables[tbl.name]; !ok {
			t.Errorf("%s has no tenant_id column: add tenant_id with RLS, or list it in systemTables or globalTables with a reason", tbl.name)
		}
		return
	}
	if !tbl.rowSecurity {
		t.Errorf("%s: ROW LEVEL SECURITY is not enabled", tbl.name)
	}
	if !tbl.forced {
		t.Errorf("%s: ROW LEVEL SECURITY is not FORCEd (the owner would bypass it)", tbl.name)
	}
	if !tbl.hasTenantAllPolicy {
		t.Errorf("%s: needs a PUBLIC FOR ALL policy with USING and WITH CHECK on app_current_tenant()", tbl.name)
	}
	for _, p := range tbl.nonTenantPolicies {
		t.Errorf("%s: PUBLIC policy %s does not constrain on app_current_tenant(); it would open the table", tbl.name, p)
	}
	allowed := systemPolicies[tbl.name]
	for _, p := range tbl.nonPublicPolicyNames {
		if !slices.Contains(allowed, p) {
			t.Errorf("%s: role-specific policy %s is not in systemPolicies", tbl.name, p)
		}
	}
}

// checkSystemTable holds a tenantless table to system scope: RLS on and
// forced, no writes from glossa_app, and reads only through a PUBLIC
// SELECT policy that is itself scoped to the current tenant.
func checkSystemTable(t *testing.T, tbl tableSecurity) {
	if !tbl.rowSecurity || !tbl.forced {
		t.Errorf("%s: system table must ENABLE and FORCE row level security", tbl.name)
	}
	allowed := systemPolicies[tbl.name]
	for _, p := range tbl.nonPublicPolicyNames {
		if !slices.Contains(allowed, p) {
			t.Errorf("%s: role-specific policy %s is not in systemPolicies", tbl.name, p)
		}
	}
	for _, p := range tbl.nonTenantPolicies {
		t.Errorf("%s: PUBLIC policy %s does not constrain on app_current_tenant()", tbl.name, p)
	}
	for _, p := range tbl.publicPolicies {
		if !strings.HasSuffix(p, ":r") {
			t.Errorf("%s: PUBLIC policy %s must be FOR SELECT", tbl.name, p)
		}
	}
	if tbl.appWrites {
		t.Errorf("%s: glossa_app must not insert, update or delete a system table", tbl.name)
	}
	if tbl.appReads && len(tbl.publicPolicies) == 0 {
		t.Errorf("%s: glossa_app may read it but no tenant-scoped policy limits what it sees", tbl.name)
	}
}

func loadTableSecurity(t *testing.T) []tableSecurity {
	t.Helper()
	ctx := context.Background()
	rows, err := env.Super.Query(ctx, `
		SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
		       EXISTS (SELECT FROM pg_attribute a
		               WHERE a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (tableSecurity, error) {
		var ts tableSecurity
		err := r.Scan(&ts.name, &ts.rowSecurity, &ts.forced, &ts.hasTenantID)
		return ts, err
	})
	if err != nil {
		t.Fatalf("scan tables: %v", err)
	}
	for i := range tables {
		loadPolicies(t, &tables[i])
		loadAppPrivileges(t, &tables[i])
	}
	return tables
}

func loadAppPrivileges(t *testing.T, ts *tableSecurity) {
	t.Helper()
	err := env.Super.QueryRow(context.Background(), `
		SELECT has_table_privilege('glossa_app', $1, 'INSERT')
		    OR has_table_privilege('glossa_app', $1, 'UPDATE')
		    OR has_table_privilege('glossa_app', $1, 'DELETE')
		    OR has_any_column_privilege('glossa_app', $1, 'INSERT')
		    OR has_any_column_privilege('glossa_app', $1, 'UPDATE'),
		       has_any_column_privilege('glossa_app', $1, 'SELECT')`,
		"public."+ts.name).Scan(&ts.appWrites, &ts.appReads)
	if err != nil {
		t.Fatalf("privileges of %s: %v", ts.name, err)
	}
}

func loadPolicies(t *testing.T, ts *tableSecurity) {
	t.Helper()
	rows, err := env.Super.Query(context.Background(), `
		SELECT p.polname, p.polcmd::text, p.polpermissive, p.polroles = '{0}'::oid[],
		       coalesce(pg_get_expr(p.polqual, p.polrelid), ''),
		       coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '')
		FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = $1`, ts.name)
	if err != nil {
		t.Fatalf("list policies of %s: %v", ts.name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, cmd, using, check string
		var permissive, public bool
		if err := rows.Scan(&name, &cmd, &permissive, &public, &using, &check); err != nil {
			t.Fatalf("scan policy: %v", err)
		}
		classifyPolicy(ts, name, cmd, permissive, public, using, check)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("policies of %s: %v", ts.name, err)
	}
}

func classifyPolicy(ts *tableSecurity, name, cmd string, permissive, public bool, using, check string) {
	const fn = "app_current_tenant()"
	if !public {
		ts.nonPublicPolicyNames = append(ts.nonPublicPolicyNames, name)
		return
	}
	ts.publicPolicies = append(ts.publicPolicies, name+":"+cmd)
	scoped := (using == "" || strings.Contains(using, fn)) && (check == "" || strings.Contains(check, fn))
	if permissive && !scoped {
		ts.nonTenantPolicies = append(ts.nonTenantPolicies, name)
	}
	if permissive && cmd == "*" && strings.Contains(using, fn) && strings.Contains(check, fn) {
		ts.hasTenantAllPolicy = true
	}
}

// ── Isolation suite ──────────────────────────────────────────────────

const rlsViolation = "42501" // insufficient_privilege: RLS WITH CHECK or missing grant

func asTenant(t *testing.T, tenant tenancy.ID, fn func(context.Context, *db.TenantTx) error) error {
	t.Helper()
	ctx := tenancy.ContextWithTenant(context.Background(), tenant)
	return db.NewUnitOfWork(env.App).InTenantTx(ctx, fn)
}

func wantPgCode(t *testing.T, err error, code, what string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Errorf("%s: err = %v, want SQLSTATE %s", what, err, code)
	}
}

func TestIsolationTenants(t *testing.T) {
	reset(t)
	a, b := seedTenant(t, "acme"), seedTenant(t, "bolt")

	err := asTenant(t, a, func(ctx context.Context, tx *db.TenantTx) error {
		var ids []string
		rows, err := tx.Query(ctx, "SELECT id::text FROM tenants")
		if err != nil {
			return err
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] != a.String() {
			t.Errorf("tenant A sees tenants %v, want only itself", ids)
		}

		tag, err := tx.Exec(ctx, "UPDATE tenants SET name = 'pwned' WHERE id = $1", b.UUID())
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 0 {
			t.Errorf("tenant A updated %d of tenant B's rows", tag.RowsAffected())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = asTenant(t, a, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx,
			"INSERT INTO tenants (id, kind, slug, name) VALUES ($1, 'individual', 'sneaky', 'Sneaky')",
			tenancy.NewID().UUID())
		return err
	})
	wantPgCode(t, err, rlsViolation, "tenant A inserting another tenant")

	var name string
	if err := env.Super.QueryRow(context.Background(), "SELECT name FROM tenants WHERE id = $1", b.UUID()).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name == "pwned" {
		t.Error("tenant B's row was modified")
	}
}

func TestIsolationOutbox(t *testing.T) {
	reset(t)
	a, b := seedTenant(t, "acme"), seedTenant(t, "bolt")
	seedEvent(t, a)
	seedEvent(t, b)
	seedEvent(t, b)

	err := asTenant(t, a, func(ctx context.Context, tx *db.TenantTx) error {
		var foreign int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE tenant_id <> $1", a.UUID()).Scan(&foreign); err != nil {
			return err
		}
		if foreign != 0 {
			t.Errorf("tenant A sees %d of tenant B's events", foreign)
		}
		n, err := countRows(ctx, tx, "outbox_events")
		if err == nil && n != 1 {
			t.Errorf("tenant A sees %d events, want 1", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	err = asTenant(t, a, func(ctx context.Context, tx *db.TenantTx) error {
		return insertEvent(ctx, tx, b)
	})
	wantPgCode(t, err, rlsViolation, "tenant A publishing as tenant B")

	err = asTenant(t, a, func(ctx context.Context, tx *db.TenantTx) error {
		_, err := tx.Exec(ctx, "UPDATE outbox_events SET status = 'dead'")
		return err
	})
	wantPgCode(t, err, rlsViolation, "request path updating the outbox")
}

func TestIsolationWithoutTenantContext(t *testing.T) {
	reset(t)
	a := seedTenant(t, "acme")
	seedEvent(t, a)
	ctx := context.Background()

	// A raw transaction that never set app.tenant_id — what a handler
	// bypassing the unit of work would get.
	tx, err := env.App.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, table := range []string{"tenants", "outbox_events"} {
		n, err := countRows(ctx, tx, table)
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("unscoped transaction sees %d rows of %s", n, table)
		}
	}

	_, err = tx.Exec(ctx, "SAVEPOINT s1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx,
		"INSERT INTO tenants (id, kind, slug, name) VALUES ($1, 'individual', 'ghost', 'Ghost')",
		tenancy.NewID().UUID())
	wantPgCode(t, err, rlsViolation, "unscoped tenant insert")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT s1"); err != nil {
		t.Fatal(err)
	}

	err = insertEvent(ctx, tx, a)
	wantPgCode(t, err, rlsViolation, "unscoped outbox insert")
}
