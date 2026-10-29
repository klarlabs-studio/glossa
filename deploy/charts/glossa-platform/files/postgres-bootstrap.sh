#!/usr/bin/env bash
# Converges the in-namespace Postgres for glossa-platform. Runs as two
# hook Jobs in the postgres image, connected as the superuser (PGUSER,
# PGPASSWORD), around the migration Job. Idempotent: every statement
# converges on the state below whether it ran before or not, so upgrades
# re-run it safely and a rotated password in a Secret is applied.
#
#   PHASE=init       (<fullname>-postgres-init, before the migration)
#     role OWNER         LOGIN CREATEROLE, never SUPERUSER/BYPASSRLS, PASSWORD OWNER_PASSWORD
#     database DATABASE  OWNER OWNER (never re-owned: a different owner fails)
#     role BACKUP_USER   LOGIN BYPASSRLS, member of pg_read_all_data (only when BACKUP_USER is set)
#     role glossa_app    LOGIN PASSWORD APP_PASSWORD, only if it exists already
#                        (upgrades: a rotated password is live before new server pods start)
#   PHASE=app-login  (<fullname>-postgres-app-login, after the migration)
#     role glossa_app    LOGIN PASSWORD APP_PASSWORD, never SUPERUSER/BYPASSRLS/CREATEROLE/CREATEDB.
#                        The first migration creates it NOLOGIN (as the owner, which
#                        so holds ADMIN OPTION on it); this only lets it log in.
#
# Environment (set by the chart):
#   PGHOST, PGPORT, PGUSER, PGPASSWORD   the superuser's connection
#   PHASE                                init | app-login
#   DATABASE, OWNER, OWNER_PASSWORD, APP_PASSWORD
#   BACKUP_USER, BACKUP_PASSWORD         optional (backups on)
#   WAIT_SECONDS                         how long to wait for Postgres
set -euo pipefail

log() { printf 'postgres-bootstrap[%s]: %s\n' "${PHASE:-?}" "$*"; }
fail() { printf 'postgres-bootstrap[%s]: ERROR: %s\n' "${PHASE:-?}" "$*" >&2; exit 1; }

# The chart's DSNs carry passwords single-quoted in keyword/value form
# (no percent-encoding needed); a ' or \ would break them.
check_password() { # name value
  [[ -n $2 ]] || fail "$1 is empty"
  case $2 in *\'* | *\\*) fail "$1 contains ' or \\; generate it with: openssl rand -hex 24" ;; esac
}

# psql as the superuser against the maintenance database; statements on stdin.
sql() { psql -X -q -v ON_ERROR_STOP=1 -d postgres "$@"; }
# One value from a query.
value() { psql -X -At -v ON_ERROR_STOP=1 -d postgres "$@"; }

case ${PHASE:-} in init | app-login) ;; *) fail "PHASE must be init or app-login, got '${PHASE:-}'" ;; esac
[[ $OWNER =~ ^[a-z_][a-z0-9_]*$ ]] || fail "owner name '$OWNER' is not a plain lower-case identifier"
[[ $OWNER != "$PGUSER" && $OWNER != glossa_app && $OWNER != glossa_system ]] ||
  fail "the owner must be its own role, not '$OWNER'"
check_password "the owner's password" "$OWNER_PASSWORD"
check_password "glossa_app's password" "$APP_PASSWORD"
if [[ -n ${BACKUP_USER:-} ]]; then
  [[ $BACKUP_USER =~ ^[a-z_][a-z0-9_]*$ ]] || fail "backup role name '$BACKUP_USER' is not a plain lower-case identifier"
  [[ $BACKUP_USER != "$PGUSER" && $BACKUP_USER != "$OWNER" && $BACKUP_USER != glossa_app && $BACKUP_USER != glossa_system ]] ||
    fail "the backup role must be its own role, not '$BACKUP_USER'"
  check_password "the backup role's password" "${BACKUP_PASSWORD:-}"
fi

# 1. Wait for Postgres (a fresh StatefulSet may still be attaching its
#    Longhorn volume or running initdb when the hook starts).
deadline=$((SECONDS + ${WAIT_SECONDS:-300}))
until err=$(value -c 'SELECT 1' 2>&1 >/dev/null); do
  ((SECONDS < deadline)) || fail "Postgres at $PGHOST:${PGPORT:-5432} is not ready after ${WAIT_SECONDS:-300}s: $err"
  log "waiting for Postgres at $PGHOST:${PGPORT:-5432}"
  sleep 5
done
[[ $(value -c 'SELECT rolsuper FROM pg_roles WHERE rolname = current_user') == t ]] ||
  fail "$PGUSER is not a superuser; postgres.superuser must name the image's superuser"

# glossa_app: LOGIN with its password and nothing more.
app_login() {
  sql -v app_pw="$APP_PASSWORD" <<'SQL'
ALTER ROLE glossa_app WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'app_pw';
SQL
  log "glossa_app may log in"
}
app_exists() { [[ $(value -c "SELECT count(*) FROM pg_roles WHERE rolname = 'glossa_app'") == 1 ]]; }

if [[ $PHASE == init ]]; then
  # 2. The schema owner. CREATEROLE lets the first migration create
  #    glossa_app and glossa_system (and hold ADMIN OPTION on them).
  sql -v owner="$OWNER" -v owner_pw="$OWNER_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I', :'owner')
 WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'owner') \gexec
ALTER ROLE :"owner" WITH LOGIN CREATEROLE NOSUPERUSER NOCREATEDB NOREPLICATION NOBYPASSRLS PASSWORD :'owner_pw';
SQL
  log "role $OWNER present (LOGIN CREATEROLE, not a superuser)"

  # 3. The database, owned by the schema owner.
  sql -v owner="$OWNER" -v db="$DATABASE" <<'SQL'
SELECT format('CREATE DATABASE %I OWNER %I', :'db', :'owner')
 WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db') \gexec
SQL
  dbowner=$(value -v db="$DATABASE" <<'SQL'
SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = :'db';
SQL
  )
  [[ $dbowner == "$OWNER" ]] ||
    fail "database $DATABASE exists but is owned by $dbowner, not $OWNER; fix it by hand (ALTER DATABASE … OWNER TO …)"
  log "database $DATABASE present, owned by $OWNER"

  # 4. The backup role: reads everything (pg_read_all_data), past row
  #    level security (BYPASSRLS; pg_dump refuses to dump a table whose
  #    policies would filter it), and can change nothing.
  if [[ -n ${BACKUP_USER:-} ]]; then
    sql -v backup="$BACKUP_USER" -v backup_pw="$BACKUP_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I', :'backup')
 WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'backup') \gexec
ALTER ROLE :"backup" WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION BYPASSRLS PASSWORD :'backup_pw';
SELECT format('GRANT pg_read_all_data TO %I', :'backup')
 WHERE NOT pg_has_role(:'backup', 'pg_read_all_data', 'MEMBER') \gexec
SQL
    log "role $BACKUP_USER present (read-only, BYPASSRLS)"
  fi

  # 5. On upgrades glossa_app exists: apply a rotated password now.
  if app_exists; then
    app_login
  else
    log "glossa_app does not exist yet; the first migration creates it"
  fi
else
  app_exists || fail "glossa_app does not exist: the migration Job should have created it"
  app_login
fi

[[ $(value -c "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = 'glossa_app'" 2>/dev/null) != t ]] ||
  fail "glossa_app is a superuser or BYPASSRLS; row level security would not bind it"
log "done"
