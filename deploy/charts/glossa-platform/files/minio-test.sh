#!/usr/bin/env bash
# `helm test` for the in-namespace MinIO (the Pod <fullname>-minio-test,
# cgr.dev/chainguard/minio-client image). Checks what the bootstrap Job
# promised:
#   - glossa-edge's credentials reach the bucket and can read objects,
#   - they cannot write or delete,
#   - glossa-server's credentials can write and delete.
# It leaves nothing behind: its probe object is deleted on every exit.
#
# Environment: MINIO_URL, BUCKET, OBJECT_PREFIX (no leading/trailing "/"),
# RW_ACCESS_KEY / RW_SECRET_KEY, RO_ACCESS_KEY / RO_SECRET_KEY, MC_CONFIG_DIR.
set -euo pipefail

pass() { printf 'ok   %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*" >&2; exit 1; }

# mc quotes a rejected secret key in its error, so those errors are not shown.
mc alias set server "$MINIO_URL" "$RW_ACCESS_KEY" "$RW_SECRET_KEY" --api S3v4 --path on >/dev/null 2>&1 ||
  fail "glossa-server's credentials are rejected by mc (secret key shorter than 8 characters?)"
mc alias set edge "$MINIO_URL" "$RO_ACCESS_KEY" "$RO_SECRET_KEY" --api S3v4 --path on >/dev/null 2>&1 ||
  fail "glossa-edge's credentials are rejected by mc (secret key shorter than 8 characters?)"

# A new pod's first connections can be refused for a moment while its
# network policy is programmed (k3s): wait for MinIO before judging it.
for _ in $(seq 1 30); do mc ls "edge/$BUCKET/" >/dev/null 2>&1 && break; sleep 1; done

dir="$BUCKET/${OBJECT_PREFIX:+$OBJECT_PREFIX/}.glossa-helm-test"
probe="$dir/${HOSTNAME:-pod}-$RANDOM"
body="glossa helm test $RANDOM"
cleanup() { mc rm --force "server/$probe" "server/$probe.edge" >/dev/null 2>&1 || true; }
trap cleanup EXIT

mc ls "edge/$BUCKET/${OBJECT_PREFIX:+$OBJECT_PREFIX/}" >/dev/null ||
  fail "glossa-edge's credentials cannot list bucket $BUCKET"
pass "glossa-edge's credentials reach bucket $BUCKET"

# Without s3:ListBucket a missing key answers 403, which glossa-edge would
# count as a storage outage rather than a 404.
if out=$(mc stat "edge/$probe.missing" 2>&1); then
  fail "a key that was never written exists: $probe.missing"
fi
[[ $out != *"Access Denied"* ]] ||
  fail "a missing key answers Access Denied instead of Not Found for glossa-edge (s3:ListBucket missing)"
pass "a missing key answers Not Found"

printf '%s' "$body" | mc pipe "server/$probe" >/dev/null ||
  fail "glossa-server's credentials cannot write $probe"
pass "glossa-server's credentials write"

got=$(mc cat "edge/$probe") || fail "glossa-edge's credentials cannot read $probe"
[[ $got == "$body" ]] || fail "glossa-edge read back something else from $probe"
pass "glossa-edge's credentials read"

if printf 'x' | mc pipe "edge/$probe.edge" >/dev/null 2>&1; then
  fail "glossa-edge's credentials can WRITE to $BUCKET; they must be read-only"
fi
pass "glossa-edge's credentials cannot write"

if mc rm "edge/$probe" >/dev/null 2>&1; then
  fail "glossa-edge's credentials can DELETE in $BUCKET; they must be read-only"
fi
mc stat "server/$probe" >/dev/null 2>&1 || fail "the probe object vanished after a refused delete"
pass "glossa-edge's credentials cannot delete"

mc rm "server/$probe" >/dev/null || fail "glossa-server's credentials cannot delete $probe"
pass "glossa-server's credentials delete"
