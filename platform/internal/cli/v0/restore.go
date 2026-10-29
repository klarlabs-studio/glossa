package v0

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The restore marker (RFC 0006 §7.2). `--v0-db` reads a v0.3 database
// directly, so it refuses any database that is not a marked restore:
// pointed at v0.3's live database by mistake, it must not run. A
// database is a restore when, and only when, all of these hold — there
// is no flag that skips them:
//
//  1. the table MarkerTable exists and holds exactly one row;
//  2. that row names this database: its OID and name are those of
//     current_database(), so a dump of a marked database restored
//     anywhere else is not marked (a fresh database gets a fresh OID);
//  3. the database itself — not the session, which the importer sets
//     read-only on its own connection — defaults to read-only
//     transactions, which v0.3's API cannot write under, so a live v0.3
//     database never has it.
//
// platform/scripts/v0-restore.sh creates a new database, restores the
// dump into it, and runs platform/scripts/v0-restore-marker.sql, which
// writes all three.
const (
	MarkerSchema = "glossa_v0_restore"
	MarkerTable  = MarkerSchema + ".marker"
	// RestoreScript is how an operator makes a restore.
	RestoreScript = "platform/scripts/v0-restore.sh <dump.sql.gz> <new-database>"
)

// Restore is what the marker says about the backup being read.
type Restore struct {
	Database   string    `json:"database"`
	DumpName   string    `json:"dump_name"`
	DumpSHA256 string    `json:"dump_sha256"`
	RestoredAt time.Time `json:"restored_at"`
	RestoredBy string    `json:"restored_by"`
}

// NotARestoreError is the refusal of a database that isn't a marked
// restore. Reason says which of the three conditions failed.
type NotARestoreError struct {
	Database string
	Reason   string
}

func (e *NotARestoreError) Error() string {
	return fmt.Sprintf("database %q is not a marked v0.3 restore: %s", e.Database, e.Reason)
}

// markerFacts is what the importer reads to decide; decideRestore is the
// decision, kept pure so every refusal is unit-tested.
type markerFacts struct {
	database      string
	databaseOID   uint32
	tableExists   bool
	rows          []markerRow
	readOnlyByDef bool
}

type markerRow struct {
	databaseOID  uint32
	databaseName string
	restore      Restore
}

func decideRestore(f markerFacts) (Restore, error) {
	refuse := func(format string, args ...any) (Restore, error) {
		return Restore{}, &NotARestoreError{Database: f.database, Reason: fmt.Sprintf(format, args...)}
	}
	switch {
	case !f.tableExists:
		return refuse("it has no %s table, which only %s writes", MarkerTable, RestoreScript)
	case len(f.rows) != 1:
		return refuse("%s holds %d rows, not exactly one", MarkerTable, len(f.rows))
	}
	m := f.rows[0]
	switch {
	case m.databaseOID != f.databaseOID || m.databaseName != f.database:
		return refuse("the marker was written for database %q (oid %d), not this one (%q, oid %d): a marked database was copied, so this copy is not a restore",
			m.databaseName, m.databaseOID, f.database, f.databaseOID)
	case !f.readOnlyByDef:
		return refuse("the database does not default to read-only transactions (default_transaction_read_only is not set on it), so it may be a live, writable v0.3 database")
	}
	r := m.restore
	r.Database = f.database
	return r, nil
}

// readMarker gathers the facts inside the importer's read-only
// transaction.
func readMarker(ctx context.Context, tx pgx.Tx) (markerFacts, error) {
	var (
		f   markerFacts
		oid int64
	)
	err := tx.QueryRow(ctx, `
SELECT d.datname, d.oid::int8,
       to_regclass($1) IS NOT NULL,
       EXISTS (SELECT 1 FROM pg_db_role_setting s, unnest(s.setconfig) c
               WHERE s.setdatabase = d.oid AND s.setrole = 0 AND c = 'default_transaction_read_only=on')
FROM pg_database d WHERE d.datname = current_database()`, MarkerTable).
		Scan(&f.database, &oid, &f.tableExists, &f.readOnlyByDef)
	f.databaseOID = uint32(oid)
	if err != nil || !f.tableExists {
		return f, err
	}
	rows, err := tx.Query(ctx, `SELECT database_oid::int8, database_name::text, dump_name, dump_sha256, restored_at, restored_by::text FROM `+MarkerTable)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var m markerRow
		if err := rows.Scan(&oid, &m.databaseName, &m.restore.DumpName, &m.restore.DumpSHA256,
			&m.restore.RestoredAt, &m.restore.RestoredBy); err != nil {
			return f, err
		}
		m.databaseOID, m.restore.RestoredAt = uint32(oid), m.restore.RestoredAt.UTC()
		f.rows = append(f.rows, m)
	}
	return f, rows.Err()
}

// requiredColumns is the v0.3 schema the importer reads: every column
// as of apps/api's migration 0006. A database without them is not a
// v0.3 backup (or an older one), and reading it would misreport gaps as
// absent data.
var requiredColumns = map[string][]string{
	"tenants":      {"id", "slug", "name"},
	"projects":     {"id", "tenant_id", "slug", "name", "default_locale"},
	"locales":      {"id", "project_id", "code", "label", "enabled"},
	"keys":         {"id", "project_id", "key", "description", "first_seen_at"},
	"translations": {"id", "key_id", "locale_id", "value", "status", "updated_by", "updated_at"},
	"users":        {"id", "tenant_id", "email", "role", "locales", "created_at"},
	"audit_log": {"id", "tenant_id", "translation_id", "before_value", "after_value", "changed_by",
		"actor_kind", "actor_label", "changed_at"},
	"project_api_keys":         {"project_id"},
	"ai_translation_providers": {"tenant_id"},
	"analytics_events":         {"project_id"},
}

func checkSchema(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT table_name::text, column_name::text FROM information_schema.columns WHERE table_schema = 'public'`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			rows.Close()
			return err
		}
		have[t+"."+c] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var missing []string
	for _, t := range sortedKeys(requiredColumns) {
		for _, c := range requiredColumns[t] {
			if !have[t+"."+c] {
				missing = append(missing, t+"."+c)
			}
		}
	}
	if len(missing) > 0 {
		return &SchemaError{Missing: missing}
	}
	return nil
}

// SchemaError is a restore that isn't v0.3's schema at migration 0006.
type SchemaError struct{ Missing []string }

func (e *SchemaError) Error() string {
	return "not Glossa v0.3's schema (apps/api migrations through 0006): missing " + strings.Join(e.Missing, ", ")
}

// RowSecurityError is a role that row-level security would filter. The
// importer's session runs with row_security = off, so Postgres raises
// an error instead of returning fewer rows: a silently empty read would
// be a silent data loss.
type RowSecurityError struct{ Err error }

func (e *RowSecurityError) Error() string {
	return "row-level security would hide v0.3 rows from this role: connect as the restored database's owner or a superuser (the role v0-restore.sh ran as)"
}

func (e *RowSecurityError) Unwrap() error { return e.Err }

func classify(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "42501" && strings.Contains(pg.Message, "row-level security") {
		return &RowSecurityError{Err: err}
	}
	return err
}
