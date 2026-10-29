#!/usr/bin/env bash
# Provisions the in-namespace MinIO for glossa-platform. Runs as the
# post-install/post-upgrade hook Job <fullname>-minio-bootstrap in the
# cgr.dev/chainguard/minio-client image (`mc`). Idempotent: every step
# converges on the state below whether it ran before or not, so upgrades
# re-run it safely and a rotated secret key in a user Secret is picked up
# by the next upgrade.
#
#   bucket BUCKET
#   policy POLICY_RW: read-write on BUCKET[/PREFIX]/*  → user RW_ACCESS_KEY (glossa-server)
#   policy POLICY_RO: read-only  on BUCKET[/PREFIX]/*  → user RO_ACCESS_KEY (glossa-edge)
#
# Environment (set by the chart):
#   MINIO_URL                         http(s)://<fullname>-minio:9000
#   MINIO_ROOT_USER / MINIO_ROOT_PASSWORD
#   BUCKET, OBJECT_PREFIX             prefix without leading/trailing "/" (may be empty)
#   POLICY_RW, POLICY_RO              policy names
#   RW_ACCESS_KEY / RW_SECRET_KEY     glossa-server's credentials
#   RO_ACCESS_KEY / RO_SECRET_KEY     glossa-edge's credentials
#   WAIT_SECONDS                      how long to wait for MinIO to come up
#   MC_CONFIG_DIR                     writable mc config dir (an emptyDir)
set -euo pipefail

log() { printf 'minio-bootstrap: %s\n' "$*"; }
fail() { printf 'minio-bootstrap: ERROR: %s\n' "$*" >&2; exit 1; }
# mc quotes a rejected secret key in its errors: never log one.
redact() {
  local s=$1 k
  for k in "${MINIO_ROOT_PASSWORD:-}" "${RW_SECRET_KEY:-}" "${RO_SECRET_KEY:-}"; do
    [[ -z $k ]] || s=${s//"$k"/<redacted>}
  done
  printf '%s' "$s"
}
# mcq ARGS…: run mc quietly; on failure, fail with its redacted output.
mcq() {
  local out
  out=$(mc "$@" 2>&1) || fail "mc $1 $2: $(redact "$out")"
}

[[ -n ${RW_ACCESS_KEY:-} && -n ${RW_SECRET_KEY:-} ]] || fail "glossa-server's access key or secret key is empty"
[[ -n ${RO_ACCESS_KEY:-} && -n ${RO_SECRET_KEY:-} ]] || fail "glossa-edge's access key or secret key is empty"
[[ $RW_ACCESS_KEY != "$RO_ACCESS_KEY" ]] ||
  fail "glossa-server and glossa-edge use the same access key; the edge needs its own read-only user"
[[ $RW_ACCESS_KEY != "$MINIO_ROOT_USER" && $RO_ACCESS_KEY != "$MINIO_ROOT_USER" ]] ||
  fail "an application access key equals the MinIO root user; give each its own"

# 1. Wait for MinIO (a fresh StatefulSet may still be pulling or attaching
#    its Longhorn volume when the hook starts).
deadline=$((SECONDS + ${WAIT_SECONDS:-300}))
until err=$(mc alias set glossa "$MINIO_URL" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" --api S3v4 --path on 2>&1 >/dev/null &&
  mc admin info glossa 2>&1 >/dev/null); do
  ((SECONDS < deadline)) || fail "MinIO at $MINIO_URL is not ready after ${WAIT_SECONDS:-300}s: $(redact "$err")"
  log "waiting for MinIO at $MINIO_URL"
  sleep 5
done

# 2. Bucket.
mcq mb --ignore-existing "glossa/$BUCKET"
log "bucket $BUCKET present"

# 3. Policies. ListBucket and GetBucketLocation sit on the bucket, not on
#    the prefix: without ListBucket a GET of a missing key answers 403
#    instead of 404, which glossa counts as a storage failure (and a
#    prefix condition would not apply to that check).
objects="arn:aws:s3:::${BUCKET}/${OBJECT_PREFIX:+$OBJECT_PREFIX/}*"
policy() {
  printf '{"Version":"2012-10-17","Statement":[%s,%s]}\n' \
    "{\"Effect\":\"Allow\",\"Action\":[\"s3:GetBucketLocation\",\"s3:ListBucket\"],\"Resource\":[\"arn:aws:s3:::${BUCKET}\"]}" \
    "{\"Effect\":\"Allow\",\"Action\":$1,\"Resource\":[\"$objects\"]}"
}
policy '["s3:GetObject","s3:PutObject","s3:DeleteObject","s3:AbortMultipartUpload","s3:ListMultipartUploadParts"]' >"$MC_CONFIG_DIR/rw.json"
policy '["s3:GetObject"]' >"$MC_CONFIG_DIR/ro.json"
mcq admin policy create glossa "$POLICY_RW" "$MC_CONFIG_DIR/rw.json"
mcq admin policy create glossa "$POLICY_RO" "$MC_CONFIG_DIR/ro.json"
log "policies $POLICY_RW (read-write) and $POLICY_RO (read-only) on $objects"

# 4. Users. `user add` on an existing user updates its secret key.
attach() { # policy user
  local out
  if ! out=$(mc admin policy attach glossa "$1" --user "$2" 2>&1); then
    [[ $out == *"already"* ]] || fail "attach $1 to $2: $(redact "$out")"
  fi
}
mcq admin user add glossa "$RW_ACCESS_KEY" "$RW_SECRET_KEY"
attach "$POLICY_RW" "$RW_ACCESS_KEY"
mcq admin user add glossa "$RO_ACCESS_KEY" "$RO_SECRET_KEY"
attach "$POLICY_RO" "$RO_ACCESS_KEY"
log "users for glossa-server ($POLICY_RW) and glossa-edge ($POLICY_RO) present"
