# Operations

Backups and restore, logs, crash recovery of imports, the health check of the container, and upgrades. Configuration options are in [`configuration.md`](configuration.md).

## Data directory

| Path | What | Back it up |
|---|---|---|
| `gonema.db` (+ `-wal`, `-shm`) | SQLite database (WAL mode): library, settings, history, encrypted secrets | through Gonema backups (never copy the live files) |
| `gonema.key` | master key: decrypts the stored secrets (indexer API keys, download client passwords, TMDB key) | **yes, separately**, once: it does not change |
| `backups/` | backup archives | copy them off the machine |
| `restore/` | a staged restore, and the database it replaced | no |
| `cache/` | poster and backdrop cache | no (rebuilt) |
| `logs/` | log files, when `GONEMA_LOG_FILE` is set | no |

When `GONEMA_MASTER_KEY` is set, there is no key file: keep that value where you keep your other secrets.

## Backups

A backup is a zip in `<data dir>/backups/` named `gonema-backup-YYYYMMDD-HHMMSS-<kind>.zip`, holding:

- `gonema.db`: a consistent copy made online with SQLite `VACUUM INTO` (Gonema keeps running; writers are not blocked);
- `manifest.json`: archive format, Gonema version, database schema version, date, kind, SHA-256 of the database, and a fingerprint of the master key (the first 8 bytes of its SHA-256: it tells whether a restore needs another key without revealing it);
- `gonema.key`, **only when asked** (`includeKey`).

**The master key is not included by default.** The database stores your secrets encrypted with it: an archive with both unlocks everything. Back up `gonema.key` once, somewhere else (a password manager, an offline copy). Include it in an archive only if you understand that whoever gets the archive gets your indexer keys and client passwords.

Kinds:

| Kind | Made by | Kept |
|---|---|---|
| `manual` | `POST /api/v1/backups` or `gonema backup` | until you delete it |
| `scheduled` | the scheduled job (weekly by default) | the newest N (`keep`, default 4); older scheduled backups are deleted after each run |
| `pre-restore` | every restore, before staging | until you delete it |
| `uploaded` | `POST /api/v1/backups/upload`, or `gonema restore` with a file from elsewhere | until you delete it |

Schedule: `GET`/`PUT /api/v1/settings/backups` with `enabled`, `intervalHours` (1 to 720, default 168), `keep` (1 to 100, default 4), `includeKey` (default false). A new interval applies at once. The next run time survives restarts. A failed scheduled backup raises a health issue (`backups`).

API:

| Endpoint | Does |
|---|---|
| `GET /api/v1/backups` | list (newest first), the staged restore if any, the folder, and the key notice |
| `POST /api/v1/backups` `{"includeKey": false}` | back up now (201) |
| `GET /api/v1/backups/{name}/download` | stream the archive. `name` must match the archive pattern: no path can be given, nothing outside `backups/` is reachable (symbolic links are not followed) |
| `DELETE /api/v1/backups/{name}?confirm=true` | delete an archive (422 without `confirm=true`) |
| `POST /api/v1/backups/upload` (multipart, field `file`) | add an archive from your computer, after the same validation as a restore |
| `POST /api/v1/backups/restore` `{"name": "...", "confirm": true, "restoreKey": false}` | stage a restore (202) |
| `DELETE /api/v1/backups/restore` | cancel the staged restore |

Command line (the server may be running or stopped):

```sh
gonema backup -data-dir /var/lib/gonema            # prints the archive path
gonema backup -data-dir /var/lib/gonema -include-key   # warns: the archive holds the key
```

Copy the archives off the machine (another disk, another host): a backup on the same disk does not survive the disk.

## Restore

A restore never swaps the database under a running server. It is **staged**, then applied at the next start:

1. The archive is validated: only the expected files (`gonema.db`, `manifest.json`, optional `gonema.key`), Gonema manifest of a known format, database size and SHA-256 equal to the manifest, SQLite `quick_check` clean, schema version equal to the manifest and **not newer than this Gonema** (update Gonema first otherwise). An older schema is accepted: migrations upgrade it at start.
2. The current state is backed up (`pre-restore` archive).
3. The database is extracted to `restore/gonema.db.staged` and a marker `restore/pending.json` is written last.
4. At the next start, before opening the database, Gonema moves the current `gonema.db`, `-wal` and `-shm` (and `gonema.key` with `restoreKey`) to `restore/replaced/<time>/`, puts the staged files in place and removes the marker. The step is idempotent: a crash in the middle is completed at the following start. Only the newest `replaced/` folder is kept, except folders holding a replaced master key, which are never removed.

From the command line:

```sh
gonema restore -data-dir /var/lib/gonema /path/to/gonema-backup-20261106-030000-scheduled.zip
# asks for "yes" (or pass -yes), validates, backs up the current state, stages
systemctl restart gonema    # or: docker restart gonema
```

Master key and restore:

- If the archive was made with the same key as now, nothing to do.
- If the key changed since (new data directory, lost key), the response warns: the secrets of the restored database cannot be decrypted; enter them again, or restore with `restoreKey` when the archive includes its key (`-restore-key` on the command line). The current key is then moved aside, not deleted.
- With `GONEMA_MASTER_KEY`, `restoreKey` is refused: set the variable to the archived key instead.

Cancel a staged restore with `DELETE /api/v1/backups/restore` (the pre-restore archive stays).

**Test your backups**: now and then, restore one into a scratch data directory and look at it on another port:

```sh
gonema serve -data-dir /tmp/gonema-test -bind 127.0.0.1:7677   # creates an empty instance; stop it with Ctrl-C
gonema restore -yes -data-dir /tmp/gonema-test /path/to/archive.zip
gonema serve -data-dir /tmp/gonema-test -bind 127.0.0.1:7677   # applies the restore at start
```

Secrets in the restored copy need the master key the archive was made with (see above).

## Logs

- stderr always (text or JSON, `GONEMA_LOG_FORMAT`); a rotating file with `GONEMA_LOG_FILE`.
- The last `GONEMA_LOG_BUFFER` entries stay in memory: `GET /api/v1/logs?level=warn&since=2026-11-06T08:00:00Z&q=indexer&limit=200` (oldest first, the newest `limit` matches).
- Live: `GET /api/v1/logs/stream` (Server-Sent Events, event `log.entry`, `?level=` filter, `Last-Event-ID` replays what is still in memory). It is a stream of its own, apart from `/api/v1/events`, so a burst of debug lines never pushes application events out of the replay buffer of the main stream.
- `GET /api/v1/logs/download`: the log file and its rotated files (oldest first) when a file is configured, else the in-memory entries, as text.
- Level at runtime: `PUT /api/v1/settings/logs {"level": "debug"}`; not saved across restarts.
- Secrets are redacted in every output (see [configuration](configuration.md#logs)). A log you attach to a bug report should still be read before you post it: names of films and paths are not secrets and stay.

## Crash recovery of imports

An import moves (or links, or copies) a file, then records it in the database. A crash between the two used to leave a file in the library that Gonema did not know about, and a download stuck in "importing".

Every import now writes its intent to the `import_journal` table **before** touching a file (source, destination, mode, expected size, the rows to write), and marks it done in the same transaction as the movie file row. At start, before the download queue resumes, each pending entry is reconciled:

| Found on disk | Gonema does |
|---|---|
| destination present with the expected size (source gone, or kept by a hard link or copy) | completes the database: movie file, history (`recovered: true`), download marked imported. An interrupted replacement is finished: the old file goes to the recycle bin as the import intended |
| a staged file `.gonema-import-*` next to the file it replaces | finishes the replacement (old file to the recycle bin, staged file renamed), then completes the database |
| source present, destination absent | marks the entry failed: the download stays "importing" and the queue imports it again; a manual import must be run again |
| anything else: both missing, a different file at the destination, same-size ambiguity | **touches nothing**, marks the entry `attention` and raises a health issue (`imports`) saying what to check |

Temporary partial copies (`.gonema-partial-*`, Gonema's own unfinished copies) in the destination folder are removed. No user file is ever deleted by the recovery.

`GET /api/v1/imports/journal` lists the entries needing attention (`?all=true`: the latest 500 of every status). After checking the files yourself, `POST /api/v1/imports/journal/{id}/dismiss?confirm=true` clears the entry and its health issue. Done entries are pruned after 30 days.

## Container health check

The image has no shell and no curl: its `HEALTHCHECK` runs `/gonema healthcheck`, which asks `GET /health` on the loopback (port from `GONEMA_BIND`) and exits 0 on HTTP 200. `/health` is public: it says the process serves HTTP, nothing about the library. Outside Docker:

```sh
gonema healthcheck -url http://127.0.0.1:7676/health
```

## Upgrading

1. Back up (`gonema backup` or `POST /api/v1/backups`) and keep a copy of `gonema.key`.
2. Replace the binary (or `docker compose pull && docker compose up -d`).
3. Start: pending migrations run automatically, each in its own transaction. Watch the start-up log.

Downgrading is not supported: an older Gonema refuses a database with a newer schema. To go back, restore the backup made before the upgrade with the older version.

## Releases

Maintainers only. Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml` (GoReleaser, `GITHUB_TOKEN` only):

```sh
git tag -a v1.2.0 -m "v1.2.0" && git push origin v1.2.0
```

- A **draft** GitHub release with the archives (web interface embedded), `checksums.txt` and one SBOM per archive (syft, SPDX).
- The multi-arch image `ghcr.io/fs0ciety7000/gonema:1.2.0` (linux/amd64, arm64, arm/v7), plus `:latest` unless the tag is a pre-release (`v1.2.0-rc.1`).
- Check the draft and the image (`docker buildx imagetools inspect ghcr.io/fs0ciety7000/gonema:1.2.0`), then publish the release. Checklist and rollback: [`DEPLOY.md`](../DEPLOY.md).

## Disk usage

- Scheduled backups: `keep` x (database size, compressed). The database of a 10,000-film library is in the tens of MB.
- Recycle bin: replaced and deleted files stay until you purge them (a health issue mentions a large or old bin).
- Logs: at most `GONEMA_LOG_MAX_SIZE_MB` x (`GONEMA_LOG_KEEP` + 1).
