# Brief du projet

> Rempli en phase 1 de `/new-project`. Source complète : la description initiale du projet (premier message de la session).

## Objectif
Gonema est un gestionnaire de collection de films auto-hébergé pour qui télécharge via Usenet et BitTorrent. Il surveille les films voulus, trouve la meilleure release selon les règles de qualité de l'utilisateur, l'envoie au client de téléchargement, l'importe et la renomme dans la bibliothèque, puis l'améliore dans le temps. Il doit donner l'impression d'un produit premium, pas d'un panneau d'administration.

## Public
Particuliers auto-hébergeurs (NAS, homelab, VPS), souvent en Docker à côté de leurs clients de téléchargement et de leur serveur multimédia (Plex, Jellyfin, Kodi). Profil technique moyen à avancé, sensible à la vitesse et à l'esthétique.

## Indispensable
1. **Moteur de décision transparent** : parseur de noms de releases + profils de qualité + scores personnalisés + décision accept/reject avec raisons + upgrades jusqu'au cutoff.
2. **Chaîne complète** : indexeurs Usenet/torrent (recherche + RSS, en parallèle, tolérants aux pannes) → clients de téléchargement → import (renommage, hardlink/move, mapping de chemins) → échec géré (blocklist + release suivante).
3. **Bibliothèque riche** : métadonnées (posters, notes, bandes-annonces, casting, titres alternatifs, collections), filtres, vues sauvegardées, édition en masse, 10 000 films fluides.
4. **UI premium** : identité de marque et maquettes validées avant tout code front ; file d'attente en direct, calendrier, tableau de bord, palette de commandes, onboarding guidé.
5. **Fiabilité et traçabilité** : historique complet, health checks explicatifs, sauvegardes, notifications, hooks serveurs multimédia.

## Hors périmètre (v1)
- Séries TV, musique, livres (films uniquement).
- Lecteur multimédia / streaming des fichiers.
- Indexeur ou client de téléchargement intégrés (on parle à ceux qui existent).
- Applications mobiles natives (le web doit rester utilisable sur tablette).
- Reprise du nom, du logo ou de l'identité d'un produit existant.

## Contraintes
- Backend en **Go**, obligatoire.
- Livraison : un binaire autonome + une image Docker minimale.
- Budgets mesurés : démarrage à froid < 1 s, parsing d'une release < 50 µs, 10 000 films à 60 fps, recherche interactive en streaming, un indexeur lent ne bloque jamais les autres.
- **Porte dure** : aucun code frontend avant validation de l'identité et des maquettes (phase 2).
- Pas de données fictives dans l'UI finale (mode démo autorisé s'il est signalé).
- Ne jamais supprimer de fichier utilisateur sans confirmation explicite.
- Travail par phases, revue de l'utilisateur après chacune.

## Critères de réussite
- Corpus de 300+ noms de releases réels parsés correctement, benchmarks sous budget.
- Parcours onboarding → premier film récupéré en quelques minutes.
- Chaque release rejetée affiche sa raison ; chaque action automatique figure dans l'historique.
- UI conforme aux maquettes et à `DESIGN.md`, WCAG AA, tests E2E verts.
