# Sécurité

*English: please report vulnerabilities privately through GitHub (link below), never in a public issue. Reports in English are welcome.*

Bibli contient des données personnelles, parfois de mineurs (prénom, initiale, groupe, lectures). Une faille se signale donc **en privé**, jamais dans une issue publique.

## Signaler une faille

Utiliser le signalement privé de GitHub : onglet **Security** du dépôt, puis **Report a vulnerability**, ou directement [github.com/tintamarre/bibli/security/advisories/new](https://github.com/tintamarre/bibli/security/advisories/new).

Décrire, si possible :

- la version concernée (affichée sur la page **À propos**) ;
- ce qu'il faut faire pour reproduire le problème ;
- ce qu'un attaquant peut obtenir (lire, modifier, se connecter…), et s'il doit déjà être connecté.

Le projet est maintenu par des bénévoles : un premier retour arrive en général sous une semaine. Un correctif est publié dans une nouvelle version, et le signalement est rendu public une fois que les utilisateurs ont pu mettre à jour.

## Versions suivies

Seule la **dernière version publiée** reçoit les correctifs. Mettre à jour est la première chose à faire avant de signaler.

## Hors du périmètre

- Une installation exposée sur Internet sans HTTPS, ou avec un mot de passe faible : voir [HTTPS ou réseau local](docs/deployment.md#https-ou-réseau-local--choisir-le-bon-mode) dans [Déployer un serveur](docs/deployment.md).
- Les catalogues externes (BnF, UniCat, Google Books, Open Library) : Bibli ne leur envoie qu'un ISBN.
