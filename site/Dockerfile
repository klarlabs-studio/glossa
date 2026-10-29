# syntax=docker/dockerfile:1.10
#
# Glossa site: the static Astro landing page of glossa.klarlabs.de, served by
# an unprivileged nginx (uid 101) with a strict CSP and security headers.
# The ingress sends only "/" and /_astro/* here; Studio serves the rest.
#
# Build from the repository root (the site is a pnpm workspace member and
# shares Studio's design-system link script):
#
#   docker build -f site/Dockerfile -t glossa-site .
#
# The build fetches @klarlabs-studio/ui from its public repository at the
# commit pinned in studio/package.json#klarlabsUi, so it needs network
# access and git. There is no runtime configuration.
#
# Base images are pinned by digest, the same ones as studio/Dockerfile. Bump
# the tag and the digest together.

ARG NODE_IMAGE=node:22-alpine@sha256:b6f26b36c8ff49624cfdac716b8ea1138d606df02586a77d364bb5536a634f85
ARG NGINX_IMAGE=nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6

# ── Build ────────────────────────────────────────────────────────────
# The output is plain HTML and CSS, so build on the host's architecture.
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS build
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    PNPM_HOME=/pnpm \
    CI=true \
    ASTRO_TELEMETRY_DISABLED=1
RUN apk add --no-cache git && corepack enable
WORKDIR /repo

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY studio/package.json studio/
COPY site/package.json site/
RUN --mount=type=cache,id=glossa-pnpm-store,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && \
    pnpm install --frozen-lockfile --filter "@glossa/site..."

COPY studio/scripts/ studio/scripts/
COPY site/ site/
# `build` links the design system first (studio/scripts/klarlabs-ui.mjs), then
# type-checks and builds.
RUN --mount=type=cache,id=glossa-pnpm-store,target=/pnpm/store \
    pnpm --filter @glossa/site build

# ── Runtime ──────────────────────────────────────────────────────────
FROM ${NGINX_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=""
LABEL org.opencontainers.image.title="glossa-site" \
      org.opencontainers.image.description="Glossa's landing page, served at the root of glossa.klarlabs.de." \
      org.opencontainers.image.source="https://go.klarlabs.de/glossa" \
      org.opencontainers.image.url="https://go.klarlabs.de/glossa/tree/main/site" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Klarlabs" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

# Served from its own directory, so the base image's default page and
# server block (never loaded: nginx.conf includes only /tmp/nginx/conf.d)
# need no root-owned cleanup.
USER 101

# Rendered by the image's /docker-entrypoint.d/20-envsubst-on-templates.sh
# into /tmp/nginx on every start. The templates use no variables; the filter
# keeps nginx's own $variables untouched.
ENV NGINX_ENVSUBST_TEMPLATE_DIR=/etc/nginx/templates \
    NGINX_ENVSUBST_OUTPUT_DIR=/tmp/nginx \
    NGINX_ENVSUBST_FILTER=^GLOSSA_SITE_

COPY site/docker/nginx.conf /etc/nginx/nginx.conf
COPY site/docker/templates/ /etc/nginx/templates/
COPY --chmod=0755 site/docker/05-glossa-runtime-dir.sh /docker-entrypoint.d/
COPY --from=build /repo/site/dist/ /usr/share/glossa-site/

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
