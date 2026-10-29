# syntax=docker/dockerfile:1.10
#
# Glossa Studio: the Vue SPA, built with Vite and served as static files by
# an unprivileged nginx (uid 101) with SPA fallback, a strict CSP and
# security headers.
#
# Build from the repository root (Studio is a pnpm workspace member and
# imports @klarlabs-studio/glossa/messageformat, built from messageformat/js and the other
# source packages of runtimes/js that the umbrella package assembles):
#
#   docker build -f studio/Dockerfile -t glossa-studio .
#
# The build fetches @klarlabs-studio/ui from its public repository at the
# commit pinned in studio/package.json#klarlabsUi (studio/README.md,
# "Design system"), so it needs network access and git. It also builds
# @klarlabs-studio/glossa (its overlay bundle,
# @klarlabs-studio/glossa/overlay/bundle, is what Studio serves at
# /overlay/v1/overlay.js, RFC 0004 §5.1).
#
# Runtime configuration (no rebuild): GLOSSA_STUDIO_* variables are
# rendered into /config.json and the CSP at container start; see
# studio/docker/templates. The API is expected on the same origin under
# /v1, routed to glossa-server by the ingress.
#
# Base images are pinned by digest. Bump the tag and the digest together
# (`docker buildx imagetools inspect <image>:<tag>` prints the index digest).

ARG NODE_IMAGE=node:22-alpine@sha256:b6f26b36c8ff49624cfdac716b8ea1138d606df02586a77d364bb5536a634f85
ARG NGINX_IMAGE=nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6

# ── Build ────────────────────────────────────────────────────────────
# The output is plain JS/CSS, so build on the host's architecture.
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS build
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    PNPM_HOME=/pnpm \
    CI=true
RUN apk add --no-cache git && corepack enable
WORKDIR /repo

# Manifests first, so the dependency install caches across source edits.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY messageformat/js/package.json messageformat/js/
COPY runtimes/js/runtime/package.json runtimes/js/runtime/
COPY runtimes/js/elements/package.json runtimes/js/elements/
COPY runtimes/js/vue/package.json runtimes/js/vue/
COPY runtimes/js/react/package.json runtimes/js/react/
COPY runtimes/js/astro/package.json runtimes/js/astro/
COPY runtimes/js/unplugin/package.json runtimes/js/unplugin/
COPY runtimes/js/capture/package.json runtimes/js/capture/
COPY runtimes/js/overlay/package.json runtimes/js/overlay/
COPY runtimes/js/glossa/package.json runtimes/js/glossa/
COPY studio/package.json studio/
RUN --mount=type=cache,id=glossa-pnpm-store,target=/pnpm/store \
    pnpm config set store-dir /pnpm/store && \
    pnpm install --frozen-lockfile --filter "@glossa/studio..."

COPY messageformat/js/ messageformat/js/
COPY runtimes/js/runtime/ runtimes/js/runtime/
COPY runtimes/js/elements/ runtimes/js/elements/
COPY runtimes/js/vue/ runtimes/js/vue/
COPY runtimes/js/react/ runtimes/js/react/
COPY runtimes/js/astro/ runtimes/js/astro/
COPY runtimes/js/unplugin/ runtimes/js/unplugin/
COPY runtimes/js/capture/ runtimes/js/capture/
COPY runtimes/js/overlay/ runtimes/js/overlay/
COPY runtimes/js/glossa/ runtimes/js/glossa/
COPY studio/ studio/
# The umbrella assembles dist/ from its nine source packages, which build first.
RUN pnpm --filter "@klarlabs-studio/glossa..." build
# `build` links the design system first (scripts/klarlabs-ui.mjs), then runs
# vite build. The Vite manifest is tooling, not something to serve.
RUN --mount=type=cache,id=glossa-pnpm-store,target=/pnpm/store \
    pnpm --filter @glossa/studio build && \
    rm -rf studio/dist/.vite

# ── Runtime ──────────────────────────────────────────────────────────
FROM ${NGINX_IMAGE}
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=""
LABEL org.opencontainers.image.title="glossa-studio" \
      org.opencontainers.image.description="Glossa Studio: the web app for translators, developers and localization managers." \
      org.opencontainers.image.source="https://go.klarlabs.de/glossa" \
      org.opencontainers.image.url="https://go.klarlabs.de/glossa/tree/main/studio" \
      org.opencontainers.image.documentation="https://go.klarlabs.de/glossa/blob/main/studio/README.md" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Klarlabs" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

# The stock server block (which the entrypoint's IPv6 script would rewrite)
# and welcome pages go; ours come from templates and the build.
USER root
RUN rm -f /etc/nginx/conf.d/default.conf /usr/share/nginx/html/*
USER 101

# Rendered by the image's /docker-entrypoint.d/20-envsubst-on-templates.sh
# into /tmp/nginx on every start. Only GLOSSA_STUDIO_* variables are
# substituted; each needs a value here so an unset one renders empty.
ENV NGINX_ENVSUBST_TEMPLATE_DIR=/etc/nginx/templates \
    NGINX_ENVSUBST_OUTPUT_DIR=/tmp/nginx \
    NGINX_ENVSUBST_FILTER=^GLOSSA_STUDIO_ \
    GLOSSA_STUDIO_API_BASE_URL="" \
    GLOSSA_STUDIO_EDGE_URL="" \
    GLOSSA_STUDIO_ENVIRONMENT="production" \
    GLOSSA_STUDIO_CSP_CONNECT_SRC=""

COPY studio/docker/nginx.conf /etc/nginx/nginx.conf
COPY studio/docker/templates/ /etc/nginx/templates/
COPY --chmod=0755 studio/docker/05-glossa-runtime-dir.sh /docker-entrypoint.d/
COPY --from=build /repo/studio/dist/ /usr/share/nginx/html/

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
