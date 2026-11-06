# DEPLOY.md

> Gonema is **self-hosted software**: we host nothing. There is no Coolify application, no Cloudflare Worker, no domain or DNS record of ours. "Deploying" Gonema means **publishing a release** that users install on their own machines. No secret value here: names only.

## Delivery

| Artefact | Built by | Where |
|---|---|---|
| Archives: Linux, macOS, Windows (amd64, arm64) and Linux armv7, web interface embedded | GoReleaser (`.goreleaser.yaml`) | GitHub release of the tag (draft, published by hand) |
| `checksums.txt` (SHA-256), SBOM per archive (SPDX, needs `syft`) | GoReleaser | same release |
| Multi-arch image `ghcr.io/fs0ciety7000/gonema:<version>` and `:latest` (linux/amd64, linux/arm64, linux/arm/v7), `FROM scratch` | GoReleaser `dockers_v2` + buildx (`Dockerfile.goreleaser`) | GitHub Container Registry |
| Image built from source | `Dockerfile` (multi-stage: Node, Go, scratch) | `make docker` / `docker buildx build` |

## Release checklist

1. `make test`, `go test -race ./...`, `make lint`, `make build` pass; `TestColdStart` under 1 s.
2. `make openapi` committed if the API changed; the web client types regenerated.
3. New migrations have a `Down` and `docs/data-model.md` is up to date.
4. `make release-check` (validates `.goreleaser.yaml`).
5. Tag `vX.Y.Z`, then `goreleaser release --clean` (CI or a maintainer machine with Docker buildx and syft).
6. Check the draft release (archives, checksums, SBOMs) and the image manifest (`docker buildx imagetools inspect ghcr.io/fs0ciety7000/gonema:X.Y.Z`), then publish.
7. Smoke-test the image: `docker run --rm -p 7676:7676 ghcr.io/fs0ciety7000/gonema:X.Y.Z`, `docker inspect --format '{{.State.Health.Status}}'` reaches `healthy`.

## Release variables (names only)

| Name | When | Role |
|---|---|---|
| `GITHUB_TOKEN` | release | publish the GitHub release and push to ghcr.io (`packages: write`) |

They live in the CI secrets (or `kit secrets` on a maintainer machine), never in the repository.

## Runtime (on the user's machine)

- **Port**: 7676. **Health route**: `GET /health` (public); the image's `HEALTHCHECK` runs `/gonema healthcheck`.
- **Persistent volume**: `/data` in the image (`GONEMA_DATA_DIR`): database, master key `gonema.key`, `backups/`.
- **Configuration**: [`docs/configuration.md`](docs/configuration.md). Security-relevant variables: `GONEMA_AUTH` (`enabled` | `disabled_for_local`), `GONEMA_COOKIE_SECURE` (`auto` | `true` | `false`), `GONEMA_TRUSTED_ORIGINS`, `GONEMA_OUTBOUND_ALLOW_LOOPBACK`, `GONEMA_OUTBOUND_ALLOW_PRIVATE`, `GONEMA_WEBHOOK_ALLOW_PRIVATE`, `GONEMA_MASTER_KEY`.
- **User**: `user: "UID:GID"` in compose (scratch image: no PUID/PGID); see `docker-compose.example.yml`.
- **TLS and domain**: the user's reverse proxy (README: Caddy, nginx, Traefik). Behind HTTPS the session cookie needs `Secure` (`X-Forwarded-Proto` or `GONEMA_COOKIE_SECURE=true`); `disabled_for_local` never applies to requests carrying proxy headers.
- **Backups**: scheduled weekly, 4 kept, in `/data/backups`; the master key is not included by default. Restore test: [`docs/operations.md`](docs/operations.md#restore).

## Rollback

For users: restore the backup made before the upgrade with the previous version (downgrades of the schema are not supported). For us: mark the bad release as a pre-release, move `latest` back to the previous image (`docker buildx imagetools create -t ghcr.io/fs0ciety7000/gonema:latest ghcr.io/fs0ciety7000/gonema:<previous>`), publish a fixed version. Never delete a published tag.
