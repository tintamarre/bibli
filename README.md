# Bibli

[![Version](https://img.shields.io/github/v/release/tintamarre/bibli?label=version&sort=semver)](https://github.com/tintamarre/bibli/releases)
[![CI](https://github.com/tintamarre/bibli/actions/workflows/ci.yml/badge.svg)](https://github.com/tintamarre/bibli/actions/workflows/ci.yml)
[![Licence AGPL-3.0](https://img.shields.io/badge/licence-AGPL--3.0-blue)](LICENSE)

**La bibliothèque de l'école, tenue par des bénévoles.** Prêter et rendre à la douchette, cataloguer un livre en scannant son ISBN, imprimer les étiquettes, suivre les retards. Sur un seul ordinateur ou pour toute l'école, sans informaticien sur place.

**[Essayer la démonstration](https://bibli.tintamarre.be)** · mot de passe `demo` · une école fictive, remise à zéro régulièrement
· **[Voir la vidéo](https://www.youtube.com/watch?v=Yo_IYGwo8sM)**

[![L'accueil de Bibli : emprunter, rendre, et les livres en rayon, en prêt et en retard](docs/img/home.png)](https://www.youtube.com/watch?v=Yo_IYGwo8sM)

*English: Bibli is a library app for schools (primary or secondary), run by volunteers. The interface speaks French, Dutch and English; this documentation is in French.*

## Ce que fait Bibli

- **Prêt et retour en quelques secondes.** On scanne la carte de l'élève, puis ses livres ; au retour, un seul scan suffit. Une douchette USB à 30 € fait l'affaire, la caméra d'une tablette aussi.
- **Catalogage par ISBN.** Le titre, l'auteur, l'éditeur et la couverture arrivent seuls, depuis la BnF, UniCat, Google Books et Open Library. Un livre sans ISBN se saisit à la main.
- **Étiquettes et cartes.** Planches A4 autocollantes, avec code-barres et cote de rangement ; les étiquettes d'une séance sortent dans l'ordre du catalogage.
- **Élèves et classes.** Import des classes depuis un fichier CSV (exporté d'un tableur), cartes d'emprunteur, passage d'année en un écran.
- **Retards, inventaire, statistiques.** Liste des retards à imprimer, inventaire modifiable ligne par ligne, lectures de l'année, exports CSV et Excel.
- **Liens de suivi pour les familles**, si l'école le souhaite : chaque famille voit les livres de son enfant, rien d'autre.
- **En français, en néerlandais et en anglais.**

| | |
|---|---|
| ![L'écran de prêt : l'emprunteur, puis les livres scannés](docs/img/borrow.png) | ![Le catalogage : la notice trouvée depuis l'ISBN, avec sa couverture](docs/img/catalogue.png) |
| **Prêter** : la carte, puis les livres | **Cataloguer** : l'ISBN suffit |
| ![Une planche d'étiquettes : titre, code-barres et cote](docs/img/labels.png) | ![La liste des prêts en cours, par classe et par élève, avec la date de retour](docs/img/loans.png) |
| **Étiqueter** : planches A4 autocollantes | **Suivre** : les prêts, classe par classe |

## Pensé pour une école

- **La vie privée des enfants d'abord.** Un élève, c'est un prénom, l'initiale du nom et une classe, rien de plus. Seul l'ISBN d'un livre quitte le serveur. Les lectures sont anonymisées après la durée de conservation choisie, trois ans par défaut.
- **Rien à administrer.** Un seul programme et un seul fichier de base de données. Les sauvegardes se font seules, chaque jour, et se téléchargent depuis les réglages.
- **Aucun service extérieur.** Pas de compte à créer, pas de cloud, pas de CDN : tout est servi par Bibli. Prêter et rendre fonctionnent sans Internet.
- **Sobre.** Tourne sur un vieux PC, un mini-PC ou un Raspberry Pi, et s'utilise sans formation : grands boutons, écrans lisibles sur une tablette.
- **À la taille d'une grande école.** Testé jusqu'à 50 000 exemplaires et 2 500 élèves avec cinq ans de prêts : le comptoir répond en moins d'une milliseconde, chaque écran en moins d'une demi-seconde. Détails : [capacité](docs/deployment.md#capacité).

## Installer

| Pour | La solution | Le guide |
|---|---|---|
| **Un seul ordinateur** à la bibliothèque | L'application de bureau, Mac, Windows ou Linux : télécharger, double-cliquer. | [Installer sur un ordinateur](docs/installation.md) |
| **Plusieurs postes, des tablettes**, un accès depuis la maison | Un serveur sur le réseau de l'école ou sur Internet : un binaire, ou Docker. | [Déployer un serveur](docs/deployment.md) |

Le choix en détail : [quelle installation choisir ?](docs/deployment.md#quelle-installation-choisir-)

## Documentation

- [**Guide d'utilisation**](docs/guide.md) : pour les bénévoles. Prêter, rendre, cataloguer, étiqueter, gérer les élèves, passer d'une année à l'autre.
- [**Installer sur un ordinateur**](docs/installation.md) : l'application de bureau, pas à pas.
- [**Déployer un serveur**](docs/deployment.md) : systemd, Docker, HTTPS, sauvegardes.
- [**Développer**](docs/development.md) : lancer le code, le jeu de démonstration, les captures, publier une version.
- [**Contribuer**](CONTRIBUTING.md) et [**signaler une faille**](SECURITY.md).

## Essayer depuis le code

    git clone https://github.com/tintamarre/bibli.git
    cd bibli
    make demo    # crée la base et y charge l'école de démonstration
    make dev     # http://127.0.0.1:8080, mot de passe « dev »

Il faut Go ≥ 1.27 et `sqlite3`. Le détail est dans [Développer](docs/development.md).

## Licence

[GNU AGPL-3.0](LICENSE) : l'application peut être reprise et modifiée librement, mais toute version mise en service doit rester ouverte.

Les bibliothèques incluses dans `app/static/vendor/` gardent leur propre licence : htmx (0BSD), ZXing (Apache-2.0), JsBarcode (MIT) et la police Atkinson Hyperlegible (SIL OFL 1.1). Versions, sources et textes de licence : [`app/static/vendor/README.md`](app/static/vendor/README.md).
