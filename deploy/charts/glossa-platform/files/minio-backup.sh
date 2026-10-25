#!/bin/sh
# Nightly mirror of the release bucket (<fullname>-minio-backup, rclone
# image, busybox sh). rclone reads MinIO directly with glossa-edge's
# read-only credentials, so there is no scratch copy of the bucket and no
# root. What lands on the remote is encrypted: the mirror is written
# through rclone's crypt remote "vault" (standard file and directory names,
# standard content encryption) over REMOTE.
#
#   <REMOTE> (encrypted)/current/             the bucket (prefix) as of this run
#   <REMOTE> (encrypted)/deleted/<timestamp>/ what this run removed or overwrote there,
#                                             kept DELETED_RETENTION_DAYS days
#
# A plain sync would carry an accidental delete (or an empty bucket on a
# freshly lost volume) into the backup the next night; --backup-dir keeps
# those objects instead, and an empty source never replaces a non-empty
# mirror. After the sync, the mirror must hold the bucket's object count and
# bytes, or the Job fails.
#
# Environment (set by the chart):
#   RCLONE_CONFIG, RCLONE_CONFIG_MINIO_*  rclone.conf and the MinIO remote
#   RCLONE_CONFIG_VAULT_TYPE=crypt, RCLONE_CONFIG_VAULT_REMOTE=REMOTE
#   BUCKET, OBJECT_PREFIX                 the source (prefix may be empty)
#   REMOTE                                <remote>:<directory>, never a remote's root
#   DELETED_RETENTION_DAYS
# Mounted: /etc/minio-crypt/password and password2 (the crypt passwords,
# never printed: they are obscured below and exported for rclone).
set -eu

log() { printf 'minio-backup: %s\n' "$*"; }
fail() { printf 'minio-backup: ERROR: %s\n' "$*" >&2; exit 1; }
size() { rclone size --json "$1" 2>/dev/null; }
field() { sed -n "s/.*\"$1\":\([0-9]*\).*/\1/p"; }

case $DELETED_RETENTION_DAYS in '' | *[!0-9]* | 0) fail "DELETED_RETENTION_DAYS must be a positive integer" ;; esac

# The crypt passwords, in rclone's obscured form (the config format).
[ -s /etc/minio-crypt/password ] && [ -s /etc/minio-crypt/password2 ] || fail "the crypt passwords are missing"
pw=$(rclone obscure "$(cat /etc/minio-crypt/password)") || fail "cannot obscure the crypt password"
pw2=$(rclone obscure "$(cat /etc/minio-crypt/password2)") || fail "cannot obscure the crypt password2"
export RCLONE_CONFIG_VAULT_PASSWORD="$pw" RCLONE_CONFIG_VAULT_PASSWORD2="$pw2"
unset pw pw2

src="minio:$BUCKET${OBJECT_PREFIX:+/$OBJECT_PREFIX}"
dst="vault:current"
trash="vault:deleted"
ts=$(date -u +%Y%m%dT%H%M%SZ)

src_size=$(size "$src") || true
src_count=$(printf '%s' "$src_size" | field count)
src_bytes=$(printf '%s' "$src_size" | field bytes)
[ -n "$src_count" ] || fail "cannot list $src"
dst_size=$(size "$dst") || true
dst_count=$(printf '%s' "$dst_size" | field count)
if [ "$src_count" -eq 0 ] && [ "${dst_count:-0}" -gt 0 ]; then
  fail "$src is empty but the mirror holds $dst_count objects; refusing to empty it (restore MinIO from the mirror, or clear it by hand)"
fi

rclone sync "$src" "$dst" --backup-dir "$trash/$ts"

# The mirror must hold what the bucket holds: an object count and byte total
# (crypt reports plaintext sizes), so a silently short upload fails the Job.
mirror_size=$(size "$dst")
# An empty bucket leaves no mirror to size: 0 objects, which is what it holds.
mirror_count=$(printf '%s' "$mirror_size" | field count)
mirror_bytes=$(printf '%s' "$mirror_size" | field bytes)
mirror_count=${mirror_count:-0}
mirror_bytes=${mirror_bytes:-0}
[ "$mirror_count" = "$src_count" ] && [ "$mirror_bytes" = "$src_bytes" ] ||
  fail "the encrypted mirror holds $mirror_count objects, $mirror_bytes bytes; the bucket $src_count objects, $src_bytes bytes"
log "mirrored $src_count objects, $src_bytes bytes, from $src to the encrypted $REMOTE/current"

# Retention by the run's timestamp: objects moved to deleted/ keep their
# original modification time, so --min-age would drop them at once.
cutoff=$(date -u -d "@$(($(date +%s) - DELETED_RETENTION_DAYS * 86400))" +%Y%m%d%H%M%S)
rclone lsf --dirs-only --max-depth 1 "$trash/" 2>/dev/null | sed 's#/$##' |
  while IFS= read -r run; do
    case $run in
    [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z) ;;
    *) continue ;;
    esac
    if [ "$(printf '%s' "$run" | tr -d TZ)" -lt "$cutoff" ]; then
      rclone purge "$trash/$run"
      log "removed deleted/$run (older than ${DELETED_RETENTION_DAYS}d)"
    fi
  done
