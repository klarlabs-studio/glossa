#!/bin/sh
# The first half of the weekly restore drill (the init container "fetch"
# of <fullname>-postgres-restore-test, rclone image, busybox sh): picks the
# newest dump on the remote, refuses one older than MAX_AGE_HOURS (a drill
# that keeps restoring an old dump while the nightly backup has silently
# stopped proves nothing), downloads it, decrypts it with the drill key and
# checks the archive.
#
# Environment (set by the chart):
#   RCLONE_CONFIG, REMOTE, PREFIX, OUT_DIR, MAX_AGE_HOURS
#   TOOLS             the directory with age (fetch-age)
#   DRILL_IDENTITY    the drill key's identity file
set -eu

log() { printf 'restore-fetch: %s\n' "$*"; }
fail() { printf 'restore-fetch: ERROR: %s\n' "$*" >&2; exit 1; }

# Anchored on this release's own file names: a bare `sort | tail -1` would
# happily pick anything that lands in the directory later.
latest=$(rclone lsf --files-only --max-depth 1 "$REMOTE/" |
  grep -E "^$PREFIX-[0-9]{8}T[0-9]{6}Z\.sql\.gz\.age\$" | sort | tail -n 1 || true)
[ -n "$latest" ] || fail "no encrypted $PREFIX-*.sql.gz.age dumps in $REMOTE"

ts=${latest#"$PREFIX"-}
ts=${ts%.sql.gz.age}
taken=$(date -u -D '%Y%m%dT%H%M%SZ' -d "$ts" +%s) || fail "cannot parse the timestamp of $latest"
age_hours=$((($(date +%s) - taken) / 3600))
[ "$age_hours" -le "$MAX_AGE_HOURS" ] ||
  fail "the newest dump, $latest, is ${age_hours}h old (limit ${MAX_AGE_HOURS}h): is the nightly backup failing?"

rclone copy --no-traverse "$REMOTE/$latest" "$OUT_DIR/"
plain=${latest%.age}
"$TOOLS/age" -d -i "$DRILL_IDENTITY" -o "$OUT_DIR/$plain" "$OUT_DIR/$latest" ||
  fail "$latest does not decrypt with the drill identity"
rm -f "$OUT_DIR/$latest"
# A truncated or partial upload shows here, before Postgres is involved.
gzip -t "$OUT_DIR/$plain" || fail "$plain is corrupt"
printf '%s\n' "$plain" >"$OUT_DIR/dump.name"
log "fetched $latest (${age_hours}h old), decrypted to $plain ($(stat -c %s "$OUT_DIR/$plain") bytes)"
