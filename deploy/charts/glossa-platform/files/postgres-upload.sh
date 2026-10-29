#!/bin/sh
# The second half of the nightly Postgres backup (the container "upload"
# of <fullname>-postgres-backup, rclone image, busybox sh): ships the dump
# the "dump" init container verified to the remote, checks it arrived
# whole, then applies retention to this release's own dump files only.
#
# Environment (set by the chart):
#   RCLONE_CONFIG    rclone.conf (from backup.rclone.configSecretName)
#   IN_DIR           the scratch volume with dump.name and the dump (encrypted: .sql.gz.age)
#   REMOTE           <remote>:<directory>, never a remote's root
#   PREFIX           dump file prefix
#   RETENTION_DAYS   dumps older than this are deleted
set -eu

log() { printf 'postgres-upload: %s\n' "$*"; }
fail() { printf 'postgres-upload: ERROR: %s\n' "$*" >&2; exit 1; }

name=$(cat "$IN_DIR/dump.name") || fail "no dump.name: the dump container did not finish"
[ -f "$IN_DIR/$name" ] || fail "the dump $name is missing"
case $RETENTION_DAYS in '' | *[!0-9]* | 0) fail "RETENTION_DAYS must be a positive integer, got '$RETENTION_DAYS'" ;; esac

rclone copy --no-traverse "$IN_DIR/$name" "$REMOTE/"
local_size=$(stat -c %s "$IN_DIR/$name")
remote_size=$(rclone lsf --format s --files-only "$REMOTE/$name")
[ "$remote_size" = "$local_size" ] ||
  fail "$REMOTE/$name has ${remote_size:-no} bytes, the dump $local_size"
log "uploaded $REMOTE/$name ($local_size bytes)"

# Retention runs only after a verified upload, so the newest dump always
# survives it. Anchored to this directory (--max-depth 1) and to this
# release's file names, the encrypted dumps (.sql.gz.age) and the plaintext
# ones from before encryption alike: the remote may be shared (v0.3's first
# version deleted every tenant's dumps with an unscoped `rclone delete`).
rclone delete --max-depth 1 --min-age "${RETENTION_DAYS}d" --include "/$PREFIX-*.sql.gz*" "$REMOTE/"
kept=$(rclone lsf --max-depth 1 --files-only --include "/$PREFIX-*.sql.gz*" "$REMOTE/" | wc -l)
log "retention ${RETENTION_DAYS}d: $kept dumps kept"
