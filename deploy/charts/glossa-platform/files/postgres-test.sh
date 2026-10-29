#!/usr/bin/env bash
# `helm test` for the in-namespace Postgres (the Pod <fullname>-postgres-test,
# postgres image). Connects the way glossa-server does, as glossa_app, and
# checks what row level security depends on:
#   - glossa_app logs in with the password from postgres.app,
#   - it is not a superuser, BYPASSRLS, CREATEROLE or CREATEDB role,
#   - it cannot SET ROLE to any role that is (the superuser, the owner,
#     the backup role),
#   - it can SET ROLE glossa_system, as the server's system scope must.
# Read-only: it changes nothing.
#
# Environment: PGHOST, PGPORT, PGDATABASE, PGUSER (glossa_app), PGPASSWORD.
set -euo pipefail

pass() { printf 'ok   %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*" >&2; exit 1; }
q() { psql -X -q -At -v ON_ERROR_STOP=1 "$@"; }

# A new pod's first connections can be refused for a moment while its
# network policy is programmed (k3s): wait for the server before judging it.
for _ in $(seq 1 30); do pg_isready -q -t 2 && break; sleep 1; done

me=$(q -c 'SELECT current_user' 2>&1) || fail "glossa_app cannot connect to $PGHOST/$PGDATABASE: $me"
[[ $me == glossa_app ]] || fail "connected as $me, not glossa_app"
pass "glossa_app connects to $PGDATABASE"

attrs=$(q -c "SELECT rolsuper, rolbypassrls, rolcreaterole, rolcreatedb FROM pg_roles WHERE rolname = current_user")
[[ $attrs == 'f|f|f|f' ]] ||
  fail "glossa_app has privileged attributes (superuser|bypassrls|createrole|createdb = $attrs)"
pass "glossa_app is not superuser, BYPASSRLS, CREATEROLE or CREATEDB"

privileged=$(q -c "SELECT rolname FROM pg_roles WHERE (rolsuper OR rolbypassrls OR rolcreaterole) AND rolname <> current_user ORDER BY 1")
[[ -n $privileged ]] || fail "no superuser is visible in pg_roles; the check would prove nothing"
while IFS= read -r role; do
  if out=$(q -v r="$role" 2>&1 <<'SQL'
SET ROLE :"r";
SQL
  ); then
    fail "glossa_app can SET ROLE $role (superuser, BYPASSRLS or CREATEROLE): row level security does not bind it"
  fi
  [[ $out == *"permission denied"* ]] || fail "SET ROLE $role failed, but not for lack of permission: $out"
  pass "glossa_app cannot SET ROLE $role"
done <<<"$privileged"

sys=$(q -c 'SET ROLE glossa_system' -c 'SELECT current_user' 2>&1) ||
  fail "glossa_app cannot SET ROLE glossa_system (the server refuses to start then): $sys"
[[ $sys == glossa_system ]] || fail "SET ROLE glossa_system ended as $sys"
pass "glossa_app can SET ROLE glossa_system"
