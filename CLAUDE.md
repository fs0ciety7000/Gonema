# CLAUDE.md

> Lu automatiquement à chaque session. Court exprès. Les règles générales (langue, secrets, routage des skills et des agents) sont dans `~/.claude/CLAUDE.md`.

@AGENTS.md

## Projet courant

- **Type** : `backend-go` + `design-ui` + `brand` (puis `web-frontend` en phase 7)
- **Stack** : Go 1.27 (net/http + Huma v2, SSE), SQLite (modernc, sqlc, goose) ; front React 19 + Vite + TanStack + Tailwind 4 + Base UI + Motion, embarqué via `go:embed`. Détail : `docs/architecture.md`.
- **Commandes** : `make test`, `make bench`, `make lint`, `make build` (voir `AGENTS.md`)
- **Déploiement** : aucun hébergement par nous. Produit auto-hébergé livré en binaire (GoReleaser) + image Docker `scratch`.

## Documents du projet

- `docs/brief.md` : objectif, périmètre, contraintes.
- `DESIGN.md` : direction visuelle (couleurs, typographie, composants). À respecter pour toute interface.
- `DEPLOY.md` : cible Coolify ou Cloudflare, sous-domaine, DNS, variables (noms), retour arrière.

## Spécifique à ce projet

- **Phases avec revue** : le plan est dans `docs/architecture.md`. S'arrêter à la fin de chaque phase pour validation.
- **Porte dure** : aucun code frontend (`web/`) tant que l'identité et les maquettes de la phase 2 ne sont pas approuvées explicitement.
- **Cœur pur** : `internal/parser`, `internal/quality`, `internal/decision` n'importent ni réseau, ni base, ni horloge. Tests en table, fuzz, benchmarks (`< 50 µs` par parsing).
- **Transparence** : toute décision porte ses raisons ; toute action automatique écrit dans `history`.
- **Fichiers utilisateur** : aucune suppression sans confirmation explicite ; passer par `internal/fsx`.
- **Interdit** : le nom ou l'identité visuelle d'un produit existant (Radarr ou autre). Pas de données fictives dans l'UI finale (mode démo signalé seulement).
- Modèle de données : `docs/data-model.md`. Licence : GPL-3.0.
