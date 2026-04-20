# fernsicht CLI — minimal container image.
#
# Two-stage build:
#   1. golang:1.26-alpine — produces a stripped, CGO-free binary.
#   2. alpine:3.20 — minimal runtime with /bin/sh + ca-certificates.
#
# We pick alpine over distroless/scratch because the wrap subcommand
# needs to spawn subprocesses; users typically `apk add python` etc.
# in derived images so their wrapped commands have an interpreter.
# distroless/static would require building everything we wrap into a
# bespoke image — defeats the "wrap any command" pitch.
#
# Image is built multi-arch (linux/amd64, linux/arm64) by the
# .github/workflows/cli-docker.yml workflow on every CLI release.
#
# Build context: the repository root (so we can COPY both cli/ and
# bridge/ — they're separate Go modules sharing a workspace).

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG SOURCE_DATE_EPOCH

# Reproducibility: respecting SOURCE_DATE_EPOCH lets us match the
# binary checksum produced by `make dist` outside of Docker.
ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH}

WORKDIR /src

# Copy the bridge sibling module first so the cli's local replace
# directive resolves.
COPY bridge/ ./bridge/
COPY cli/    ./cli/

WORKDIR /src/cli

# --buildvcs=false avoids embedding git-state info that varies per
# build context. -trimpath strips local paths. -buildid= clears the
# random build ID. -s -w drops symbol table + debug.
RUN go build \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w -buildid= \
                -X main.version=${VERSION} \
                -X main.commit=${COMMIT} \
                -X main.buildDate=${BUILD_DATE}" \
      -o /out/fernsicht \
      ./cmd/fernsicht


# ---------- runtime ----------

FROM alpine:3.20

# ca-certificates so HTTPS to signal.fernsicht.space + GitHub release
# downloads work out-of-the-box. tini for clean signal forwarding to
# wrapped subprocesses (Ctrl-C in `docker run` reaches the wrapped
# command's process group correctly).
RUN apk add --no-cache ca-certificates tini

COPY --from=builder /out/fernsicht /usr/local/bin/fernsicht

# tini handles PID 1 responsibilities (signal forwarding, zombie
# reaping); fernsicht itself is the actual entrypoint.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/fernsicht"]

# OCI labels for the GitHub Container Registry.
LABEL org.opencontainers.image.title="fernsicht" \
      org.opencontainers.image.description="Watch any command's progress from anywhere" \
      org.opencontainers.image.source="https://github.com/MuteJester/Fernsicht" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.documentation="https://github.com/MuteJester/Fernsicht/blob/main/cli/README.md"
