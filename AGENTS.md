# AGENTS.md

Règles communes à tous les agents de code (Claude Code, Codex, Cursor...). Format neutre : rien ici n'est propre à Claude.

## Contexte

- **Nom** : Gonema
- **Type** : backend Go + SPA embarquée (application auto-hébergée)
- **Résumé en une phrase** : gestionnaire de collection de films auto-hébergé (Usenet/BitTorrent) qui surveille, choisit la meilleure release selon des règles de qualité, télécharge, importe, renomme et améliore dans le temps.

## Commandes

```bash
install: go mod download            # front (phase 7) : npm ci dans web/
dev:     go run ./cmd/gonema         # serveur à partir de la phase 3+
test:    make test                   # go test ./...
bench:   make bench                  # benchmarks du moteur
lint:    make lint                   # golangci-lint (version compatible Go 1.27 requise)
build:   make build                  # binaire statique bin/gonema (CGO_ENABLED=0)
```

## Structure du dépôt

```
.
├── AGENTS.md
├── CLAUDE.md
├── DESIGN.md           # direction visuelle
├── DEPLOY.md           # Coolify / Cloudflare, domaine, variables (noms)
├── LICENSE             # GPL-3.0
├── Makefile
├── go.mod
├── docs/
│   ├── brief.md        # objectif et périmètre
│   ├── architecture.md # plan par phases, stack, architecture
│   └── data-model.md   # schéma SQLite et types du moteur
├── cmd/gonema/         # point d'entrée
├── internal/           # parser, quality, decision (purs), store, indexer, download, importer…
├── testdata/releases/  # corpus de noms de releases
├── web/                # SPA, uniquement après validation du design (phase 7)
├── .env.example        # noms des variables, valeurs factices
└── secrets/            # secrets chiffrés (sops + age), commitables
```

## Règles de travail

1. Lis avant d'écrire : ouvre les fichiers concernés avant de les modifier.
2. Plus petit changement qui résout le problème. Pas de refactor non demandé.
3. Suis le style existant (nommage, formatage, structure).
4. Ajoute ou mets à jour les tests avec chaque changement de comportement.
5. Lance `test` et `lint` avant de conclure ; rapporte les résultats réels.
6. Ne touche jamais à : `.env*`, `dist/`, `build/`, fichiers de lock modifiés à la main.
7. Tâche de plus de ~5 fichiers ou changement d'architecture : propose d'abord un plan.

## Sécurité

- Aucun secret dans le code, les logs ou les commits. Les secrets passent par `kit secrets`.
- Valide toutes les entrées externes ; requêtes SQL paramétrées uniquement.
- Confirmation avant toute action destructive (suppression, reset, migration ou déploiement en prod).
