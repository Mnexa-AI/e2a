# syntax=docker/dockerfile:1.6
#
# Two-stage build: Next.js produces a static export, Caddy serves it
# with a parameterizable reverse-proxy to the e2a API backend.
#
# Build context is the `web/` directory:
#   docker build -t e2a-web -f web/Dockerfile web
#
# Customizing branding/SEO for a non-default deployment:
#   docker build \
#     --build-arg NEXT_PUBLIC_SITE_URL=https://e2a.example.com \
#     --build-arg NEXT_PUBLIC_AGENTS_DOMAIN=agents.example.com \
#     -t e2a-web -f web/Dockerfile web
#
# Next.js inlines NEXT_PUBLIC_* values at build time (a static-export
# constraint), so these cannot be overridden at container start. To
# rebrand a published image, rebuild from source with --build-arg.

# ── Builder ─────────────────────────────────────────────────────────
FROM node:22-alpine AS builder
WORKDIR /app

# Install deps first so the layer caches when only source changes.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --prefer-offline

# Empty defaults make the published OSS image generic. The hosted
# deployment overrides these at build time. See web/.env.example for
# the full list and what each one controls.
ARG NEXT_PUBLIC_SITE_URL=
ARG NEXT_PUBLIC_SITE_NAME=
ARG NEXT_PUBLIC_AGENTS_DOMAIN=
ARG NEXT_PUBLIC_FEEDBACK_EMAIL=
ARG NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION=

ENV NEXT_PUBLIC_SITE_URL=$NEXT_PUBLIC_SITE_URL \
    NEXT_PUBLIC_SITE_NAME=$NEXT_PUBLIC_SITE_NAME \
    NEXT_PUBLIC_AGENTS_DOMAIN=$NEXT_PUBLIC_AGENTS_DOMAIN \
    NEXT_PUBLIC_FEEDBACK_EMAIL=$NEXT_PUBLIC_FEEDBACK_EMAIL \
    NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION=$NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION

COPY . ./
RUN npm run build

# ── Runtime ─────────────────────────────────────────────────────────
# Caddy 2 serves the static export and reverse-proxies /api/* to the
# e2a Go backend. The Caddyfile uses {$VAR:default} substitution so
# operators can rewire the proxy target without editing the file:
#
#   docker run \
#     -e E2A_BACKEND=http://e2a-server:8080 \
#     -e WEB_PORT=8081 \
#     -p 8081:8081 ghcr.io/mnexa-ai/e2a-web
FROM caddy:2-alpine
# wget is provided by alpine's busybox applet, but caddy:2-alpine doesn't
# guarantee it stays — Caddy's official Dockerfile doesn't `apk add` it.
# Install explicitly so the HEALTHCHECK below can't silently break if a
# future base image trims busybox.
RUN apk add --no-cache wget
COPY --from=builder /app/out /srv
COPY Caddyfile /etc/caddy/Caddyfile

# Default port; override with WEB_PORT env. Healthcheck reads the same
# var so it works regardless of the chosen port.
ENV WEB_PORT=80
EXPOSE 80
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O - "http://127.0.0.1:${WEB_PORT}/" >/dev/null || exit 1
