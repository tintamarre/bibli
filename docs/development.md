# Développer Bibli

Tout ce qu'il faut pour lancer Bibli depuis le code, le modifier et en publier une version. Les règles du projet (dépendances, textes, migrations, style des commits) sont dans [CONTRIBUTING.md](../CONTRIBUTING.md).

## Prérequis

| Outil | Pour |
|---|---|
| Go ≥ 1.27 | Compiler et lancer Bibli, les tests. |
| `sqlite3` | Charger le jeu de démonstration à la main, les captures d'écran. |
| Node ≥ 22 et Chrome ou Chromium | Les captures d'écran (`scripts/screenshots.mjs`). |
| `python3` | Régénérer les prêts de démonstration (`scripts/gen-demo-loans.py`), construire l'application Mac hors d'un Mac. |
| `zip`, `tar` | Les applications de bureau. |

Rien d'autre à installer : deux dépendances Go (`modernc.org/sqlite` et `golang.org/x/sys`, que la première apporte déjà, pour le service Windows), pas de `package.json`.

## Démarrer

    git clone https://github.com/tintamarre/bibli.git
    cd bibli
    make demo    # facultatif : la base de démonstration, voir plus bas
    make dev

`make dev` compile et lance Bibli sur <http://127.0.0.1:8080> (mot de passe `dev`), puis **recompile et redémarre à chaque changement**, y compris sur un gabarit, du CSS, du JS ou une migration : tout cela est embarqué dans le binaire par `embed.FS`, donc rien ne bouge sans recompilation.

- La base est `data/biblio.db`, la même que le conteneur Docker de développement ; la cible arrête d'abord ce conteneur, qui tiendrait le port et le fichier.
- Une compilation en échec **ne coupe pas le serveur** : la version précédente continue de répondre, et le message d'erreur s'affiche.
- La détection de changement est un `find -newer`, une fois par seconde (`scripts/dev.sh`).
- `make dev-docker` fait la même chose à travers l'image, pour vérifier ce qu'une vraie installation exécutera. Plus lent, à réserver à ce contrôle.

Sans `make` :

    go mod download
    BIBLI_ADMIN_PASSWORD=dev go run ./app -db biblio.db -secure-cookies=false

## Le jeu de démonstration

    make demo                           # recrée data/biblio.db avec le jeu de démonstration
    sqlite3 biblio.db < app/demo.sql    # ou le charger dans une autre base

Une école entière, fictive : **100 ouvrages et 115 exemplaires**, **30 lecteurs répartis en quatre groupes (P1 à P4)** et **3 enseignants** (le groupe « Enseignants »), et **un an de prêts** au rythme d'une vraie bibliothèque (une ou deux ouvertures par semaine), dont deux en retard. C'est le jeu de l'[instance de démonstration](deployment.md#instance-de-démonstration).

Les dates y sont relatives au jour du chargement : le jeu ne se périme pas. Les ISBN sont réels : chacun est une édition connue de la BnF, avec son titre, son auteur, son éditeur et son année, donc l'enrichissement et les couvertures fonctionnent comme sur un vrai rayon. `scripts/gen-demo-loans.py` régénère les prêts et les groupes.

## Vérifier avant un commit

    make check    # gofmt -l, go vet, go test
    make help     # toutes les cibles

La CI rejoue exactement ces étapes avant de construire l'image. `i18n_test.go` échoue tant qu'un texte manque dans une des trois langues.

`BIBLI_LOG_COLOR=1` colore le journal dans un terminal.

## Captures d'écran

    node scripts/screenshots.mjs

Compile le code de la copie de travail, crée une base jetable, y charge le jeu de démonstration, lance un serveur sur un port libre et photographie chaque écran dans `screenshots/` (`-o DIR` pour choisir ailleurs, `--only nom,nom` pour n'en refaire qu'un). Tout ce qui a été créé en chemin est effacé en sortant.

Pourquoi un script plutôt qu'une touche « impression écran » : les dates du jeu de démonstration sont relatives au jour où il est chargé, donc une capture vieillit (« 5 jours de retard » n'est vrai que le jour où elle a été prise). Une exécution refait la série entière en une minute.

Les images de `docs/img/` en sont tirées : réduites à 1600 px de large, et les planches d'étiquettes et de cartes coupées sous la dernière rangée.

Le script cherche Chrome ou Chromium sur la machine, y compris celui que Playwright met en cache ; à défaut, indiquer son chemin dans `CHROME`. Deux écrans sortent sur Internet et le disent dans le script : le catalogage interroge la BnF, et les couvertures sont cherchées par le serveur. Hors ligne, ils s'affichent quand même, sans notice ni vignette.

## Applications de bureau

Trois scripts construisent un Bibli pour **un seul ordinateur** : le serveur tourne en arrière-plan sur `http://localhost:8765/` (ou le port de `BIBLI_PORT`) et s'ouvre dans une fenêtre à lui, sans onglets ni barre d'adresse. Il n'écoute que `localhost` : aucune tablette ne l'atteint, et rien ne le relance après une coupure de courant. Ce n'est pas un déploiement pour plusieurs postes. Le mode d'emploi pour l'utilisateur est dans le [guide d'installation](installation.md).

À chaque version, le workflow de release construit les trois et les attache à la release (`Bibli-vX.Y.Z-macos.zip`, `Bibli-vX.Y.Z-windows.exe`, `Bibli-vX.Y.Z-linux.tar.gz`). Un échec de cette étape n'empêche pas la release, qui sort alors sans elles.

### macOS

    packaging/macos.sh [dossier]

Construit `Bibli.app`, `Bibli-macos.zip` et, sur un Mac, `Bibli.dmg` dans `dist/`. La fenêtre est Chrome, Edge, Brave ou Chromium en mode application, quel que soit le navigateur par défaut ; **quitter cette fenêtre (Cmd+Q) arrête le serveur**. Sans aucun de ces navigateurs, Bibli s'ouvre dans celui par défaut, et un second double-clic propose de l'arrêter. Le premier lancement demande le mot de passe. Les données vivent dans `~/Library/Application Support/Bibli`, à part de l'application, qu'une nouvelle version remplace sans rien perdre. Rien n'est signé par Apple. Il faut Go, `zip` et, hors d'un Mac, `python3`.

### Windows

    packaging/windows.sh [dossier]

Construit `Bibli-windows.exe` dans `dist/`, depuis un Mac ou un Linux : `bibli.exe` seul. Double-cliqué (ou lancé avec `install`), il lance les scripts embarqués de `packaging/windows/`, qui demandent ce que sera ce PC : l'application de bureau (`%LOCALAPPDATA%\Programs\Bibli`, sans droits d'administrateur, **fermer sa fenêtre arrête le serveur**), le [service Windows](deployment.md#windows-service) ou un raccourci vers un serveur. Lancé avec des options, il est le serveur, comme ailleurs. Rien n'est signé. Il faut Go.

Les scripts sont en UTF-8 et LF dans le dépôt ; `bibli.exe` leur ajoute le BOM et les CRLF qu'attend Windows PowerShell 5. La CI teste le cycle du service sur Windows ; l'application, l'icône de notification et le raccourci se vérifient à la main.

### Linux

    packaging/linux.sh [dossier]

Construit `Bibli-linux.tar.gz` dans `dist/`, depuis n'importe quel système. L'archive contient le serveur pour PC (amd64) et pour ARM 64 bits (arm64, un Raspberry Pi récent par exemple) ; `bibli.sh` choisit le bon. Le premier lancement se fait depuis un terminal (`./bibli.sh`) : il demande le mot de passe et ajoute Bibli au menu des applications. La fenêtre est Chrome, Edge, Brave ou Chromium en mode application, y compris le Chromium en snap d'Ubuntu ; **fermer cette fenêtre arrête le serveur**. Les dialogues passent par `zenity` (GNOME) ou `kdialog` (KDE), à défaut par le terminal. Les données vivent dans `~/.local/share/bibli`. Il faut Go et `tar`.

### Icônes

Les icônes sont dessinées depuis `app/static/favicon.svg` par `packaging/icons.sh` (Chrome requis, chemin dans `CHROME` au besoin) : celles des applications de bureau dans `packaging/icons/`, et celles de l'écran d'accueil des téléphones (« Ajouter à l'écran d'accueil ») dans `app/static/` (`icon-192`, `icon-512`, `apple-touch-icon.png`). À relancer sur un Mac quand le logo change, puis à committer : c'est ce qui permet de construire les applications sans navigateur, sur le runner comme ailleurs.

## Structure

Tout le code Go vit dans `app/`, en **un seul paquet plat** : les fichiers y sont côte à côte, un par écran ou par sujet, sans sous-dossier ni couche. Les gabarits, le CSS, le JS, les migrations et les traductions l'accompagnent, parce que `go:embed` ne sait pas remonter d'un cran : ils sont embarqués dans le binaire, jamais lus sur disque. La racine ne garde que ce qui n'est pas du code : `go.mod`, l'outillage et la documentation.

    app/main.go          configuration, routes, chargement des gabarits, arrêt propre
    app/db.go            ouverture SQLite, migrations embarquées
    app/auth.go          session bibliothécaire, en-têtes de sécurité, anti-brute-force
    app/handlers.go      rendu (pages, fragments HTMX) et écrans de consultation
    app/i18n.go          catalogue de traduction, langue de la requête
    app/loan.go          prêt et retour, le cœur du produit
    app/catalogue.go     écran de catalogage, création d'exemplaires
    app/enrichment.go    interrogation des catalogues (BnF, UniCat, Google, Open Library)
    app/marc.go          lecture de l'UNIMARC et du MARC21 qu'ils renvoient
    app/cover.go         vignettes de couverture, servies par le binaire
    app/book.go          fiche d'un ouvrage
    app/inventory.go     inventaire éditable
    app/labels.go        étiquettes : cote, filtres, planche à imprimer
    app/borrowers.go     emprunteurs : saisie, import CSV, cartes, changement de groupe
    app/tracking.go      page de suivi (jeton secret)
    app/reports.go       impressions et export CSV
    app/xlsx.go          export Excel, sans dépendance
    app/stats.go         lectures des prêts (fiches, /stats) ; heatmap.go et timeline.go les dessinent
    app/review.go        bilan annuel de la collection, téléchargé depuis /stats
    app/codes.go         codes internes des cartes et des étiquettes (VOL…, LEC…)
    app/desklookup.go    « recherche en cours » au comptoir, catalogue par catalogue
    app/pager.go         pagination de l'inventaire et des emprunteurs
    app/maintenance.go   sauvegardes et anonymisation RGPD
    app/settings.go      écran de réglages
    app/themes.go        thèmes de couleurs (le détail est dans app.css)
    app/dates.go         dates « pour humains », formats par langue
    app/isbn.go          conversion et validation ISBN-10/13
    app/params.go        réglages globaux gardés en mémoire (nom de la bibliothèque, langue, début de l'année des statistiques…)
    app/version.go       métadonnées de build, lien vers le code source
    app/demo.go          mode démonstration : remise à zéro périodique
    app/demo.sql         jeu de démonstration, embarqué dans le binaire
    app/migrations/      001_initial.sql (le schéma), schema.dbml (le même en DBML, dbdiagram.io)
    app/locales/         fr.json, en.json, nl.json : tout le texte affiché
    app/templates/       HTML rendu côté serveur
    app/static/          CSS, JS et bibliothèques vendorées (vendor/README.md), aucun CDN
    app/testdata/        fixture.sql, le petit jeu de données des tests
    scripts/dev.sh              serveur de développement qui se recompile tout seul
    scripts/screenshots.mjs     captures de tous les écrans, sur une base jetable
    scripts/gen-demo-loans.py   prêts et groupes du jeu de démonstration
    packaging/build-info.sh     écrit app/build-info.json, la version que le binaire annonce
    packaging/macos.sh          application de bureau Mac
    packaging/windows.sh        Windows : application, serveur ou raccourci, un seul .exe
    packaging/windows/          installateur Windows et ses icônes, embarqués dans bibli.exe
    packaging/linux.sh          application de bureau Linux
    packaging/icons.sh          icônes de bureau (packaging/icons/, packaging/windows/) et web (app/static/)
    dist/                       tout ce qui est construit (make build, make dist), jamais versionné
    Makefile             raccourcis de développement (make help)

## Ajouter une migration

Créer `app/migrations/00N_description.sql` en continuant la numérotation. **Ne jamais modifier une migration déjà appliquée sur une instance qui contient des données** : en écrire une nouvelle. Garder `schema.dbml` en accord (un test le vérifie).

`001_initial.sql` a été édité en place jusqu'à la v1, tant qu'aucune instance ne détenait de données réelles : une instance antérieure à la v1 n'est donc pas garantie migrable et se recrée depuis zéro. À partir de la v1, `001_initial.sql` est figé et tout changement de schéma passe par une nouvelle migration.

## Publier une version

Le workflow **release** calcule la version à partir des commits conventionnels (`feat` → mineure, `fix` et les autres types → patch, `!` ou `BREAKING CHANGE` → majeure), crée le tag, construit l'image `:stable` et les applications de bureau, et rédige le changelog. Aucun numéro de version n'est écrit à la main.

Le pied de page affiche la version ; la page **À propos** donne le commit, la date de build et le lien vers le code source.
