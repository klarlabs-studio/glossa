#!/bin/sh
# Runs before the image's envsubst step: the configuration is rendered into
# /tmp/nginx, so /tmp must be writable (an emptyDir in Kubernetes,
# `--tmpfs /tmp` with `docker run --read-only`). Fail loudly instead of
# letting nginx start without a server block.
set -eu
if ! mkdir -p /tmp/nginx 2>/dev/null || [ ! -w /tmp/nginx ]; then
  echo "glossa-studio: /tmp is not writable; mount an emptyDir or tmpfs at /tmp" >&2
  exit 1
fi
