# Gonema — Sécurité (phase 8)

> Modèle de menace, réglages par défaut et options explicites. Code : `internal/auth`, `internal/netx`, `internal/api/auth_*.go`.

## 1. Modèle de menace

Gonema est auto-hébergé : un binaire ou un conteneur sur une machine personnelle, un NAS ou un petit serveur, souvent exposé derrière un reverse proxy (Caddy, Traefik, nginx) ou un tunnel.

| Acteur | Capacité | Ce qu'il vise | Parade |
|---|---|---|---|
| Internet (instance exposée) | requêtes HTTP arbitraires | lire ou piloter la bibliothèque, les clés des indexeurs, les mots de passe des clients | authentification obligatoire sur toute l'API, limitation des connexions, secrets jamais renvoyés |
| Site tiers visité par l'admin | le navigateur de l'admin, avec ses cookies | déclencher une action (suppression, téléchargement) à son insu | cookie `SameSite=Lax` + contrôle d'origine (CSRF), `frame-ancestors 'none'` |
| Utilisateur de Gonema (ou session volée) | saisit des URL d'intégrations | faire parler le serveur à des adresses internes : métadonnées cloud, admin du routeur (SSRF) | `internal/netx` : IP vérifiée à la connexion, plages interdites, webhooks publics seulement |
| Indexeur ou service malveillant | réponses HTTP arbitraires | redirections vers des adresses internes, réponses géantes | redirections revérifiées, tailles et délais bornés |
| Copie de la base (sauvegarde perdue) | lecture du fichier SQLite | sessions, clé API, mots de passe | mot de passe en argon2id, jetons et clé API stockés hachés (SHA-256), secrets d'intégrations chiffrés par la clé maître (hors base) |
| Personne sur le LAN | accès réseau local | idem Internet | auth par défaut aussi sur le LAN ; `disabled_for_local` est un choix explicite |

Hors périmètre : un attaquant root sur la machine, ou qui lit à la fois la base et la clé maître ; la sécurité des services tiers eux-mêmes ; le chiffrement TLS (rôle du reverse proxy).

## 2. Authentification

- **Un compte admin**, créé au premier lancement par l'écran d'accueil (`POST /api/v1/auth/setup`, accepté seulement tant qu'aucun compte n'existe). Tant qu'il n'existe pas, toute personne qui atteint l'interface peut le créer : le journal de démarrage l'annonce. Créez-le avant d'exposer l'instance.
- **Mot de passe** : 10 à 256 caractères, haché en **argon2id** (`golang.org/x/crypto/argon2`, paramètres RFC 9106 n° 2 : 64 Mio, 3 passes, 4 voies ; chaîne PHC, les paramètres suivent le hachage). Comparaison en temps constant ; un nom inconnu coûte le même calcul (pas d'énumération par le temps). Deux calculs au plus en parallèle (mémoire bornée).
- **Sessions côté serveur** : jeton aléatoire de 256 bits dans le cookie `gonema_session` (`HttpOnly`, `SameSite=Lax`, `Path=<base URL>/`, `Secure` selon `GONEMA_COOKIE_SECURE`). Seul son SHA-256 est en base. Durée absolue 30 jours, fin après 14 jours sans usage. Chaque connexion crée une nouvelle session et termine celle que le navigateur présentait (rotation). Changer le mot de passe termine toutes les autres sessions et renouvelle la session courante.
- **Limitation des échecs** : 10 échecs par adresse et 20 par compte sur 15 minutes glissantes, puis `429` avec `Retry-After`. C'est un frein, pas un verrouillage : l'admin attend au plus 15 minutes. Derrière un reverse proxy local (pair en loopback ou privé), l'adresse retenue est la dernière entrée de `X-Forwarded-For` (celle ajoutée par le proxy) ; elle ne sert qu'au comptage et à la liste des sessions, jamais à une décision d'accès.
- **Clé API** : `gnm_` + 256 bits, créée avec le compte et montrée **une seule fois** (réponse du setup, ou `POST /api/v1/auth/api-key` qui la remplace). Stockée hachée ; Réglages > Sécurité n'affiche que ses 4 derniers caractères. Acceptée **uniquement** dans l'en-tête `X-Api-Key`. Une clé dans l'URL (`?apikey=`) est refusée avec un `400` explicite : elle finirait dans les journaux des proxys et l'historique du navigateur. Aucun cas ne l'impose : le flux SSE (`/api/v1/events`) et les images utilisent le cookie dans le navigateur, et les scripts peuvent poser un en-tête.
- **Routes publiques** : `GET /api/v1/auth/status`, `POST /api/v1/auth/setup`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /health` (sonde de vie) et les fichiers de l'interface (HTML, JS, CSS, icônes : ils ne contiennent aucune donnée). Tout le reste de `/api/` exige un appelant, y compris `/api/v1/openapi.json`, `/api/v1/events` et `/api/v1/images/…`. Sans service d'authentification câblé, l'API répond `401` (échec fermé).
- **Journaux** : connexion réussie (id du compte et de la session), échec (adresse du client, pour fail2ban ; jamais le nom saisi, qui peut être un mot de passe tapé dans le mauvais champ), changement de mot de passe, révocation, régénération de clé. Jamais de mot de passe, jeton ou clé.

### Option `GONEMA_AUTH=disabled_for_local`

Choix explicite pour un LAN de confiance, **désactivé par défaut**. Une requête dont le pair TCP est une adresse loopback ou privée (RFC 1918, `fc00::/7`) **et** qui ne porte aucun en-tête de proxy (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`, `X-Forwarded-Host`, `CF-Connecting-IP`, `True-Client-IP`) passe sans connexion. Sans cette dernière condition, un reverse proxy sur la même machine ferait passer tout Internet pour « local ». Le contrôle CSRF s'applique quand même (un navigateur du LAN porte ce mode). Un avertissement est journalisé au démarrage. N'utilisez pas ce mode si un proxy qui n'ajoute pas ces en-têtes (tunnel TCP brut) relaie l'instance.

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
- **Toujours refusés** : `0.0.0.0/8`, `::`, lien-local (`169.254.0.0/16`, `fe80::/10`), adresses de métadonnées cloud (`169.254.169.254`, `169.254.170.2`, `fd00:ec2::254`, `100.100.100.200`, `168.63.129.16`, `192.0.0.192`), multicast, broadcast, plages réservées et de documentation, IPv4 compatibles (`::a.b.c.d`). Les formes IPv6 qui embarquent une IPv4 (mappée `::ffff:`, NAT64 `64:ff9b::/96`, 6to4 `2002::/16`) sont jugées sur l'IPv4 embarquée.
- **Politique par classe** :

| Classe | Concerne | Loopback | Privé (RFC 1918, `fc00::/7`, `100.64.0.0/10`) |
|---|---|---|---|
| intégration | indexeurs, clients, Plex/Jellyfin/Kodi, ntfy, Gotify, Telegram, Pushover, e-mail | autorisé (`GONEMA_OUTBOUND_ALLOW_LOOPBACK=true`) | autorisé (`GONEMA_OUTBOUND_ALLOW_PRIVATE=true`) |
| webhook | webhook générique, Discord, Slack | refusé | refusé ; `GONEMA_WEBHOOK_ALLOW_PRIVATE=true` autorise les deux |

  Une application auto-hébergée parle légitimement à SABnzbd sur `127.0.0.1` ou à un NAS en `192.168.x` : d'où l'autorisation par défaut pour les intégrations. Un webhook n'a pas de raison de viser le LAN, et c'est le vecteur SSRF le plus direct : public seulement par défaut.
- Corps de réponse plafonnés (64 Mio dans `netx`, et les limites propres à chaque client : 4 Mio pour les notifications, 32 Mio pour les clients de téléchargement…) ; délais : connexion 10 s, TLS 10 s, en-têtes 60 s, plus le délai par requête de chaque appelant.
- Proxy sortant (`HTTPS_PROXY`…) : le proxy résout la cible, donc `netx` résout et vérifie l'hôte cible avant de lui confier la requête ; le proxy lui-même est de confiance. Limite : un proxy qui résout autrement (rebinding) n'est pas contrôlé.
- TMDB (hôtes fixes `api.themoviedb.org`, `image.tmdb.org`) n'est pas concerné : ses URL ne viennent pas de l'utilisateur.
- Le refus est un message clair (« Gonema refuses to connect to 169.254.169.254, a cloud metadata address »), visible dans le test de connexion.

Réglages > Sécurité (`GET /api/v1/settings/security`) affiche la politique en vigueur.

## 6. Secrets dans les réponses et les journaux

Clés des indexeurs, mots de passe et clés des clients, clé TMDB, jetons des serveurs multimédias, secrets de signature et en-têtes des webhooks, URL secrètes Discord/Slack : chiffrés en base (AES-256-GCM, clé maître hors base), jamais renvoyés (les réponses disent seulement `apiKeySet`, `passwordSet`, `tokenSet`…), retirés des messages d'erreur. Le test `TestNoSecretInResponses` (`internal/api/auth_test.go`) enregistre un secret de chaque sorte, lit tous les `GET` sans paramètre, les fiches et lance les tests de connexion, puis cherche chaque valeur dans les réponses et les journaux. `TestEveryRouteRequiresAuth` appelle chaque opération de l'OpenAPI sans identifiants.

## 7. Variables (toutes optionnelles)

| Variable | Défaut | Rôle |
|---|---|---|
| `GONEMA_AUTH` | `enabled` | `disabled_for_local` : pas de connexion pour les adresses loopback et privées en direct (§ 2) |
| `GONEMA_COOKIE_SECURE` | `auto` | `auto` : `Secure` si la requête arrive en HTTPS ou avec `X-Forwarded-Proto: https` ; `true` ; `false` |
| `GONEMA_TRUSTED_ORIGINS` | vide | origines acceptées par le contrôle CSRF en plus de l'hôte de la requête (`https://media.example.org`) |
| `GONEMA_OUTBOUND_ALLOW_LOOPBACK` | `true` | intégrations vers `127.0.0.0/8` et `::1` |
| `GONEMA_OUTBOUND_ALLOW_PRIVATE` | `true` | intégrations vers les plages privées |
| `GONEMA_WEBHOOK_ALLOW_PRIVATE` | `false` | webhooks vers loopback et plages privées |

Chacune a son drapeau (`-auth`, `-cookie-secure`, `-trusted-origins`, `-outbound-allow-loopback`, `-outbound-allow-private`, `-webhook-allow-private`).

## 8. Limites connues

- Un seul compte, sans rôles ni 2FA. Pas de réinitialisation du mot de passe par e-mail : en cas d'oubli, arrêter le serveur et supprimer la ligne de la table `users` (les sessions et la clé partent avec), puis recréer le compte.
- La limitation des connexions est en mémoire : elle repart de zéro au redémarrage.
- Pas de journal d'audit dans `history` (table des films) : les événements d'auth sont dans les journaux.
- Le premier venu crée le compte tant qu'il n'existe pas (§ 2).
