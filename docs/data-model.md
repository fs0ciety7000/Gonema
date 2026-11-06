# Gonema — Modèle de données (phase 1)

> Statut : **proposition, en attente de validation**. Base SQLite (WAL, `foreign_keys=ON`), migrations goose, requêtes sqlc.

## Conventions

- Clés primaires `INTEGER PRIMARY KEY` (rowid, rapide). Identifiants externes (`tmdb_id`, `imdb_id`) en colonnes uniques.
- Horodatages en `INTEGER` : millisecondes Unix UTC. Les dates sans heure (sorties) en `TEXT` ISO `YYYY-MM-DD`.
- Colonnes `JSON` (type `TEXT`, vérifié `json_valid`) pour les données affichées mais jamais filtrées. Tout ce qui sert à filtrer, trier ou joindre est en colonne.
- Énumérations stockées en `TEXT` lisible (`'downloading'`), validées en Go et par `CHECK`.
- Secrets (clés API d'indexeurs, mots de passe de clients) : colonne `secrets BLOB`, chiffrée AES-256-GCM avec la clé maître (`/config/gonema.key` ou variable `GONEMA_MASTER_KEY`). L'API ne les renvoie jamais en clair.
- `history` est en ajout seul : aucune mise à jour, aucune suppression hors purge de rétention explicite.

## Dossier de données (`/config`)

```
gonema.db          base SQLite (+ -wal, -shm)
gonema.key         clé maître (0600), à sauvegarder à part
backups/           archives zip : base + config (sans la clé)
logs/              journaux tournants, lus par l'UI
cache/images/      posters et backdrops redimensionnés (WebP)
```

## Schéma relationnel

```
quality_profiles ─┬─< quality_profile_items >── qualities (catalogue figé, en Go)
                  └─< profile_scores >── scoring_rules ─< scoring_conditions
root_folders ─┐
collections ──┼─< movies ─┬─< alternate_titles
tags ─< movie_tags >──────┤  ├─< movie_files
                          │  ├─< downloads >── download_clients
                          │  ├─< history
                          │  └─< blocklist >── indexers ─ rss_state
import_lists ─< import_list_exclusions        jobs   events (SSE, en mémoire)
users ─< sessions   api_keys   saved_views   notifications   media_servers
remote_path_mappings   settings
```

## Tables

### Bibliothèque

**movies**
| Colonne | Type | Rôle |
|---|---|---|
| id | INTEGER PK | |
| tmdb_id | INTEGER UNIQUE NOT NULL | identité canonique |
| imdb_id | TEXT | indexé, recherche indexeurs |
| title, original_title, sort_title, clean_title | TEXT | `clean_title` normalisé pour le matching |
| original_language | TEXT | ISO 639-1 |
| year | INTEGER | |
| overview | TEXT | |
| runtime_min | INTEGER | sert aussi aux limites de taille (Mo/min) |
| status | TEXT | `announced` · `in_cinemas` · `released` · `deleted` (retiré de TMDB) |
| release_cinema, release_digital, release_physical | TEXT (date) | calendrier, disponibilité minimale |
| certification | TEXT | |
| genres | JSON | tableau de noms (filtrable via `json_each`) |
| ratings | JSON | `{tmdb:{value,votes}, imdb:{…}}` |
| poster_path, backdrop_path | TEXT | chemins TMDB, images mises en cache localement |
| dominant_color | TEXT | `#rrggbb` extrait du poster, accent de la page film |
| extra | JSON | casting, équipe, vidéos (bandes-annonces), mots-clés : affichage seul |
| collection_id | INTEGER FK → collections | nullable |
| monitored | INTEGER (bool) | |
| quality_profile_id | INTEGER FK | |
| minimum_availability | TEXT | `announced` · `in_cinemas` · `released` |
| root_folder_id | INTEGER FK | |
| path | TEXT UNIQUE | dossier du film, relatif au root folder |
| added_at, metadata_refreshed_at, last_searched_at | INTEGER | |

Index : `(monitored, status)`, `(year)`, `(sort_title)`, `(added_at)`, `(collection_id)`, `(imdb_id)`.
Le statut de bibliothèque (`missing`, `downloading`, `available`, `upgradable`) n'est pas stocké : il est dérivé de `movie_files`, `downloads` et du profil, et exposé par une vue SQL `movie_state`.

**alternate_titles** — `id, movie_id FK CASCADE, title, clean_title, country, source ('tmdb'|'user')`, index `(clean_title)`. Sert au matching des releases étrangères.

**collections** — `id, tmdb_id UNIQUE, name, overview, poster_path, backdrop_path, monitored, quality_profile_id, root_folder_id, minimum_availability, refreshed_at`. `monitored` = ajouter automatiquement les films de la saga.

**movie_files**
| Colonne | Type | Rôle |
|---|---|---|
| id | INTEGER PK | |
| movie_id | INTEGER FK UNIQUE | un fichier principal par film (v1) |
| relative_path | TEXT | relatif à `movies.path` |
| size | INTEGER | octets |
| quality | TEXT | id du catalogue (`bluray-2160p`, `webdl-1080p`…) |
| revision | INTEGER | 1 = original, +1 par proper/repack |
| edition | TEXT | `Director's Cut`, `IMAX`… |
| languages | JSON | |
| release_group, scene_name | TEXT | nom d'origine de la release |
| parsed | JSON | sortie complète du parseur au moment de l'import |
| media_info | JSON | codecs, HDR, pistes audio/sous-titres (ffprobe facultatif) |
| custom_score | INTEGER | score figé à l'import, recalculé si les règles changent |
| imported_at | INTEGER | |

**tags** — `id, label UNIQUE`. **movie_tags** — `(movie_id, tag_id)` PK. Les tags relient films ↔ indexeurs, clients, notifications.

**root_folders** — `id, path UNIQUE, free_space, total_space, checked_at`. Espace disque rafraîchi par un job.

### Qualité et score

**qualities** — catalogue figé en Go (pas en base) : `id`, `source` (`cam`, `telesync`, `telecine`, `screener`, `dvd`, `hdtv`, `webrip`, `webdl`, `bluray`, `remux`, `brdisk`), `resolution` (480, 576, 720, 1080, 2160), `rank`. Les bornes de taille par qualité sont en base :

**quality_sizes** — `quality PK, min_mb_per_min, preferred_mb_per_min, max_mb_per_min`.

**quality_profiles** — `id, name UNIQUE, upgrade_allowed, cutoff_quality, min_score, cutoff_score, min_upgrade_step, languages JSON`.

**quality_profile_items** — `profile_id, position, quality, group_name, allowed`. L'ordre définit la préférence ; un groupe (« HD-1080p ») rend plusieurs qualités équivalentes.

**scoring_rules** — `id, name UNIQUE, include_in_rename, description`. Une règle = une condition composée (« HDR10+ », « Groupe de confiance », « Mauvais encodeur »).

**scoring_conditions** — `id, rule_id FK CASCADE, kind, negate, required, value JSON`.
`kind` ∈ `title_regex`, `release_group`, `source`, `resolution`, `codec`, `hdr`, `audio`, `edition`, `language`, `indexer_flag`, `size_range`, `year_range`. Sémantique : toutes les conditions `required` doivent être vraies et au moins une non-`required` de chaque `kind`.

**profile_scores** — `(profile_id, rule_id) PK, score INTEGER` (positif ou négatif).

### Recherche et téléchargement

**indexers**
| Colonne | Rôle |
|---|---|
| id, name UNIQUE, kind (`newznab` · `torznab`), protocol (`usenet` · `torrent`) | |
| base_url, api_path, categories JSON | |
| secrets BLOB | clé API chiffrée |
| priority | départage à score égal |
| enable_rss, enable_auto_search, enable_interactive | |
| min_seeders, seed_ratio, seed_time_min | torrents |
| tags JSON | restreint l'indexeur à certains films |
| failure_count, disabled_until, last_error, last_ok_at | disjoncteur et santé |

**rss_state** — `indexer_id PK, last_guid, last_pub_at, last_sync_at`.

**download_clients** — `id, name, kind (sabnzbd·nzbget·qbittorrent·deluge·transmission), protocol, host, port, use_tls, url_base, secrets BLOB, category, priority, enabled, remove_completed, remove_failed, tags JSON, failure_count, last_error`.

**remote_path_mappings** — `id, host, remote_path, local_path`, unique `(host, remote_path)`. Traduit le chemin vu par le client en chemin vu par Gonema.

**downloads** (file d'attente suivie)
| Colonne | Rôle |
|---|---|
| id | |
| movie_id FK | |
| client_id FK, client_ref | identifiant côté client (nzo_id, hash) ; unique `(client_id, client_ref)` |
| indexer_id FK | |
| release_title, release_guid, info_url | |
| protocol, size | |
| quality, revision, custom_score | décision au moment du grab |
| decision | JSON : décision complète (raisons, détail du score) pour la traçabilité |
| status | `queued` · `downloading` · `paused` · `completed` · `importing` · `imported` · `failed` · `warning` · `removed` |
| progress, speed_bps, eta_s | mis à jour en mémoire, persistés à basse fréquence |
| output_path | chemin local après mapping |
| status_messages | JSON |
| retry_of | FK → downloads : chaîne des nouvelles tentatives |
| grabbed_at, completed_at, imported_at | |
| manual | bool : grab manuel (override) |

**blocklist** — `id, movie_id FK nullable, kind ('release'|'group'), source_title, release_group, indexer_id, protocol, info_hash, reason, created_at`. Index `(movie_id)`, `(info_hash)`, `(release_group)`.

**history** (ajout seul) — `id, movie_id FK, event, source_title, quality, custom_score, download_id, data JSON, created_at`. Index `(event, id)` pour le filtre par type de l'écran History (migration 00005). Les renommages et déplacements de dossier écrivent `file_renamed` (`data.kind` = `rename` | `move`), les suppressions demandées `file_deleted` (fichiers dans la corbeille).
`event` ∈ `grabbed`, `grab_failed`, `download_failed`, `imported`, `upgraded`, `import_failed`, `file_deleted`, `file_renamed`, `blocklisted`, `ignored`, `metadata_changed`. Index `(movie_id, created_at)`, `(created_at)`.

### Découverte

**import_lists** — `id, name, kind (tmdb_popular·tmdb_trending·tmdb_upcoming·tmdb_collection·tmdb_person·tmdb_list), settings JSON, quality_profile_id, root_folder_id, minimum_availability, monitored, auto_add, search_on_add, sync_interval_min, last_sync_at`.

**import_list_exclusions** — `tmdb_id PK, title, year, created_at`.

### Intégrations

**notifications** — `id, name, kind (webhook·discord·telegram·pushover·ntfy·gotify·slack·email…), settings JSON, secrets BLOB, events JSON` (`grab`, `import`, `upgrade`, `failure`, `health`, `rename`, `delete`) `, tags JSON, enabled`.

**media_servers** — `id, name, kind (plex·jellyfin·kodi), base_url, secrets BLOB, path_mappings JSON, refresh_on JSON, enabled`.

### Système

**jobs** — `id, kind, payload JSON, status (pending·running·done·failed·cancelled), priority, dedupe_key UNIQUE NULL, run_at, attempts, max_attempts, last_error, started_at, finished_at, created_at`. Index `(status, run_at, priority)`. Les jobs `running` au démarrage repassent en `pending` (reprise).

**users** — `id, username UNIQUE, password_hash (argon2id), role (admin·user), created_at, last_login_at`.
**sessions** — `token_hash PK, user_id FK, expires_at, user_agent, ip, created_at`.
**api_keys** — `id, user_id FK, name, key_hash, prefix, created_at, last_used_at`. La clé n'est montrée qu'une fois.

**saved_views** — `id, user_id FK NULL, page, name, filters JSON, sort JSON, columns JSON, density ('airy'|'compact'), position`. `user_id` NULL = vue partagée (avant l'authentification de la phase 8) ; nom unique par `(page, name)` pour les vues partagées (migration 00004).

**library_version** — compteur unique incrémenté par triggers sur les colonnes visibles de la bibliothèque ; invalide l'index en mémoire des listes (migration 00004).

**settings** — `key PK, value JSON`. Nommage (gabarits), mode d'import (hardlink/copy/move), rétention, port, URL de base, clé TMDB (chiffrée), etc.

## Types du moteur pur (Go, hors base)

```go
type ParsedRelease struct {
    Title        string; Year int; CleanTitle string
    Resolution   Resolution; Source Source; Modifier Modifier // remux, brdisk…
    VideoCodec   Codec; BitDepth int; HDR []HDRFormat          // hdr10, hdr10+, dv, hlg
    Audio        []AudioFormat; Channels string                // truehd-atmos, dts-x, 7.1…
    Edition      Edition                                       // flags: directors, extended, imax…
    Languages    []Language; HardcodedSubs bool; Subs string
    Revision     Revision                                      // proper, repack, version
    ReleaseGroup string; Container string; Streaming string    // amzn, nf…
    Hash         string                                        // [ABCD1234]
}

type Decision struct {
    Accepted   bool
    Rejections []Rejection      // Code stable + Message lisible + Permanent bool
    Score      ScoreBreakdown   // total + contribution de chaque règle
    Quality    QualityID
    UpgradeOf  *MovieFileRef    // si c'est une amélioration
}
```

Les décisions sont ordonnées de façon déterministe : acceptées d'abord, puis qualité (rang dans le profil), score, révision, protocole/priorité indexeur, taille proche de la préférée, âge.
