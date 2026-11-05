# DESIGN.md

> Direction visuelle de Gonema, lue par les agents de design et de front. Statut : phase 2B, à valider par le product owner avant tout code front.
>
> **Sources de vérité** (dans cet ordre) :
> 1. `docs/brand/kit/tokens.css` (généré depuis `tokens.json`) : couleurs, typographie, rayons, ombre, focus de la marque. Ne jamais redéfinir.
> 2. `docs/design/system/gonema.css` : extension du kit (espacements, élévations, verre, mouvement, z-index, densité, mise en page, teinte film) puis base, composants et shell.
> 3. `docs/design/system/index.html` : planche du système, tous les composants dans tous leurs états, contrastes calculés en direct.
>
> En cas de conflit entre ce document et le CSS, le CSS fait foi ; corriger ce document.

## Principes

1. **Une salle privée, pas un tableur.** Noirs chauds, affiches en héros, une seule couleur d'accent, beaucoup d'air. Le thème clair (papier os) est un citoyen à part entière.
2. **Transparent.** Chaque décision automatique se lit en clair : pourquoi une release est rejetée, pourquoi une copie en remplace une autre, ce que Gonema fait ensuite.
3. **Calme.** Rien ne clignote quand tout va bien ; seule la santé prend la parole, une fois, avec une action.
4. **Rapide au clavier.** Palette ⌘K, raccourcis à une lettre, focus toujours visible, actions de masse, Annuler plutôt que confirmer.
5. **Accessible d'emblée.** WCAG 2.1 AA, statut jamais porté par la couleur seule, mouvement réduit respecté par paliers.

## Couleurs

Valeurs exactes : `docs/brand/kit/tokens.css`. Ratios mesurés en direct dans `docs/design/system/index.html#color`.

| Rôle | Sombre (défaut) | Clair | Usage |
|---|---|---|---|
| `bg-base` | `#12100E` | `#F3EDE3` | fond de l'application |
| `bg-raised` | `#1C1815` | `#FBF8F2` | cartes, barre latérale, panneaux |
| `bg-sunken` (ajout) | `#0C0A09` | `#EAE2D5` | champs, puits, journaux |
| `surface-glass` | `#2A231E` @ 72 % | `#FFFDF9` @ 70 % | verre niveau 2 |
| `border` | os @ 12 % | brun @ 14 % | filets décoratifs uniquement |
| `border-control` (ajout) | os @ 38 % | brun @ 56 % | contours de contrôles, ≥ 3:1 |
| `text-primary` / `secondary` / `muted` | `#F2E9DD` / `#C9BBAB` / `#9C8D7D` | `#1C1713` / `#4E443B` / `#6E6257` | texte ; `muted` ≥ 12 px |
| `accent` / `accent-contrast` | `#8F96FF` / `#0E0C1F` | `#3B3FD6` / `#FFFFFF` | action principale, focus, sélection, liens |

**Accent** : au plus un élément plein en accent par vue (le bouton principal). Ailleurs : anneau de focus, sélection (cases, anneau de poster), liens. Jamais comme couleur de statut.

### Statuts

| Statut | Sombre | Clair | Icône (Lucide) | Libellé |
|---|---|---|---|---|
| downloading | `#5EC4D6` | `#0B6E80` | `arrow-down-to-line` | Downloading / `42%` |
| missing | `#E8AE55` | `#8C5300` | `circle-dashed` | Missing / Wanted |
| upgraded | `#D69AEA` | `#86389F` | `circle-arrow-up` | Upgraded |
| failed | `#F27268` | `#B42A22` | `triangle-alert` | Failed |
| available | `#7FCC89` | `#2B7236` | `circle-check` | Available / qualité |
| non surveillé | `text-muted` | `text-muted` | `eye-off` | Not monitored |

Toujours **icône + libellé**. Badge = texte du statut sur une teinte à 14 % de lui-même (`--status-*-bg`). Sur une affiche, badge en verre opaque à 72 % avec l'icône colorée (`.badge.on-media`). Le plus faible ratio : 4,57:1 (badge downloading, clair).

### Teinte film

`movies.dominant_color` → `--movie-dominant` sur `.movie-scope`. Le CSS dérive :

```css
--movie-tint: oklch(from var(--movie-dominant) var(--tint-l) min(c, var(--tint-c)) h);
/* sombre : L 0.80, C ≤ 0.10 · clair : L 0.47, C ≤ 0.13 (paramètres portés par [data-theme]) */
```

La teinte garde la **teinte chromatique** du poster, plafonne la saturation et fixe la luminosité. Elle tient donc ≥ 4,5:1 sur `bg-base` quelle que soit l'entrée, vérifié sur 6 films dans la planche. Elle colore l'atmosphère de la fiche (halo), le bouton principal de la fiche (`.btn-tint`), les anneaux de note, les filets de collection. Elle ne remplace jamais : le focus, les liens, les statuts, les actions destructives. Repli sans `oklch(from …)` : accent de marque.

## Typographie

| Rôle | Police | Échelle (taille/interligne · graisse) |
|---|---|---|
| Display (titres de page, titres de film) | Archivo, `wdth` 125 | 2xl 64/68 · xl 48/52 · lg 36/40 · md 28/34, 600, approche négative |
| Surtitre | Archivo 125, capitales | 11/16 · 600 · +0.12em |
| Interface | Geist | title-lg 20/28 · title-md 16/24 · body-lg 16/24 · body 14/20 · body-sm 13/18 · caption 12/16 |
| Données | Geist Mono | mono 13/20 · mono-sm 12/16 : noms de releases, chemins, scores, journaux |

Chiffres alignés : `tabular-nums` partout (tailles, durées, scores, dates, files). Titres de film en casse de titre. Pas de graisse < 400 sous 16 px.

## Espacements, rayons, ombres, verre

- **Espacements** : base 4 px (`--space-1` à `--space-20` : 4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 64, 80).
- **Rayons** : `xs` 4 (kbd), `sm` 6, `md` 10 (contrôles), `poster` 12, `lg` 16 (cartes, menus), `xl` 24 (dialogues, palette), `pill`.
- **Élévation** : `elev-0` à `elev-4` + `elev-poster` / `elev-poster-hover`. Ombres chaudes ; en sombre, un filet clair en haut plutôt qu'une ombre plus noire.
- **Verre** (fond translucide + flou + bord éclairé + profondeur) :
  - niveau 1 : chrome (barre du haut), flou 16, 72 % ;
  - niveau 2 : panneaux (`surface-glass` de la marque), flou 20 ;
  - niveau 3 : menus, dialogues, toasts, palette, barre d'actions groupées, flou 32, 86 %.
  - Jamais de verre sur du verre. `prefers-reduced-transparency` : tout devient opaque.
- **Mise en page** : conçue à 1440 × 900.
  - Barre latérale de 248 px, en rail de 72 px sous 1100 px ou à la demande. Barre du haut de 64 px.
  - Gouttière de 32 px (24 px en tablette), contenu de 1600 px maximum.
  - Contrôles de 28, 36 ou 44 px.
  - Grille de posters : `auto-fill, minmax(--poster-min, 1fr)`.

## Densité

| | Aérée (défaut, navigation) | Compacte (tableaux, usage intensif) |
|---|---|---|
| Poster minimum | 168 px | 128 px |
| Espacement de grille | 24 / 32 | 16 / 20 |
| Ligne de tableau | 56 px, body 14 | 36 px, body-sm 13 |

Bascule par `[data-density]`, mémorisée par vue sauvegardée (`saved_views.density`).

## Composants

Tous visibles dans `docs/design/system/index.html`, avec les états **défaut, survol, appui, focus, désactivé, chargement** (classes forcées `.is-hover`, `.is-active`, `.is-focus` pour les captures).

- **Boutons** : primaire, secondaire, fantôme, danger, teinte film. Tailles 28, 36 et 44. Appui `scale(0.97)`. Chargement : libellé conservé + balayage lumineux, jamais de spinner.
- **Bouton icône** : fantôme, contour, sur média. Il porte toujours un `aria-label`.
- **Saisie** :
  - champ avec icône, suffixe et erreur, qui précise le problème et la correction ;
  - liste déroulante ;
  - interrupteur ;
  - case à cocher (y compris indéterminée) ;
  - contrôle segmenté ;
  - onglets ;
  - puces de filtre amovibles ;
  - tags.
- **Données** :
  - badge de statut ;
  - badge de qualité (résolution + source, 2160p inversé) ;
  - drapeaux HDR et audio ;
  - pastille de score (+/−, cliquable : détail du score) ;
  - barre de progression en direct ;
  - info-bulle ;
  - lignes de tableau aérées ou compactes, en-têtes triables (`aria-sort`).
- **Superpositions** :
  - menu et menu contextuel (raccourcis, sous-menu, élément coché, désactivé, danger) ;
  - dialogue de confirmation, réservé à la suppression de fichiers ;
  - toast avec **Annuler** (8 s, minuteur en pause au survol ou au focus) ;
  - palette de commandes.
- **Carte poster** : le poster entier est un lien ; les contrôles sont des voisins, jamais imbriqués dans le lien.
  - États : disponible, survol/focus, sélectionné, manquant, téléchargement (progression au pied), échec (filet rouge + badge), amélioré, non surveillé (désaturé), squelette.
  - Sur l'affiche, seuls les états qui demandent de l'attention ; les autres vont dans la ligne d'infos.
- **Shell** : barre latérale (Home, Films, Collections, Calendar, Add & discover · Queue, History, Wanted, Blocklist · Health, Settings), barre du haut (recherche ⌘K, fil d'Ariane, thème), barre d'actions groupées flottante.
- **États vides et erreurs** : bloc vide (titre court, une phrase, une action), message en ligne calme (ce qui s'est passé, ce que Gonema fait ensuite, une action).

## Mouvement

Spécification complète : `docs/design/motion.md`. Démos : `docs/design/motion/index.html`. Images clés : `docs/design/renders/motion-*.jpg`.

- **Durées** : 100 (appui), 150 (survol, sortie), 200 (menus), 250 (dialogue, toast), 300 ms (élément partagé).
- **Courbes** : `--ease-out` (0.23, 1, 0.32, 1) pour les entrées, `--ease-in-out` (0.77, 0, 0.175, 1) pour les déplacements, `--ease-standard` pour la couleur et l'opacité, `--ease-exit` (≤ 150 ms) pour les sorties.
- **Ressorts** en `linear()` :
  - `snappy` (230 ms, sans rebond) : appui, interrupteurs ;
  - `gentle` (330 ms, 3,6 %) : survol des posters, toasts ;
  - `pop` (380 ms, 9 %) : coche, sélection.
- **Règles** :
  - on n'anime que `transform` et `opacity` ;
  - rien d'animé pour les actions au clavier répétées (palette, J/K, navigation de liste) ;
  - cascade uniquement au premier affichage d'une vue (24 ms par élément, 12 au maximum) ;
  - squelettes affichés après 150 ms d'attente et gardés au moins 300 ms ;
  - progression : la valeur glisse en 900 ms, un reflet traverse la partie remplie toutes les 2,4 s.
- **Poster → fiche** : View Transitions (`view-transition-name: poster-<id>`), 300 ms ease-in-out. La grille disparaît en 150 ms, l'atmosphère apparaît en 250 ms, le texte en 200 ms (délai 120). Retour en 250 ms, le focus revient sur le poster.
- **Mouvement réduit** (par paliers, CSS `prefers-reduced-motion` + `matchMedia` en JS) :
  - plus de déplacement ni d'échelle ;
  - fondus gardés et ramenés à 150 ms ou moins ;
  - plus de reflet ni de pulsation ;
  - la valeur de progression continue de se mettre à jour ;
  - l'élément partagé devient un fondu enchaîné de 150 ms.

## Accessibilité

- **Contraste** : texte ≥ 4,5:1, contrôles et anneau de focus ≥ 3:1. Sur une liste ou une ligne survolée ou sélectionnée, le texte `muted` passe en `secondary`. Les lignes rejetées sont atténuées par la couleur, jamais par l'opacité.
- **Focus** : anneau de 2 px d'accent, décalé de 2 px (`--focus-ring`), uniquement en `:focus-visible`, jamais animé. Le poster montre le même chrome au focus qu'au survol.
- **Structure** : un `main` par page, lien d'évitement, `nav` nommées, titres hiérarchisés, tableaux avec `aria-sort`, `role="grid"` pour le tableau multisélection, palette en `combobox` + `listbox` (`aria-activedescendant`).
- **Annonces** : progression `role="progressbar"` (annonce des changements d'état seulement), toasts `role="status"` (erreurs `role="alert"`), résultats de recherche en direct `aria-live="polite"`.
- **Vérification** : axe-core 4.10 (WCAG 2.1 A/AA + best-practice) sur chaque capture, en sombre et en clair, avec `docs/design/tools/render.js` → `docs/design/renders/a11y-report.json`. Objectif : 0 violation. Le texte posé sur les affiches (non mesurable par axe) est protégé par des voiles et des badges opaques.

## Iconographie

Lucide v0.460.0 (licence ISC, © Lucide Contributors ; portions © Cole Bemis, Feather, MIT), embarquée en sprite SVG : `docs/design/system/icons.js`. Trait unique de 1,5 px (1,75 dans les badges). Tailles 14, 16, 18, 20, 24. Les icônes sont décoratives (`aria-hidden`) ; le contrôle porte le libellé. Logo : marque et wordmark `currentColor` du kit, jamais redessinés.

## Mascotte

Kernel (`docs/brand/kit/mascot/`) :
- **Où** : bibliothèque vide (`empty-library`), onboarding (`welcome`), 404 (`404`), détail d'un téléchargement échoué (`failed-download`), écran « tout est à jour » (`neutral`).
- **Jamais** : à côté du logo, dans les tableaux, notifications, navigation, palette, dialogues destructifs, erreurs qui demandent une action (serveur injoignable).
- **Règles** : une seule par écran, 240 px de haut au maximum, jamais animée en boucle.

## Voix

Calme, transparente, brève (voir `docs/brand/kit/guidelines.md` §7). Toujours **ce qui s'est passé** + **ce que Gonema fait ensuite** + une action. Une touche de vocabulaire de salle (séance, copie, bobine, cabine), une plaisanterie par écran au maximum, jamais dans une erreur qui demande d'agir. UI en anglais. Exemples des maquettes : « The house is dark. », « The print arrived damaged. Blocklisted it, trying the next release. », « This reel isn't in the booth. », « 720p is below your profile's floor (1080p). ».

## Maquettes

Galerie : `docs/design/index.html`. Captures 1440 × 900 en sombre et en clair : `docs/design/renders/` (pleine page ignorée par git, à régénérer).

| Fichier | Contenu | Variantes |
|---|---|---|
| `docs/design/system/index.html` | Planche du système | — |
| `docs/design/motion/index.html` | Démos de mouvement | `?motion=reduce` |
| `docs/design/mockups/dashboard.html` | Accueil, alerte santé | — |
| `docs/design/mockups/library-grid.html` | Bibliothèque en posters, sélection, actions groupées | tablette 1024 |
| `docs/design/mockups/library-table.html` | Tableau compact, menu contextuel | — |
| `docs/design/mockups/movie-detail.html` | Fiche film, teinte, fichier, historique | tablette 1024 |
| `docs/design/mockups/interactive-search.html` | Recherche interactive en flux, raisons de rejet, forçage | — |
| `docs/design/mockups/add-film.html` | Ajout (TMDB) et options | `?tab=discover` |
| `docs/design/mockups/queue.html` | File en direct, nouvelle tentative auto, Annuler | `?tab=history` |
| `docs/design/mockups/calendar.html` | Calendrier mensuel | `?view=agenda` |
| `docs/design/mockups/onboarding.html` | Mise en route en 8 étapes | `?step=welcome·account·tmdb·folder·client·indexer·quality·film` |
| `docs/design/mockups/settings-quality.html` | Profils de qualité, règles de score, testeur | `?section=index` (vue d'ensemble des réglages) |
| `docs/design/mockups/command-palette.html` | Palette ⌘K | `?state=recent·query·empty` |
| `docs/design/mockups/empty-states.html` | États vides | — |
| `docs/design/mockups/404.html` | Page introuvable | — |
| `docs/design/mockups/error.html` | Serveur injoignable, reconnexion | — |
| `docs/design/mockups/failed-download.html` | Détail d'un téléchargement échoué | — |

Paramètres communs : `?theme=light|dark`, `?density=compact`, `?motion=reduce`, `?freeze=1` (boucles figées pour les captures). Les films, personnes, groupes et indexeurs sont **fictifs** ; les posters sont générés en SVG (`docs/design/system/posters.js`).

## Écarts assumés

| # | Écart | Par rapport à | Justification |
|---|---|---|---|
| 1 | `--border-control` ajouté (os 38 % / brun 56 %) | Kit : `border` à 12–14 %, « décoratif uniquement » | WCAG 1.4.11 exige 3:1 pour le contour d'un contrôle ; `border` n'y arrive pas. Le kit reste respecté pour les filets décoratifs. |
| 2 | `--bg-sunken`, `--bg-overlay`, élévations 1/3/4, verre niveaux 1 et 3, rayons `xs` (4) et `poster` (12) ajoutés | Kit : une ombre (`raised`), un verre, rayons 6 à 24 | Hiérarchie de surfaces nécessaire à une application dense ; valeurs dérivées de la palette chaude, aucune valeur du kit modifiée. |
| 3 | Archivo Expanded comme display | Brief PO : « display face for titles » | Archivo `wdth` 125 est une grotesque large, pas une police éditoriale à contraste (didone, serif de titrage). Choix validé au brand kit (typo de B) : elle lit comme un carton de générique et reste lisible à petite taille. Si le PO veut une vraie display éditoriale, la remplacer uniquement dans `--font-display`. |
| 4 | Le bouton principal de la fiche film prend la teinte du poster (`.btn-tint`) | Kit : accent outremer « action principale » | Demande du PO (« l'accent s'adapte subtilement à chaque poster »). Limité à la fiche ; focus, liens et sélection restent outremer ; contraste garanti par construction. |
| 5 | Polices hors kit (Archivo en largeur 62, Instrument Serif) dans les posters générés | Kit : 3 familles | Uniquement pour imiter des affiches tierces fictives (contenu, pas interface). Dans le produit, ces emplacements affichent les images TMDB. |
| 6 | Kernel apparaît 2 fois sur `empty-states.html` | Kit : une mascotte par écran | Page catalogue : chaque carte représente un écran différent. Dans le produit, la règle « une par écran » s'applique. |
| 7 | Compteur de la barre d'actions groupées en neutre (`text-primary`), pas en accent | Habitude de nombreuses applis | Règle de marque : un seul élément plein en accent par vue (« Add film » sur la bibliothèque). |
| 8 | Mot « Home » et entrée « Add & discover » dans la navigation | Architecture : « Discover » | Regroupe ajout TMDB et listes de découverte dans un seul écran à onglets ; moins d'entrées de premier niveau. |
| 9 | Densité compacte : body-sm 13 px dans les tableaux | Kit : body 14 | Lisibilité gardée (13 px ≥ seuil de graisse 400) ; nécessaire pour 36 px de hauteur de ligne. |
