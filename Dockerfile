# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# Task 358 — Docker is an ADDITIONAL delivery method. The systemd path
# (scripts/install.sh + scripts/systemd/orenda.service) stays canonical.
#
# Multi-stage build mirroring `make build`:
#   1. node:24-alpine  — npm ci + vite build → web/dist
#   2. golang:1.26-alpine — copy web/dist into internal/embed/web/dist
#      (keeping .gitkeep so `//go:embed all:dist` compiles), then
#      CGO_ENABLED=0 go build (SQLite is pure-Go via modernc.org/sqlite).
#   3. alpine runtime — git (orenda backup push), ca-certificates, tzdata,
#      non-root user, writable /app/data for the SQLite database.
# ---------------------------------------------------------------------------

# ---- Stage 1: build the SPA -------------------------------------------------
# web/.npmrc sets engine-strict=true with "node": ">=24.11.0" — Node 22 fails.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Stage 2: build the Go binary with the SPA embedded ---------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
# Same copy the Makefile's `embed-dists` target does: web/dist/* into the
# embed drop. Docker's COPY merges into the existing directory, so the
# .gitkeep copied with internal/ above survives and the embed compiles.
COPY --from=web /src/web/dist/ ./internal/embed/web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /orenda ./cmd/orenda

# ---- Stage 3: runtime --------------------------------------------------------
FROM alpine:3.22
# git: `orenda backup push` shells out to git (needs a remote configured
# inside the app); ca-certificates: HTTPS (TLS logins, webhooks, git over https);
# tzdata: timezone-aware calendar/cron handling.
RUN apk add --no-cache git ca-certificates tzdata \
    && addgroup -S orenda \
    && adduser -S -G orenda -u 10001 orenda \
    && mkdir -p /app/data \
    && chown orenda:orenda /app/data \
    && ln -s /app/orenda /usr/local/bin/orenda
WORKDIR /app
COPY --from=build /orenda /app/orenda
# Named volumes inherit the ownership of the mount point from the image on
# first use — /app/data must belong to `orenda` or SQLite cannot write.
USER orenda
EXPOSE 2137
ENTRYPOINT ["/app/orenda", "serve"]
