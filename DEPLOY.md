# DEPLOY.md

> Plan de déploiement du projet. Aucune valeur secrète ici : noms de variables seulement. Voir le skill `deploy-coolify-cloudflare`.

## Cible

- **Plateforme** : `TODO` (Coolify | Cloudflare Workers)
- **Source Git** : `TODO` (dépôt, branche de production, branche de préproduction)
- **Mode de build** : `TODO` (Dockerfile | Nixpacks | Docker Compose)
- **Port d'écoute** : `TODO`
- **Route de santé** : `TODO` (ex. `/health`)

## Variables

| Nom | Quand | Rôle |
|---|---|---|
| `TODO` | build / exécution | `TODO` |

Les valeurs vivent dans la ressource Coolify et dans `secrets/` (chiffré). Jamais dans le dépôt.

## Données

- **Volume persistant** : `TODO` (chemin dans le conteneur, ex. `pb_data`)
- **Sauvegarde** : `TODO` (fréquence, destination, test de restauration)

## Domaine

- **FQDN** : `TODO` (ex. `app.exemple.org`)
- **Zone Cloudflare** : `TODO`
- **Enregistrement DNS à créer** : type `TODO`, nom `TODO`, cible `TODO`, proxifié `TODO`
- **SSL/TLS** : Full (strict). Jamais « Flexible ».

## Mise en production

1. Tests, lint et build passent.
2. Variables attendues présentes (`kit secrets check`).
3. Application Coolify créée avec les valeurs ci-dessus.
4. Enregistrement DNS créé, domaine ajouté dans Coolify, certificat émis.
5. Vérification : statut `running:healthy`, URL répond, logs récents sans erreur.

## Retour arrière

`TODO` : redéployer le commit précédent ; restaurer la sauvegarde de la base si le schéma a changé.
