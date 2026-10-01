# syntax=docker/dockerfile:1
# =============================================================================
# Bibli — production image.
# A static Go binary (modernc.org/sqlite is pure Go: CGO off, no system library
# to install). The final image stays tiny.
# =============================================================================

# ---- Build stage ----
FROM golang:1.27-alpine3.24 AS build
WORKDIR /src

# Dependencies first: this layer stays cached as long as go.mod/go.sum do not move.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Build metadata lives in app/build-info.json (written by CI before the build,
# embedded with go:embed). Static binary, symbol table stripped.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/bibli ./app

# ---- Final image ----
FROM alpine:3.24

# Static OCI labels; the release workflow adds version, revision, created and source.
LABEL org.opencontainers.image.title="Bibli" \
      org.opencontainers.image.authors="Martin Erpicum" \
      org.opencontainers.image.description="Library management for a primary school, run by volunteers" \
      org.opencontainers.image.licenses="AGPL-3.0"

# ca-certificates: HTTPS calls to the catalogues (BnF, UniCat, Google, OL).
# sqlite: the CLI, for a manual ".backup" or a look at the data; the app
#   itself backs up with VACUUM INTO. Never a cp of the live file.
# tzdata: log timestamps in local time.
RUN apk add --no-cache ca-certificates sqlite tzdata \
    && adduser -D -u 1000 bibli \
    && mkdir -p /data \
    && chown bibli:bibli /data

COPY --from=build /out/bibli /usr/local/bin/bibli

USER bibli
WORKDIR /data
ENV TZ=Europe/Brussels

# The database lives in /data, bind-mounted from the host (never NFS/CIFS,
# locking is broken there).
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["bibli"]
CMD ["-db", "/data/biblio.db", "-addr", ":8080"]
