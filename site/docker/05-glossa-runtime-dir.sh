#!/bin/sh
# nginx renders its server block into /tmp/nginx at start, so /tmp must be
# writable (an emptyDir in Kubernetes, `--tmpfs /tmp` with a read-only
# root filesystem). Fail loudly instead of starting without a server block.
set -eu
if ! mkdir -p /tmp/nginx 2>/dev/null || [ ! -w /tmp/nginx ]; then
  echo "glossa-site: /tmp is not writable; mount an emptyDir or tmpfs at /tmp" >&2
  exit 1
fi
