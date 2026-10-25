#!/bin/sh
# The age release for the backup Jobs (the init container "fetch-age",
# alpine image, busybox sh): downloads the pinned release, checks its
# SHA-256 and unpacks age and age-keygen into /tools. The dump encrypts
# with them and the restore drill decrypts with them.
#
# Environment (set by the chart):
#   AGE_VERSION, AGE_SHA256   the release and the SHA-256 of its tarball
set -eu

fail() { printf 'fetch-age: ERROR: %s\n' "$*" >&2; exit 1; }

t="age-$AGE_VERSION-linux-amd64.tar.gz"
wget -q -O "/tmp/$t" "https://github.com/FiloSottile/age/releases/download/$AGE_VERSION/$t" ||
  fail "cannot download $t"
echo "$AGE_SHA256  /tmp/$t" | sha256sum -c -s || fail "$t does not match the pinned SHA-256"
tar -xzf "/tmp/$t" -C /tools --strip-components=1 age/age age/age-keygen
rm -f "/tmp/$t"
/tools/age --version >/dev/null || fail "age does not run"
