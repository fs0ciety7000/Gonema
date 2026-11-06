# syntax=docker/dockerfile:1
#
# Gonema image: a static binary in an empty (scratch) image.
#
#   docker build -t gonema --build-arg VERSION=1.0.0 .
#   docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 -t gonema .
#
# Stages: the web interface (Node), the binary (Go, CGO off, cross-compiled
# for the target platform), then scratch with only the binary, the CA
# certificates and an empty /data owned by the runtime user. The time zone
# database is compiled into the binary (-tags timetzdata).
#
# The image runs as UID:GID 65532:65532 by default. Scratch has no shell and
# no user database, so PUID/PGID variables cannot work: choose the user with
# `user: "1000:1000"` in compose (or `docker run --user 1000:1000`) and make
# /data (and your media folders) writable by that user. See README.md.

ARG GO_VERSION=1.27.3
ARG NODE_VERSION=22

# ----------------------------------------------------------------- web
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ----------------------------------------------------------------- binary
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/web/dist/ internal/webui/dist/
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN GOARM="${TARGETVARIANT#v}" CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -tags timetzdata -ldflags "-s -w -X main.version=${VERSION}" -o /out/gonema ./cmd/gonema \
 && mkdir -p /out/data

# ----------------------------------------------------------------- runtime
FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="Gonema" \
      org.opencontainers.image.description="Self-hosted movie collection manager" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/fs0ciety7000/gonema" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gonema /gonema
COPY --from=build --chown=65532:65532 /out/data /data
ENV GONEMA_DATA_DIR=/data \
    GONEMA_BIND=0.0.0.0:7676
USER 65532:65532
VOLUME ["/data"]
EXPOSE 7676
# No shell nor curl in scratch: the binary checks itself (GET /health on the
# loopback, exit 0 when it answers 200).
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/gonema", "healthcheck"]
ENTRYPOINT ["/gonema"]
CMD ["serve"]
