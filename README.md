# Bibli

[![Version](https://img.shields.io/github/v/release/tintamarre/bibli?label=version&sort=semver)](https://github.com/tintamarre/bibli/releases)
[![CI](https://github.com/tintamarre/bibli/actions/workflows/ci.yml/badge.svg)](https://github.com/tintamarre/bibli/actions/workflows/ci.yml)
[![Licence AGPL-3.0](https://img.shields.io/badge/licence-AGPL--3.0-blue)](LICENSE)

**Le logiciel de bibliothèque le plus simple à installer et à faire tourner, pour une école, un centre culturel, une maison de repos ou une association.** Un seul programme et un seul fichier, sans compte, sans cloud et sans informaticien sur place. Prêter et rendre à la douchette, cataloguer un livre en scannant son ISBN, imprimer les étiquettes, suivre les retards, en ne gardant de chaque emprunteur que son prénom, l'initiale de son nom et son groupe.

**[Essayer la démonstration](https://bibli.tintamarre.be)** · mot de passe `demo` · une école fictive, remise à zéro régulièrement
· **[Voir la vidéo](https://youtu.be/oTzKXOJz5Lk)** ([NL](https://youtu.be/ZklZmrGEb3k), [EN](https://youtu.be/yNV7mwvRggw))

[![L'accueil de Bibli : emprunter, rendre, et les livres en rayon, en prêt et en retard](docs/img/home.png)](https://youtu.be/oTzKXOJz5Lk)

*English: Bibli is the simplest library software to install and run for a small organisation (school, cultural centre, care home), run by volunteers: one program, one file, no account, no cloud, and only a first name, a last-name initial and a group kept per borrower. The interface speaks French, Dutch and English; this documentation is in French.*

## Ce que fait Bibli

- **Prêt et retour en quelques secondes.** On scanne la carte de l'emprunteur, puis ses livres ; au retour, un seul scan suffit. Une douchette USB à 30 € fait l'affaire, la caméra d'une tablette aussi.
- **Catalogage par ISBN.** Le titre, l'auteur, l'éditeur et la couverture arrivent seuls, depuis la BnF, UniCat, Google Books et Open Library. Un livre sans ISBN se saisit à la main.
- **Étiquettes et cartes.** Planches A4 autocollantes, avec code-barres et cote de rangement ; les étiquettes d'une séance sortent dans l'ordre du catalogage.
- **Emprunteurs et groupes.** Import de la liste depuis un fichier CSV (exporté d'un tableur), cartes d'emprunteur, changement de groupe en un écran. Les emprunteurs sont rangés par groupe : une classe, un étage, une unité de soins, un atelier.
- **Retards, inventaire, statistiques.** Liste des retards à imprimer, inventaire modifiable ligne par ligne, lectures de l'année, exports CSV et Excel.
- **Liens de suivi**, si on le souhaite : chaque emprunteur, ou ses proches, voit ses livres en cours, rien d'autre.
- **En français, en néerlandais et en anglais.**

| | |
|---|---|
| ![L'écran de prêt : l'emprunteur, puis les livres scannés](docs/img/borrow.png) | ![Le catalogage : la notice trouvée depuis l'ISBN, avec sa couverture](docs/img/catalogue.png) |
| **Prêter** : la carte, puis les livres | **Cataloguer** : l'ISBN suffit |
| ![Une planche d'étiquettes : titre, code-barres et cote](docs/img/labels.png) | ![La liste des prêts en cours, par groupe et par emprunteur, avec la date de retour](docs/img/loans.png) |
| **Étiqueter** : planches A4 autocollantes | **Suivre** : les prêts, groupe par groupe |

## Pensé pour les petites structures

- **La vie privée d'abord.** Conçu au départ pour des enfants : un emprunteur, c'est un prénom, l'initiale du nom et un groupe, rien de plus. Seul l'ISBN d'un livre quitte le serveur. Les lectures sont anonymisées après la durée de conservation choisie, trois ans par défaut.
- **Rien à administrer.** Un seul programme et un seul fichier de base de données. Les sauvegardes se font seules, chaque jour, et se téléchargent depuis les réglages.
- **Aucun service extérieur.** Pas de compte à créer, pas de cloud, pas de CDN : tout est servi par Bibli. Prêter et rendre fonctionnent sans Internet.
- **Sobre.** Tourne sur un vieux PC, un mini-PC ou un Raspberry Pi, et s'utilise sans formation : grands boutons, écrans lisibles sur une tablette.
- **Pour les petites et moyennes collections.** Testé jusqu'à 50 000 exemplaires et 2 500 emprunteurs avec cinq ans de prêts : le comptoir répond en moins d'une milliseconde, chaque écran en moins d'une demi-seconde. Détails : [capacité](docs/deployment.md#capacité).

## Bibli ou un autre logiciel ?

Bibli ne cherche pas à remplacer un système intégré de bibliothèque (SIGB). Il fait moins, volontairement, pour que des bénévoles puissent l'installer et l'utiliser seuls.

| | **Bibli** | Koha, Evergreen | PMB, SLiMS | BiblioteQ | BiblioGenius | Libib |
|---|---|---|---|---|---|---|
| **Installation** | Un programme, un fichier | Serveur Linux et base de données à administrer | Serveur web, PHP et MySQL à administrer | Application de bureau | Application mobile et de bureau (iOS, macOS, Android) | Aucune, service en ligne |
| **Un poste ou tout l'établissement** | Les deux, avec le même programme | Serveur | Serveur | Un poste | Un appareil, partage entre proches | Navigateur ou appli |
| **Prêter et rendre sans Internet** | Oui | Oui, sur un serveur local | Oui, sur un serveur local | Oui | Oui | Non |
| **Données des emprunteurs** | Prénom, initiale, groupe | Fiche complète | Fiche complète | Fiche complète | Prêt entre proches, sans fiches d'emprunteurs | Hébergées chez l'éditeur |
| **Groupes, retards, changement de groupe** | Oui | Oui | Oui | Non | Non | En partie |
| **Catalogue public, réservations, acquisitions** | Non | Oui | Oui | En partie | Partage du catalogue entre proches | En partie |
| **Échange avec un réseau de bibliothèques** (MARC, Z39.50) | Non | Oui | Oui | Oui | Non précisé | Non |
| **Licence** | AGPL-3.0, libre | Libre | Libre | Libre | AGPL-3.0, libre | Propriétaire, gratuit jusqu'à 5 000 exemplaires |

**Choisissez Bibli** si votre collection tient dans une seule structure (jusqu'à quelques dizaines de milliers d'exemplaires), si personne ne peut administrer un serveur, et si la vie privée des emprunteurs compte.

**Choisissez plutôt BiblioGenius** pour une bibliothèque personnelle ou entre proches, à gérer depuis un téléphone.

**Choisissez plutôt un SIGB complet** (Koha, PMB, SLiMS) si vous avez besoin d'un catalogue public, de réservations, de périodiques ou de l'échange de notices avec un réseau de bibliothèques, et que quelqu'un peut l'administrer.

## Installer

| Pour | La solution | Le guide |
|---|---|---|
| **Un seul ordinateur** à la bibliothèque | L'application de bureau, Mac, Windows ou Linux : télécharger, double-cliquer. | [Installer sur un ordinateur](docs/installation.md) |
| **Plusieurs postes, des tablettes**, un accès depuis la maison | Un serveur sur le réseau local ou sur Internet : un binaire, ou Docker. | [Déployer un serveur](docs/deployment.md) |

Le choix en détail : [quelle installation choisir ?](docs/deployment.md#quelle-installation-choisir-)

## Documentation

- [**Guide d'utilisation**](docs/guide.md) : pour les bénévoles. Prêter, rendre, cataloguer, étiqueter, gérer les emprunteurs, passer d'une année à l'autre.
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
