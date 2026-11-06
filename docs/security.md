# Gonema — Sécurité (phase 8)

> Modèle de menace, réglages par défaut et options explicites. Code : `internal/auth`, `internal/netx`, `internal/api/auth_*.go`.

## 1. Modèle de menace

Gonema est auto-hébergé : un binaire ou un conteneur sur une machine personnelle, un NAS ou un petit serveur, souvent exposé derrière un reverse proxy (Caddy, Traefik, nginx) ou un tunnel.

| Acteur | Capacité | Ce qu'il vise | Parade |
|---|---|---|---|
| Internet (instance exposée) | requêtes HTTP arbitraires | lire ou piloter la bibliothèque, les clés des indexeurs, les mots de passe des clients | authentification obligatoire sur toute l'API, limitation des connexions, secrets jamais renvoyés |
| Site tiers visité par l'admin | le navigateur de l'admin, avec ses cookies | déclencher une action (suppression, téléchargement) à son insu | cookie `SameSite=Lax` (préfixe `__Host-` en HTTPS) + contrôle d'origine (CSRF), `frame-ancestors 'none'` |
| Site tiers + DNS rebinding | un nom public qui résout vers l'adresse LAN de Gonema | lire l'API en mode `disabled_for_local`, créer le compte avant l'admin | en-tête `Host` contrôlé (IP, `localhost`, `GONEMA_ALLOWED_HOSTS`), jeton de première installation |
| Utilisateur de Gonema (ou session volée) | saisit des URL d'intégrations | faire parler le serveur à des adresses internes : métadonnées cloud, admin du routeur (SSRF) | `internal/netx` : IP vérifiée à la connexion, plages interdites, webhooks publics seulement |
| Indexeur ou service malveillant | réponses HTTP arbitraires | liens et redirections vers des adresses internes, réponses géantes | liens d'un indexeur public : adresses publiques seulement ; d'un indexeur du LAN : lui-même seulement ; chaque saut revérifié, tailles et délais bornés |
| Copie de la base (sauvegarde perdue) | lecture du fichier SQLite | sessions, clé API, mots de passe | mot de passe en argon2id, jetons et clé API stockés hachés (SHA-256), secrets d'intégrations chiffrés par la clé maître (hors base) |
| Personne sur le LAN | accès réseau local | idem Internet | auth par défaut aussi sur le LAN ; `disabled_for_local` est un choix explicite |

Hors périmètre : un attaquant root sur la machine, ou qui lit à la fois la base et la clé maître ; la sécurité des services tiers eux-mêmes ; le chiffrement TLS (rôle du reverse proxy).

## 2. Authentification

- **Un compte admin**, créé au premier lancement par l'écran d'accueil (`POST /api/v1/auth/setup`, accepté seulement tant qu'aucun compte n'existe).
  - **Jeton de première installation** : tant qu'aucun compte n'existe, le journal de démarrage affiche un jeton à usage unique (`setup=XXXX-XXXX-XXXX-XXXX-XXXX-XXXX`, 120 bits aléatoires, en mémoire seulement, nouveau à chaque démarrage). Le setup l'exige (`setupToken` dans le corps, casse, espaces et tirets ignorés, comparaison en temps constant ; `403` sinon) **sauf** depuis la machine elle-même : connexion directe depuis le loopback, sans en-tête de proxy ni pair de confiance, avec un `Host` loopback (`localhost`, `127.0.0.1`, `::1`). `GET /auth/status` dit à l'interface s'il faut le demander (`setupTokenRequired`). Lire les journaux prouve l'accès au serveur : un visiteur qui atteint l'instance avant l'admin ne peut plus créer le compte. Il disparaît dès que le compte existe. La clé du journal (`setup`) n'est pas masquée par la rédaction, exprès ; les journaux sont aussi lisibles par l'écran Journaux, qui exige déjà un appelant authentifié.
  - **En-tête `Host`** : le setup est refusé (`403`) si `Host` n'est ni une adresse IP, ni `localhost`, ni un hôte de `GONEMA_ALLOWED_HOSTS` ou de `GONEMA_TRUSTED_ORIGINS` (DNS rebinding).
- **Mot de passe** : 10 à 256 caractères, haché en **argon2id** (`golang.org/x/crypto/argon2`, paramètres RFC 9106 n° 2 : 64 Mio, 3 passes, 4 voies ; chaîne PHC, les paramètres suivent le hachage). Comparaison en temps constant ; un nom inconnu coûte le même calcul (pas d'énumération par le temps). Deux calculs au plus en parallèle (mémoire bornée).
- **Sessions côté serveur** : jeton aléatoire de 256 bits dans le cookie `__Host-gonema_session` quand il est `Secure` et sans base URL (le préfixe interdit à un sous-domaine voisin de le poser ou de l'écraser), `gonema_session` sinon (`HttpOnly`, `SameSite=Lax`, `Path=<base URL>/`, `Secure` selon `GONEMA_COOKIE_SECURE`). Transition : les deux noms sont lus ; la connexion suivante pose le nouveau nom et efface l'ancien. Seul son SHA-256 est en base. Durée absolue 30 jours, fin après 14 jours sans usage. Chaque connexion crée une nouvelle session et termine celle que le navigateur présentait (rotation). Changer le mot de passe termine toutes les autres sessions et renouvelle la session courante ; avec `regenerateApiKey: true` (`PUT /api/v1/auth/password`), la clé API est remplacée dans la même transaction et la nouvelle renvoyée une seule fois (`apiKey`), conseillé si le mot de passe a pu fuiter.
- **Limitation des échecs** : 10 échecs par adresse et 20 par compte sur 15 minutes glissantes, puis `429` avec `Retry-After`. Chaque tentative est **réservée avant le hachage** (tentatives en cours + échecs ≥ limite : `429`), donc des requêtes simultanées ne dépassent pas la limite ; un succès remet le compteur à zéro. Une adresse qui s'est connectée avec succès dans les 30 derniers jours est exemptée du compteur par compte (un attaquant ne peut pas verrouiller l'admin ; le compteur par adresse s'applique toujours ; mémoire seulement). C'est un frein, pas un verrouillage : l'admin attend au plus 15 minutes.
- **Calculs argon2** : 2 au plus en parallèle (mémoire bornée), 8 appelants au plus en attente, 3 s d'attente au plus ; au-delà, `429` immédiat (`Retry-After: 2`) au lieu d'une file sans fin.
- **Adresse du client** : le pair TCP. Seulement si ce pair est dans `GONEMA_TRUSTED_PROXIES`, l'adresse retenue est l'entrée de `X-Forwarded-For` la plus à droite qui n'est pas elle-même un proxy de confiance, et `X-Forwarded-Proto` est lu. Sans cette variable (défaut), les en-têtes de proxy sont ignorés : derrière un reverse proxy, tous les clients partagent l'adresse du proxy pour la limitation, réglez-la. Elle ne sert qu'au comptage et à la liste des sessions, jamais à une décision d'accès. La liste des sessions dit aussi comment chacune se connecte (`via` : `direct`, `local` pour une adresse loopback ou privée, `proxy` quand le pair est un proxy de confiance), noté à la connexion et rafraîchi à l'usage.
- **Clé API** : `gnm_` + 256 bits, créée avec le compte et montrée **une seule fois** (réponse du setup, ou `POST /api/v1/auth/api-key` qui la remplace). Stockée hachée ; Réglages > Sécurité n'affiche que ses 4 derniers caractères. Acceptée **uniquement** dans l'en-tête `X-Api-Key`. Une clé dans l'URL (`?apikey=`) est refusée avec un `400` explicite : elle finirait dans les journaux des proxys et l'historique du navigateur. Aucun cas ne l'impose : le flux SSE (`/api/v1/events`) et les images utilisent le cookie dans le navigateur, et les scripts peuvent poser un en-tête.
- **Exception documentée : la clé du flux iCal dans l'URL.** Les applications de calendrier ne savent pas envoyer d'en-tête ; `GET /api/v1/calendar.ics?key=gnc_…` est donc la seule route qui accepte une clé dans la requête. Portée strictement limitée :
  - clé distincte de la clé API (préfixe `gnc_`, 256 bits), en **lecture seule**, qui n'ouvre **que** ce flux : refusée comme clé API, et la clé API ou une session n'ouvrent pas le flux ;
  - stockée hachée (SHA-256, 4 derniers caractères pour l'affichage), montrée une seule fois (`POST /api/v1/settings/calendar-feed/regenerate`, qui révoque l'ancienne) ; `DELETE /api/v1/settings/calendar-feed?confirm=true` coupe le flux ;
  - contenu : dates de sortie des films (titre, année, type de sortie, note d'état, lien vers la fiche) ; aucun chemin, secret ni donnée d'intégration ;
  - limité à 60 requêtes et 10 clés fausses par 10 minutes et par adresse (`429` avec `Retry-After`), mémoire bornée ; les clés fausses sont journalisées avec l'adresse, jamais la clé ;
  - le paramètre s'appelle `key` : `?apikey=` reste refusé partout. Risque assumé : l'URL peut finir dans les journaux d'un proxy ou d'un service de calendrier ; elle ne donne accès qu'au calendrier et se régénère en un clic.
- **Routes publiques** : `GET /api/v1/auth/status`, `POST /api/v1/auth/setup`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /health` (sonde de vie) et les fichiers de l'interface (HTML, JS, CSS, icônes : ils ne contiennent aucune donnée). Tout le reste de `/api/` exige un appelant, y compris `/api/v1/openapi.json`, `/api/v1/events` et `/api/v1/images/…`. Sans service d'authentification câblé, l'API répond `401` (échec fermé).
- **Journaux** : connexion réussie (id du compte et de la session), échec (adresse du client, pour fail2ban ; jamais le nom saisi, qui peut être un mot de passe tapé dans le mauvais champ), changement de mot de passe, révocation, régénération de clé. Jamais de mot de passe, jeton ou clé.

### Option `GONEMA_AUTH=disabled_for_local`

Choix explicite pour un LAN de confiance, **désactivé par défaut**. Une requête passe sans connexion si, à la fois :

- son pair TCP est une adresse loopback ou privée (RFC 1918, `fc00::/7`) **qui n'est pas** dans `GONEMA_TRUSTED_PROXIES` ;
- elle ne porte aucun en-tête de proxy (`X-Forwarded-For`, `X-Forwarded-Proto`, `X-Real-IP`, `Forwarded`, `X-Forwarded-Host`, `CF-Connecting-IP`, `True-Client-IP`) : sinon un reverse proxy sur la même machine ferait passer tout Internet pour « local » ;
- son en-tête `Host` est une adresse IP, `localhost`, un hôte de `GONEMA_ALLOWED_HOSTS` ou de `GONEMA_TRUSTED_ORIGINS` : une page de `evil.example` dont le nom résout vers l'adresse LAN de Gonema (DNS rebinding) arrive avec `Host: evil.example` et reçoit `401`.

Le contrôle CSRF s'applique quand même (un navigateur du LAN porte ce mode). Un avertissement est journalisé au démarrage.

**Dangereux avec la publication de ports Docker ou Podman** : le `docker-proxy` en espace utilisateur (ports publiés avec `userland-proxy`, IPv6, certains hôtes) et le réseau rootless (slirp4netns, pasta) présentent les connexions venues d'Internet avec l'adresse de la passerelle du pont, une adresse privée, sans en-tête de proxy : tout le monde passerait pour « local ». Gonema journalise un avertissement s'il tourne dans un conteneur (`/.dockerenv` ou `/run/.containerenv`) avec ce mode. Ne l'utilisez qu'avec un binaire nu ou le réseau `host`, sur un LAN de confiance. N'utilisez pas non plus ce mode si un proxy qui n'ajoute pas ces en-têtes (tunnel TCP brut) relaie l'instance, sauf à déclarer son adresse dans `GONEMA_TRUSTED_PROXIES`.

### En-tête `Host` et `GONEMA_ALLOWED_HOSTS`

Sans la variable, `Host` n'est contrôlé que pour le mode local et le setup (ci-dessus). Avec `GONEMA_ALLOWED_HOSTS=media.example.org,gonema.lan`, **toute** requête `/api` sans clé API doit nommer un hôte permis (ou une IP, ou `localhost`), sinon `403` : un site rebindé ne peut même pas utiliser un cookie de session. Les fichiers de l'interface restent servis (ils ne contiennent aucune donnée).

## 3. CSRF : contrôle d'origine plutôt que jeton

Les requêtes `POST`, `PUT`, `PATCH`, `DELETE` authentifiées par le cookie (ou par le mode local), et celles des routes d'auth publiques (contre le « login CSRF »), passent par `http.CrossOriginProtection` de la bibliothèque standard : refus si `Sec-Fetch-Site` n'est ni `same-origin` ni `none` ; sinon, si `Origin` est présent, son hôte doit égaler `Host`. Les requêtes à clé API en sont exemptées : un navigateur n'ajoute jamais cet en-tête de lui-même.

Pourquoi pas un jeton synchronisé : l'interface est une SPA de même origine ; `SameSite=Lax` empêche déjà l'envoi du cookie sur un `POST` intersite, et le contrôle d'origine couvre les sous-domaines voisins (même site, autre origine) et les navigateurs anciens, sans état serveur ni plomberie dans chaque appel du client. Derrière un reverse proxy qui réécrit `Host`, ajoutez l'origine publique à `GONEMA_TRUSTED_ORIGINS`.

## 4. En-têtes de sécurité

- **Interface** (`internal/webui`) : CSP stricte, `script-src 'self'` plus les hachages SHA-256 des deux scripts en ligne de `index.html` (thème avant affichage, configuration injectée), calculés au démarrage ; `style-src 'self' 'unsafe-inline'` (attributs `style` posés par Motion et les composants : pas de script possible par là) ; `img-src 'self' data: blob:` ; `connect-src 'self'` ; `frame-src https://www.youtube-nocookie.com` (bandes-annonces) ; `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, `frame-ancestors 'none'`.
- **API** : `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'` (des données, jamais des documents).
- **Partout** : `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy` (`no-referrer` pour l'API, `strict-origin-when-cross-origin` pour l'interface), `Cross-Origin-Resource-Policy: same-origin`, et une `Permissions-Policy` qui coupe toutes les fonctions sensibles (caméra, micro, géolocalisation, USB, paiement…) sauf plein écran, lecture auto, image dans l'image et médias chiffrés, délégués au seul lecteur YouTube.
- Les réponses d'auth portent `Cache-Control: no-store` ; les images (désormais authentifiées) passent de `public` à `private`.
- Les erreurs de validation (`422`) ne recopient plus la valeur reçue : un corps de connexion mal formé ne renvoie pas le mot de passe.

## 5. Connexions sortantes : `internal/netx` (SSRF)

Toute connexion vers une adresse saisie par l'utilisateur passe par `netx` : indexeurs (et téléchargement des NZB/torrents qu'ils désignent), clients de téléchargement, serveurs multimédias, notifications (HTTP et SMTP), webhooks.

- Schémas `http` et `https` seulement, revérifiés à chaque redirection (5 au plus ; les intégrations ne suivent aucune redirection, comme avant).
- L'IP est contrôlée **au moment de la connexion** (`net.Dialer.Control`, après résolution DNS) : un nom vérifié ne peut pas être rebasculé vers une adresse interdite (DNS rebinding).
- **Toujours refusés** : `0.0.0.0/8`, `::`, lien-local (`169.254.0.0/16`, `fe80::/10`), adresses de métadonnées cloud (`169.254.169.254`, `169.254.170.2`, `fd00:ec2::254`, `100.100.100.200`, `168.63.129.16`, `192.0.0.192`), multicast, broadcast, plages réservées et de documentation, IPv4 compatibles (`::a.b.c.d`), NAT64 à usage local `64:ff9b:1::/48` (RFC 8215 : l'opérateur choisit la longueur du préfixe, l'IPv4 peut être n'importe où) sauf sa forme `/96` `64:ff9b:1::a.b.c.d`. Les formes IPv6 qui embarquent une IPv4 (mappée `::ffff:`, NAT64 `64:ff9b::/96` et `64:ff9b:1::/96`, 6to4 `2002::/16`) sont jugées sur l'IPv4 embarquée.
- **Politique par classe** :

| Classe | Concerne | Loopback | Privé (RFC 1918, `fc00::/7`, `100.64.0.0/10`) |
|---|---|---|---|
| intégration | indexeurs, clients, Plex/Jellyfin/Kodi, ntfy, Gotify, Telegram, Pushover, e-mail | autorisé (`GONEMA_OUTBOUND_ALLOW_LOOPBACK=true`) | autorisé (`GONEMA_OUTBOUND_ALLOW_PRIVATE=true`) |
| webhook | webhook générique, Discord, Slack | refusé | refusé ; `GONEMA_WEBHOOK_ALLOW_PRIVATE=true` autorise les deux |

  Une application auto-hébergée parle légitimement à SABnzbd sur `127.0.0.1` ou à un NAS en `192.168.x` : d'où l'autorisation par défaut pour les intégrations. Un webhook n'a pas de raison de viser le LAN, et c'est le vecteur SSRF le plus direct : public seulement par défaut.
- **Liens de téléchargement des indexeurs** (`.nzb`, `.torrent`) : ils viennent d'un tiers, pas de l'utilisateur. Leur portée suit l'indexeur qui les donne (`netx.Scope`) : si l'hôte de l'indexeur résout vers des adresses publiques, le lien et chaque redirection ne peuvent viser que des adresses publiques ; si l'indexeur est sur le LAN, seules ses propres adresses locales sont permises en plus. Chaque saut est vérifié avant la connexion, puis à la connexion ; aucune connexion n'est réutilisée d'un téléchargement à l'autre. Les refus ne nomment pas l'adresse, et la réponse d'une adresse locale atteinte par redirection n'est pas décrite (ni statut, ni contenu) : le message ne sert pas d'oracle sur le LAN.
- **TMDB et le cache d'images** passent aussi par `netx`, en classe publique : adresses publiques seulement, quels que soient les réglages (une réponse DNS hostile ne les fait pas viser le LAN).
- Corps de réponse plafonnés (64 Mio dans `netx`, et les limites propres à chaque client : 4 Mio pour les notifications, 32 Mio pour les clients de téléchargement…) ; délais : connexion 10 s, TLS 10 s, en-têtes 60 s, plus le délai par requête de chaque appelant.
- Proxy sortant (`HTTPS_PROXY`…) : le proxy résout la cible, donc `netx` résout et vérifie l'hôte cible avant de lui confier la requête ; le proxy lui-même est de confiance. Seule la connexion que la fonction `Proxy` de la requête a choisie vers le proxy échappe au contrôle (marque portée par le contexte de la requête) : une requête directe vers la même adresse (cible en `NO_PROXY`, cible loopback) est contrôlée comme les autres. Limite : un proxy qui résout autrement (rebinding) n'est pas contrôlé.
- Le refus est un message clair (« Gonema refuses to connect to 169.254.169.254, a cloud metadata address »), visible dans le test de connexion.

Réglages > Sécurité (`GET /api/v1/settings/security`) affiche la politique en vigueur.

## 6. Secrets dans les réponses et les journaux

Clés des indexeurs, mots de passe et clés des clients, clé TMDB, jetons des serveurs multimédias, secrets de signature et en-têtes des webhooks, URL secrètes Discord/Slack : chiffrés en base (AES-256-GCM, clé maître hors base), jamais renvoyés (les réponses disent seulement `apiKeySet`, `passwordSet`, `tokenSet`…), retirés des messages d'erreur. Le test `TestNoSecretInResponses` (`internal/api/auth_test.go`) enregistre un secret de chaque sorte, lit tous les `GET` sans paramètre, les fiches et lance les tests de connexion, puis cherche chaque valeur dans les réponses et les journaux. `TestEveryRouteRequiresAuth` appelle chaque opération de l'OpenAPI sans identifiants.

## 6 bis. Fichiers, sauvegardes, flux

- **Corbeille** : seuls les fichiers que Gonema y a mis sont listés, restaurables et purgés : nom `AAAAMMJJTHHMMSSZ-<8 hex>-<nom>`, fichier régulier, manifeste `<nom>.gonema.json` valide dont la taille correspond (revérifiée juste avant la suppression). Tout autre fichier du dossier est ignoré, jamais supprimé. Une corbeille unique (`recycleBin`) ne peut être ni `/`, ni dans le dossier de données (ni le contenir), ni un dossier racine ou un dossier de film, ni dedans ou les contenant (sauf `<racine>/.gonema-recycle`), ni un dossier existant qui contient autre chose que des éléments recyclés. Un seul message pour tous ces refus (pas de sonde du système de fichiers).
- **Dossiers racines** : ni `/`, ni le dossier de données, ni la corbeille (ni dedans, ni les contenant). Un dossier absent, illisible, non inscriptible ou réservé reçoit le même message.
- **Envoi d'une sauvegarde** (`POST /api/v1/backups/upload`) : plafond de deux fois la taille de la base actuelle (WAL compris), 1 Gio au moins (`413` au-delà, avant lecture du corps) ; `507` si le dossier de données ou le dossier temporaire n'a pas la place de l'archive et de la base extraite (2 × la taille + 256 Mio).
- **Flux SSE** (`/api/v1/events`, `/api/v1/logs/stream` ensemble) : 64 connexions au plus sur le serveur, 8 par appelant (session, clé API, ou adresse en mode local) ; au-delà `429` avec `Retry-After: 10`.
- **Export CSV de l'historique** : une cellule dont le premier caractère, après les espaces et caractères de contrôle de tête, est `=`, `+`, `-`, `@` ou leur forme pleine chasse (`＝＋－＠`), ou qui commence par une tabulation ou un retour chariot, est préfixée d'une apostrophe (sauf un nombre pur comme `-12`).

## 7. Variables (toutes optionnelles)

| Variable | Défaut | Rôle |
|---|---|---|
| `GONEMA_AUTH` | `enabled` | `disabled_for_local` : pas de connexion pour les adresses loopback et privées en direct, avec un `Host` permis (§ 2) |
| `GONEMA_COOKIE_SECURE` | `auto` | `auto` : `Secure` si la requête arrive en HTTPS ou avec `X-Forwarded-Proto: https` d'un proxy de confiance ; `true` ; `false`. Avec `auto`, avertissement si `X-Forwarded-Proto` vient d'un pair non fiable, ou si les origines de confiance sont en `https` et qu'une connexion arrive en HTTP |
| `GONEMA_TRUSTED_ORIGINS` | vide | origines acceptées par le contrôle CSRF en plus de l'hôte de la requête (`https://media.example.org`) ; leurs hôtes sont aussi des `Host` permis |
| `GONEMA_ALLOWED_HOSTS` | vide | noms d'hôte acceptés dans `Host` en plus des IP et de `localhost` ; si renseignée, toute requête `/api` sans clé API doit en utiliser un |
| `GONEMA_TRUSTED_PROXIES` | vide | adresses ou plages CIDR des reverse proxies dont `X-Forwarded-For` et `X-Forwarded-Proto` sont lus ; jamais « locaux » pour `disabled_for_local` |
| `GONEMA_OUTBOUND_ALLOW_LOOPBACK` | `true` | intégrations vers `127.0.0.0/8` et `::1` |
| `GONEMA_OUTBOUND_ALLOW_PRIVATE` | `true` | intégrations vers les plages privées |
| `GONEMA_WEBHOOK_ALLOW_PRIVATE` | `false` | webhooks vers loopback et plages privées |

Chacune a son drapeau (`-auth`, `-cookie-secure`, `-trusted-origins`, `-allowed-hosts`, `-trusted-proxies`, `-outbound-allow-loopback`, `-outbound-allow-private`, `-webhook-allow-private`).

## 8. Limites connues

- Un seul compte, sans rôles ni 2FA. Pas de réinitialisation du mot de passe par e-mail : en cas d'oubli, arrêter le serveur et supprimer la ligne de la table `users` (les sessions et la clé partent avec), puis recréer le compte.
- La limitation des connexions est en mémoire : elle repart de zéro au redémarrage.
- Pas de journal d'audit dans `history` (table des films) : les événements d'auth sont dans les journaux.
- Avant la création du compte, une personne qui lit les journaux du serveur, ou qui ouvre l'interface depuis la machine elle-même, peut le créer (§ 2).
- Derrière un reverse proxy sans `GONEMA_TRUSTED_PROXIES`, tous les clients partagent l'adresse du proxy pour la limitation des connexions.
