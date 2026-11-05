# CLAUDE.md

> Lu automatiquement à chaque session. Court exprès. Les règles générales (langue, secrets, routage des skills et des agents) sont dans `~/.claude/CLAUDE.md`.

@AGENTS.md

## Projet courant

- **Type** : `TODO` (voir `~/.claude/skills-registry.md`)
- **Stack** : `TODO`
- **Commandes** : `TODO` (install, dev, test, lint, build)
- **Déploiement** : `TODO` (Coolify, Cloudflare Workers, autre)

> Remplis cette section avec `/new-project` ou à la main.

## Documents du projet

- `docs/brief.md` : objectif, périmètre, contraintes.
- `DESIGN.md` : direction visuelle (couleurs, typographie, composants). À respecter pour toute interface.
- `DEPLOY.md` : cible Coolify ou Cloudflare, sous-domaine, DNS, variables (noms), retour arrière.

## Spécifique à ce projet

- `TODO` : conventions propres au projet (ce qui ne se déduit pas du code).
- Variables d'environnement : noms et rôle dans `.env.example` ; valeurs chiffrées dans `secrets/` (`kit secrets`).
