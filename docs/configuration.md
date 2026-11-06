# Configuration

Gonema is configured at start-up with command-line flags or `GONEMA_*` environment variables. A flag wins over its variable. Everything else (indexers, download clients, quality profiles, naming, import mode, health thresholds, scheduled backups) is set in the web interface and stored in the database.

Run `gonema serve -h` for the list printed by the binary you have.

## Server

| Flag | Variable | Default | Description |
|---|---|---|---|
| `-data-dir` | `GONEMA_DATA_DIR` | `./data` (`/config` in a container; the official image sets `/data`) | Data directory: database `gonema.db`, master key `gonema.key`, `backups/`, `restore/`, image `cache/`, optional `logs/`. |
| `-bind` | `GONEMA_BIND` | `127.0.0.1:7676` (`0.0.0.0:7676` in a container) | Listen address `host:port`. Port 7676 avoids the ports of other common media apps. |
| `-base-url` | `GONEMA_BASE_URL` | empty | URL path prefix when a reverse proxy serves Gonema under a sub-path, e.g. `/gonema`. `/health` stays at the root. |
| `-workers` | `GONEMA_WORKERS` | `4` | Background job workers, 1 to 32. |
| | `GONEMA_MASTER_KEY` | unset | Base64 master key. When set it replaces the key file `gonema.key` (useful with a secret manager). Never logged. |
| | `TZ` | system | Time zone of logs and schedules. The time zone database is built into release binaries and the image. |

## Logs

| Flag | Variable | Default | Description |
|---|---|---|---|
| `-log-level` | `GONEMA_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Can be changed while running with `PUT /api/v1/settings/logs`; the change is not saved, this value applies again at the next start. |
| `-log-format` | `GONEMA_LOG_FORMAT` | `text` | `text` or `json` (stderr and the log file). |
| `-log-file` | `GONEMA_LOG_FILE` | empty (no file) | Log file written besides stderr. A relative path is inside the data directory (`logs/gonema.log`). |
| `-log-max-size` | `GONEMA_LOG_MAX_SIZE_MB` | `10` | Size in MB that rotates the file (1 to 1024): `gonema.log` becomes `gonema.log.1`, and so on. |
| `-log-keep` | `GONEMA_LOG_KEEP` | `5` | Rotated files kept (0 to 100); the oldest is removed. |
| `-log-buffer` | `GONEMA_LOG_BUFFER` | `2000` | Entries kept in memory for the Logs screen (100 to 100000). |

Every log output (stderr, file, Logs screen, downloads) is redacted: API keys and tokens in URLs (`apikey=`, `token=`, `passkey=`...), `Authorization`, `Cookie` and `X-Api-Key` headers, `Bearer` tokens, passwords in JSON or in URLs (`http://user:password@host`), and attributes named like a secret (`password`, `api_key`, `*_token`...) become `[redacted]`.

## Security

Details and the threat model: [`docs/security.md`](security.md) (in French).

| Flag | Variable | Default | Description |
|---|---|---|---|
| `-auth` | `GONEMA_AUTH` | `enabled` | `enabled`: every `/api` request needs the session cookie (web interface) or the `X-Api-Key` header (scripts). `disabled_for_local`: requests that come **directly** from a loopback or private address skip the login, if their `Host` header is an IP address, `localhost` or an allowed host (DNS rebinding). A request carrying a proxy header (`X-Forwarded-For`, `X-Forwarded-Proto`, `X-Real-IP`, `Forwarded`, `X-Forwarded-Host`, `CF-Connecting-IP`, `True-Client-IP`), or coming from a trusted proxy, is never treated as local. **Unsafe with Docker or Podman port publishing** (userland `docker-proxy`, rootless networking): every client may then appear to come from the bridge gateway, a private address. Gonema logs a warning when it runs in a container with this mode. Use it only on a trusted LAN, with host networking or a bare binary. |
| `-cookie-secure` | `GONEMA_COOKIE_SECURE` | `auto` | `Secure` attribute of the session cookie. `auto`: set when the request came over HTTPS or a **trusted proxy** sends `X-Forwarded-Proto: https`. `true` / `false` force it. Over HTTPS without a base URL the cookie is named `__Host-gonema_session` (the old `gonema_session` is still read, and cleared at the next sign-in). With `auto`, a warning is logged when `X-Forwarded-Proto` arrives from a peer that is not a trusted proxy, or when the trusted origins are `https` but sign-ins arrive over plain HTTP. |
| `-trusted-origins` | `GONEMA_TRUSTED_ORIGINS` | empty | Comma-separated origins (`https://media.example.org`) accepted by the cross-site request check besides the request's own host. Needed when a reverse proxy rewrites the `Host` header. Their host names are also accepted as `Host`. |
| `-allowed-hosts` | `GONEMA_ALLOWED_HOSTS` | empty | Comma-separated host names (`media.example.org,gonema.lan`, no scheme or port) the server answers to besides IP addresses and `localhost`. Always used for `disabled_for_local` and the account creation; when set, every `/api` request without an API key must use one of them (else `403`). |
| `-trusted-proxies` | `GONEMA_TRUSTED_PROXIES` | empty | Comma-separated addresses or CIDR ranges (`127.0.0.1,172.18.0.0/16`) of the reverse proxies. Only their `X-Forwarded-For` (right-most address that is not a trusted proxy) and `X-Forwarded-Proto` are read. Empty: the connection peer is the client. |
| `-outbound-allow-loopback` | `GONEMA_OUTBOUND_ALLOW_LOOPBACK` | `true` | Integrations (indexers, download clients, media servers, notifications) may connect to `127.0.0.0/8` and `::1`. |
| `-outbound-allow-private` | `GONEMA_OUTBOUND_ALLOW_PRIVATE` | `true` | Integrations may connect to private ranges (RFC 1918, `fc00::/7`, `100.64.0.0/10`). |
| `-webhook-allow-private` | `GONEMA_WEBHOOK_ALLOW_PRIVATE` | `false` | Webhooks (generic, Discord, Slack) may connect to loopback and private addresses. Off by default: webhooks reach public addresses only. |

Cloud metadata addresses, link-local, multicast and reserved ranges are always refused, whatever these options say. TMDB and its images use public addresses only. The download links an indexer returns (and their redirects) may reach public addresses, and the indexer's own address when it is on the LAN: never another LAN host.

**First run.** While no account exists, Gonema logs a one-time setup token (`setup=XXXX-XXXX-XXXX-XXXX-XXXX-XXXX` on the line "no account exists yet"). Creating the account from another machine requires it; from the server itself (loopback connection with `Host` `localhost`, `127.0.0.1` or `::1`) it is optional. With Docker: `docker logs gonema`.

## Settings stored in the database

Changed while running, in the web interface or through the API (`/api/v1/openapi.json` describes every endpoint), not at start-up. The ones operations care about:

| Setting | Where | Default |
|---|---|---|
| Scheduled backups: on/off, interval, number kept, include the master key | `GET`/`PUT /api/v1/settings/backups` | on, every 168 hours (weekly), keep 4, key not included |
| Import mode (move or copy), torrent mode (hard link or copy), allowed import folders, recycle bin | Settings > Library (`/api/v1/import/settings`) | move; hard link; none; `.gonema-recycle` in each root folder. A single bin must be a folder of its own: not `/`, the data folder, a root or movie folder (except `<root>/.gonema-recycle`), nor a folder holding other files. Only files Gonema recycled (with their manifest) are listed and purged. |
| Remote path mappings | Settings > Download clients | none |
| Health thresholds | Settings > Health | see the screen |

## Examples

Binary, data in `/var/lib/gonema`, reachable on the LAN, log file:

```sh
GONEMA_DATA_DIR=/var/lib/gonema GONEMA_BIND=0.0.0.0:7676 GONEMA_LOG_FILE=logs/gonema.log gonema serve
```

Same with flags:

```sh
gonema serve -data-dir /var/lib/gonema -bind 0.0.0.0:7676 -log-file logs/gonema.log
```

Behind a reverse proxy on `https://media.example.org/gonema`:

```sh
gonema serve -bind 127.0.0.1:7676 -base-url /gonema -cookie-secure true
```
