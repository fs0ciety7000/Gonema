# Gonema — Plan, stack et architecture (phase 1)

> Statut : **option A validée** le 2026-11-05 (clé TMDB par utilisateur, licence GPL-3.0). Modèle de données : `docs/data-model.md`.
> Versions vérifiées le 2026-11-05 sur go.dev, proxy.golang.org et registry.npmjs.org.

## 1. Plan par phases

| # | Phase | Livrable | Porte |
|---|---|---|---|
| 1 | Architecture, stack, modèle de données | ce document + `docs/data-model.md` | validation stack, puis modèle |
| 2 | Marque et design | 3 directions, brand kit, maquettes HD, motion spec, design system, `DESIGN.md` | **aucun code front avant validation** |
| 3 | Moteur cœur (test-first) | `parser`, `quality`, `decision` purs + corpus 300+ releases + benchmarks | revue |
| 4 | Métadonnées | fournisseur TMDB, matching de titres, cache images | revue |
| 5 | Indexeurs, clients, import | Newznab/Torznab, 5 clients, import, échecs, blocklist | revue |
| 6 | Serveurs multimédia, notifications | Plex/Jellyfin/Kodi, webhooks et services push | revue |
| 7 | UI complète | tous les écrans, conformes aux maquettes | revue |
| 8 | Durcissement | auth, clé API, SSRF, sauvegardes, Docker, docs, E2E | revue |

La phase 3 peut démarrer en parallèle de la phase 2 : elle ne contient aucun code front.

## 2. Stack proposée

### Option A — recommandée : Go + SQLite embarqué + React SPA embarquée

| Couche | Choix (version) | Pourquoi |
|---|---|---|
| Langage | **Go 1.27.3** | Imposé. Binaire statique, démarrage en ms, concurrence native pour le fan-out indexeurs. |
| HTTP / API | **net/http** (routeur stdlib) + **Huma v2.40** | API REST décrite en Go, OpenAPI 3.1 générée automatiquement, validation des entrées, support SSE. Aucune dépendance de routeur. |
| Temps réel | **Server-Sent Events** | File d'attente, progression, résultats de recherche en streaming. Plus simple que WebSocket, traverse les reverse proxies, reconnexion native. |
| Base | **SQLite** (WAL) via **modernc.org/sqlite v1.61** | Zéro service à installer, sauvegarde = un fichier, 10 000 films est un petit volume. Pilote pur Go : `CGO_ENABLED=0`, compilation croisée triviale. |
| Requêtes | **sqlc v1.31** | SQL écrit à la main, vérifié et typé à la compilation. Pas d'ORM, requêtes lisibles et rapides. |
| Migrations | **goose v3.29**, migrations embarquées (`embed`) | Appliquées au démarrage, versionnées, réversibles. |
| Tâches | ordonnanceur maison + table `jobs` persistante | RSS, recherches, suivi des téléchargements, imports : reprennent après redémarrage, rien ne se perd. |
| Logs | `log/slog` (JSON ou texte) | Stdlib, structuré, visible dans l'UI. |
| Métadonnées | **TMDB API v3** (clé de l'utilisateur) | Posters, backdrops, casting, collections, titres alternatifs, dates de sortie par type, bandes-annonces. |
| Frontend | **React 19.3 + TypeScript 7.0 + Vite 8.3** | Écosystème le plus riche pour une UI premium ; TS 7 (compilateur natif) rend le typage quasi instantané. SPA statique embarquée dans le binaire via `go:embed`. |
| Routage / données | **TanStack Router 1.170**, **TanStack Query 5.106** | Routes typées, préchargement au survol (écrans « instantanés »), cache invalidé par les événements SSE. |
| Listes | **TanStack Virtual 3.14** | Grille et table virtualisées pour 10 000 films à 60 fps. |
| Style | **Tailwind CSS 4.3** piloté par les tokens de `DESIGN.md` (variables CSS) | Tokens = source unique, thèmes clair/sombre et accent dynamique par poster via variables. |
| Primitives UI | **Base UI 1.9** (non stylée, accessible) | Focus, clavier, ARIA gérés ; 100 % du style est le nôtre, donc aucun rendu « composant par défaut ». |
| Palette de commandes | **cmdk 1.1** | Accessible, filtrage rapide, entièrement stylable. |
| Animation | **Motion 14.9** + View Transitions API | Transitions poster → détail (éléments partagés), ressorts, entrées échelonnées ; `prefers-reduced-motion` respecté. |
| Client API | **openapi-typescript 7.13 + openapi-fetch 0.17** | Types front générés depuis l'OpenAPI de Huma : le front ne peut pas diverger du back. |
| Tests Go | `testing` (fuzzing natif, `b.Loop` benchmarks), **go-cmp**, **rapid 1.3** (propriétés), golden files | Parseur et décision purs, corpus en fichier, fuzz contre les paniques. |
| Tests front | **Vitest 5.1**, **Playwright 1.64** | Unitaires + E2E + captures de régression visuelle contre les maquettes. |
| Qualité | `golangci-lint`, **Biome 2.6** | Un outil par langage, rapide. |
| Livraison | **GoReleaser v2.20**, image Docker `FROM scratch` (+ certificats CA, tzdata embarqué) | Binaires multi-OS/arch, image ~20 Mo, utilisateur non-root, `PUID/PGID` documentés. |

**Coût de maintenance** : faible côté Go (stdlib + 4 dépendances majeures), moyen côté front (écosystème React mouvant, atténué par des versions épinglées et Renovate).
**Risque principal** : la qualité premium de l'UI repose entièrement sur nous (Base UI est non stylé) ; c'est voulu, mais demande une phase 2 solide et des tests visuels.

### Option B — alternative : Go + SQLite + SvelteKit 3 (Svelte 5.57, adapter statique)

- **Avantages** : bundle plus léger, transitions et animations intégrées au langage, moins de code pour l'état réactif.
- **Coût de maintenance** : faible à moyen.
- **Risque principal** : écosystème plus petit pour les primitives accessibles et la virtualisation avancée ; stack moins familière que React pour toi.

Le backend est identique dans les deux options : seul le front change.

### Écartés
- **PostgreSQL** : un service de plus pour l'utilisateur, aucun gain à cette échelle.
- **ORM (GORM, ent)** : moins lisible et moins rapide que sqlc pour un modèle stable.
- **Next.js** : SSR inutile pour une app auto-hébergée derrière authentification, et casse l'idée du binaire unique.
- **PocketBase** : pratique, mais impose son modèle d'auth et d'API ; on perd le contrôle du moteur et du schéma.
- **WebSocket** : SSE suffit (flux serveur → client), les actions passent par REST.

## 3. Architecture

```
                 ┌──────────────────────── binaire gonema ────────────────────────┐
 navigateur ───► │ api (Huma, REST + SSE) ── web (SPA embarquée)                  │
                 │   │                                                             │
                 │   ▼                                                             │
                 │ services ── library · search · grab · import · health · backup  │
                 │   │   ▲                                                         │
                 │   │   └── events (bus interne) ──► SSE · history · notify       │
                 │   ▼                                                             │
                 │ moteur PUR : parser → quality → decision   (aucune E/S)         │
                 │   │                                                             │
                 │ adaptateurs : metadata/tmdb · indexer/{newznab,torznab}         │
                 │               download/{sabnzbd,nzbget,qbit,deluge,transmission}│
                 │               mediaserver/{plex,jellyfin,kodi} · notify/*       │
                 │   │                                                             │
                 │ jobs (ordonnanceur persistant)    netx (client HTTP anti-SSRF)  │
                 │ store (sqlc + SQLite WAL)         fsx (hardlink/move, mapping)  │
                 └─────────────────────────────────────────────────────────────────┘
```

### Arborescence prévue

```
cmd/gonema/            point d'entrée, config, câblage
internal/
  parser/              release name → struct (pur, corpus + fuzz + bench)
  quality/             qualités, profils, règles de score (pur)
  decision/            accept/reject + raisons, upgrade/cutoff (pur)
  domain/              types partagés (Movie, Release, Decision…)
  store/               migrations goose, requêtes sqlc
  metadata/tmdb/       fournisseur de métadonnées
  indexer/             interface + newznab, torznab, rss
  download/            interface + clients
  importer/            gabarits de nommage, hardlink/move, mapping de chemins
  search/              fan-out parallèle, timeouts, disjoncteur par indexeur, streaming
  jobs/                ordonnanceur, file persistante, reprise
  events/              bus, historique, diffusion SSE
  mediaserver/  notify/
  netx/  fsx/          client HTTP durci, opérations fichiers sûres
  api/                 handlers Huma, auth, OpenAPI
web/                   SPA (phase 7 uniquement)
testdata/releases/     corpus de noms de releases
```

### Principes structurants
1. **Cœur pur** : `parser`, `quality`, `decision` n'importent ni réseau, ni base, ni horloge (l'heure est passée en paramètre). Déterministes, testés en table, benchmarkés.
2. **Interfaces aux bords** : `Indexer`, `DownloadClient`, `MediaServer`, `Notifier`, `MetadataProvider`. Chaque implémentation a des tests contre des réponses HTTP enregistrées.
3. **Isolation des pannes** : chaque appel externe a un `context` avec délai, un disjoncteur et un backoff ; la recherche diffuse chaque résultat dès qu'un indexeur répond.
4. **Tout passe par des événements** : chaque action automatique produit un événement persistant (historique), diffusé en SSE et routé vers les notifications.
5. **Aucune suppression implicite** : les opérations destructrices sur le disque passent par `fsx` avec corbeille ou confirmation explicite.
6. **Secrets** : clés d'indexeurs et de clients chiffrées au repos (clé maître hors base), jamais renvoyées en clair par l'API.

### Mesure des budgets
- Démarrage : test qui lance le binaire et chronomètre `/health` prêt.
- Parsing : `BenchmarkParse` sur le corpus, échec CI si > 50 µs/op.
- Défilement : trace Playwright sur une base de démo de 10 000 films (mode démo signalé), FPS mesurés.
- Streaming : test d'intégration avec indexeurs simulés lents et en panne.

