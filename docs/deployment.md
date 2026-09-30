# Déployer un serveur Bibli

Ce guide s'adresse à la personne qui installe Bibli **pour plusieurs appareils** : les postes de la bibliothèque, les tablettes, ou un accès depuis la maison pour les familles. Pour un seul ordinateur, l'[application de bureau](installation.md) suffit et ne demande rien de tout ceci.

## Sommaire

1. [Quelle installation choisir ?](#quelle-installation-choisir-)
2. [Prérequis](#prérequis)
3. [Configuration](#configuration)
4. [HTTPS ou réseau local : choisir le bon mode](#https-ou-réseau-local--choisir-le-bon-mode)
5. [Binaire + systemd](#binaire--systemd)
6. [Docker](#docker)
7. [Reverse proxy](#reverse-proxy)
8. [Sauvegarde et restauration](#sauvegarde-et-restauration)
9. [Capacité](#capacité)
10. [Bon à savoir](#bon-à-savoir)
11. [Instance de démonstration](#instance-de-démonstration)

## Quelle installation choisir ?

![Quelle installation choisir : application de bureau, serveur HTTP sur le réseau local, serveur HTTPS sur le réseau local, serveur sur Internet](img/which-setup.svg)

- **Un seul ordinateur** : l'application de bureau, avec le [guide d'installation](installation.md). Rien d'autre ne l'atteint.
- **Plusieurs appareils, douchettes ou ISBN tapé** : un serveur sur le réseau local, en HTTP ([binaire + systemd](#binaire--systemd) ou [Docker](#docker)), avec `-secure-cookies=false`.
- **Plusieurs appareils, caméra des tablettes** : le même serveur derrière un [reverse proxy](#reverse-proxy) HTTPS. Sur un réseau fermé, le plus simple est un vrai nom de domaine avec un certificat obtenu par défi DNS (DNS-01), qu'aucun appareil n'a besoin d'approuver.
- **Accès depuis la maison** (liens de suivi pour les familles) : un serveur sur Internet, derrière un reverse proxy HTTPS.

Dans les quatre cas, Bibli reste un seul programme et un seul fichier de base de données SQLite, avec ses sauvegardes automatiques. Le schéma se modifie dans draw.io : `docs/img/which-setup.svg` contient le diagramme éditable.

## Prérequis

| Pour | Il faut |
|---|---|
| Utiliser | Un navigateur récent. Une douchette USB au comptoir (25–40 €, elle se comporte comme un clavier). |
| Héberger | Une machine allumée en permanence : mini-PC, Raspberry Pi 4/5 ou serveur Linux. Aucune base de données ni serveur d'applications à installer. |
| Compiler | Go ≥ 1.27, sur n'importe quel système. |
| Déployer avec Docker | Docker et le plugin `compose`, sur une machine **amd64** : l'image publiée n'existe pas encore pour ARM. Sur un Raspberry Pi, utiliser le [binaire](#binaire--systemd). |

## Configuration

### Variables d'environnement

| Variable | Rôle |
|---|---|
| `BIBLI_ADMIN_PASSWORD` | **Obligatoire.** Le mot de passe bibliothécaire, unique pour toute l'école. Sans lui, Bibli refuse de démarrer, et sur une instance accessible par le réseau il exige **au moins 12 caractères** (une phrase de trois ou quatre mots suffit ; l'application de bureau et la démonstration en sont dispensées). Une session dure 12 heures sans utilisation, et jamais plus de 7 jours après la connexion ; changer le mot de passe déconnecte tout le monde. |
| `BIBLI_GOOGLE_BOOKS_KEY` | Optionnelle. Clé API Google Books : relève le quota d'enrichissement et évite les erreurs 429 quand on catalogue beaucoup de livres d'affilée. Elle peut aussi être collée dans **Réglages** (« Clé Google Books ») ; si la variable est définie, elle l'emporte et le champ est désactivé. |
| `BIBLI_DEMO_RESET` | **Réservée à une instance de démonstration publique**, jamais à une école : voir [Instance de démonstration](#instance-de-démonstration). |
| `BIBLI_LOG_COLOR` | Optionnelle. `1` colore le journal, seulement dans un terminal (utile en développement). |

### Options de ligne de commande

| Option | Défaut | Rôle |
|---|---|---|
| `-db` | `biblio.db` | Chemin du fichier SQLite. Il est créé au premier démarrage. |
| `-addr` | `:8080` | Adresse d'écoute. |
| `-backup-dir` | `backups` | Dossier des sauvegardes automatiques. Vide (`""`) pour les désactiver. |
| `-cache-dir` | `cache` | Dossier des vignettes de couverture, un fichier par ISBN. Vide (`""`) pour le désactiver. |
| `-secure-cookies` | `true` | Cookies de session en `Secure`. **À mettre à `false` pour un accès en HTTP** (réseau local, développement). |
| `-trust-proxy` | `false` | Lire `X-Forwarded-For`. **À activer derrière un reverse proxy**, jamais sans. |
| `-family-links` | `true` | Proposer les liens de suivi pour les familles. `false` sur une installation qu'aucune famille ne peut joindre (les applications de bureau le passent). |

Les migrations du schéma s'appliquent seules au démarrage.

## HTTPS ou réseau local : choisir le bon mode

Un navigateur **ignore sans rien dire** un cookie `Secure` reçu en HTTP : le mot de passe est accepté, puis l'écran de connexion revient, en boucle. Bibli détecte ce cas et l'explique à l'écran, mais il faut choisir :

| Situation | Options |
|---|---|
| Exposé sur Internet, derrière un reverse proxy TLS | `-trust-proxy` |
| Réseau local de l'école, accès en `http://IP:8080` | `-secure-cookies=false` |

`-trust-proxy` permet de distinguer les clients : sans lui, derrière un proxy, toutes les requêtes semblent venir de la même adresse, et cinq mots de passe erronés bloquent la connexion pour toute l'école pendant une minute. Ne l'activez **que** s'il y a réellement un proxy devant, sinon l'en-tête est falsifiable et le comptage ne vaut plus rien. Bibli retient la **dernière** adresse de `X-Forwarded-For`, celle qu'ajoute le proxy : il doit donc la renseigner (Caddy, Traefik et nginx avec `$proxy_add_x_forwarded_for` le font), et Bibli ne doit être joignable **que** par lui, en écoutant sur `127.0.0.1`.

La **caméra** des tablettes n'est disponible qu'en HTTPS ou sur `localhost` : c'est une exigence des navigateurs, pas un choix de Bibli. Les douchettes, elles, fonctionnent partout.

## Binaire + systemd

La solution la plus simple pour une école : un seul fichier exécutable, relancé par le système.

Compiler pour la machine cible (binaire statique, sans dépendance) :

    GOOS=linux GOARCH=amd64 go build -o biblio ./app   # mini-PC Linux
    GOOS=linux GOARCH=arm64 go build -o biblio ./app   # Raspberry Pi 4/5 (64 bits)

Copier `biblio` dans `/opt/bibli/` sur la machine. Le mot de passe ne va **pas** dans l'unité systemd (lisible par tous) mais dans un fichier réservé à root :

    sudo useradd --system --home /var/lib/bibli biblio
    sudo install -d -m 700 /etc/bibli
    printf 'BIBLI_ADMIN_PASSWORD=%s\n' 'un-mot-de-passe-fort' | sudo tee /etc/bibli/env >/dev/null
    sudo chmod 600 /etc/bibli/env
    sudo install -d -o biblio -g biblio /var/lib/bibli

`/etc/systemd/system/biblio.service` :

    [Unit]
    Description=Bibli
    After=network.target

    [Service]
    # -secure-cookies=false : accès en HTTP sur le réseau local de l'école.
    # Derrière un reverse proxy HTTPS, retirer cette option, ajouter -trust-proxy
    # et écouter sur 127.0.0.1:8080.
    ExecStart=/opt/bibli/biblio -db /var/lib/bibli/biblio.db -addr :8080 \
              -backup-dir /var/lib/bibli/backups -cache-dir /var/lib/bibli/cache \
              -secure-cookies=false
    WorkingDirectory=/opt/bibli
    EnvironmentFile=/etc/bibli/env
    Restart=always
    RestartSec=5
    User=biblio
    ProtectSystem=strict
    ProtectHome=yes
    PrivateTmp=yes
    NoNewPrivileges=yes
    ReadWritePaths=/var/lib/bibli

    [Install]
    WantedBy=multi-user.target

Puis :

    sudo systemctl enable --now biblio

`Restart=always` relance Bibli après une coupure de courant. Réserver une **IP fixe** dans le routeur : en DHCP, l'adresse changera un jour et plus aucun poste ne trouvera Bibli.

Mettre à jour : remplacer `/opt/bibli/biblio` par la nouvelle version, puis `sudo systemctl restart biblio`.

## Docker

    cp .env.example .env        # y définir BIBLI_ADMIN_PASSWORD
    docker compose up -d

Le `docker-compose.yml` fourni est prévu pour tourner **derrière un reverse proxy** : Bibli n'écoute que sur `127.0.0.1:8087` et lit `X-Forwarded-For` (`-trust-proxy`). Le proxy s'adresse donc à `127.0.0.1:8087` (voir [Reverse proxy](#reverse-proxy)).

**Sans proxy, en HTTP sur le réseau local**, modifier trois lignes du fichier :

- sous `ports:`, remplacer `"127.0.0.1:8087:8080"` par `"8080:8080"` ;
- sous `command:`, retirer `- "-trust-proxy"` ;
- sous `command:`, ajouter `- "-secure-cookies=false"`.

Bibli répond alors sur `http://IP-de-la-machine:8080`.

La base vit dans `./data` sur l'hôte, avec les sauvegardes (`./data/backups`) et les couvertures (`./data/cache`). **Ne jamais** placer ce dossier sur un partage réseau (NFS/CIFS) : le verrouillage SQLite y est cassé et la base se corrompt.

Le compose suit le tag **`:stable`**, publié à chaque version. La CI publie aussi `:<sha>` et `:latest` à chaque changement de `main` : ces tags servent aux essais, pas à une école en production.

Mettre à jour :

    docker compose pull && docker compose up -d

Le conteneur expose une sonde de santé qui interroge la base : `docker compose ps` dit si Bibli est réellement opérationnel.

## Reverse proxy

Bibli parle HTTP et ne gère aucun certificat. Exemple avec Caddy, qui obtient le certificat tout seul :

    bibli.example.be {
        reverse_proxy 127.0.0.1:8080
    }

Ajouter `-trust-proxy` aux options de Bibli, et `-addr 127.0.0.1:8080` pour qu'il ne soit joignable que par le proxy (avec le compose fourni, viser `127.0.0.1:8087`, déjà configuré ainsi).

Le proxy doit transmettre l'en-tête `Host` d'origine. Bibli refuse (403) toute action envoyée depuis un autre site ; un navigateur ancien (Safari avant 16.4, vieilles tablettes) est jugé en comparant `Origin` à `Host`, et un `Host` réécrit lui ferait refuser les actions légitimes. Caddy et Traefik le transmettent d'office ; nginx non, il faut ajouter `proxy_set_header Host $host;`.

Le proxy peut aussi envoyer l'en-tête `Strict-Transport-Security` (HSTS), que Bibli n'envoie pas lui-même puisqu'il ne voit que du HTTP.

## Sauvegarde et restauration

Bibli fait **lui-même** ses sauvegardes dans `-backup-dir`, avec `VACUUM INTO`, la méthode sûre pendant que le service tourne.

La rotation est de type *grand-père, père, fils* : **7 fichiers quotidiens** (un par jour de la semaine), **4 hebdomadaires** et **4 mensuels**, soit toujours les mêmes 15 noms. Chaque écriture remplace la plus ancienne de son niveau : il n'y a rien à purger, et le dossier ne grossit pas.

Bibli vérifie toutes les heures ce qui manque, d'après la date des fichiers : la sauvegarde du jour, une copie hebdomadaire si la dernière a 7 jours, une mensuelle si le mois n'en a pas encore. Une machine éteinte ou en veille rattrape donc son retard au réveil.

L'écran **Réglages** affiche la date et le fichier de la dernière sauvegarde, ou l'erreur rencontrée, et permet de télécharger chacune. **Prévoir en plus une copie hors site** (clé USB, autre machine) : les sauvegardes automatiques sont sur le même disque que la base.

Sauvegarde manuelle ponctuelle, sans arrêter le service :

    sqlite3 /var/lib/bibli/biblio.db ".backup '/media/usb/biblio-$(date +%F).db'"

Restauration : arrêter le service, remplacer le fichier de base par la sauvegarde, redémarrer.

    sudo systemctl stop biblio
    sudo cp /media/usb/biblio-2026-09-12.db /var/lib/bibli/biblio.db
    sudo chown biblio:biblio /var/lib/bibli/biblio.db
    sudo systemctl start biblio

**Ne jamais copier le `.db` à la main pendant que le service tourne** : la copie serait incohérente. Passer par `VACUUM INTO`, `.backup` ou le téléchargement depuis Réglages.

Une tâche quotidienne **anonymise** par ailleurs les prêts rendus et les emprunteurs sortis au-delà de la durée de conservation réglée dans Réglages (3 ans par défaut).

## Capacité

Bibli est **testé jusqu'à 50 000 exemplaires et 2 500 élèves**, avec cinq ans de prêts (plus de 400 000), soit bien au-delà d'une école primaire. Mesures sur une base de cette taille (184 Mo), servie par un ordinateur de bureau récent :

| Écran ou tâche | Temps |
|---|---|
| Prêt, retour, recherche d'un livre ou d'un élève au comptoir | moins d'une milliseconde |
| Accueil, prêts en cours, inventaire, emprunteurs, fiche d'un livre | moins de 0,2 s |
| Statistiques | 0,3 s |
| Export CSV ou Excel de toute la collection | 0,2 à 0,3 s |
| Bilan annuel de la collection | 1,4 s |
| Sauvegarde quotidienne | moins d'une seconde |

Sur un Raspberry Pi, compter quelques fois plus lent : les écrans du quotidien restent instantanés.

Les seules limites fixes sont celles des codes imprimés sur les étiquettes et les cartes, tirés au hasard :

- **Exemplaires** : `VOL` suivi de 5 chiffres et d'un chiffre de contrôle, soit 100 000 codes possibles. Au-delà d'environ 60 000 exemplaires, un nouveau code pourrait ne pas être trouvé.
- **Cartes** : `LEC` suivi de 4 chiffres et d'un chiffre de contrôle, soit 10 000 codes possibles. Un élève parti garde sa carte jusqu'à son anonymisation : avec la conservation par défaut (3 ans), une école de 2 500 élèves en occupe moins de 4 000. Avec la conservation maximale (10 ans), une école de cette taille approche la limite.
- **Import d'élèves** : 2 000 lignes par fichier ; au-delà, importer en deux fois.

## Bon à savoir

- **Catalogues interrogés** : BnF, UniCat, Google Books et Open Library, à partir de l'ISBN seul. Rien d'autre ne quitte le serveur.
- **Couvertures** : récupérées par le serveur (Open Library, puis BnF) et servies par lui, jamais par le navigateur. Elles sont conservées dans `-cache-dir`, un fichier par ISBN, absences comprises. Rien n'est écrit en base : le dossier peut être vidé à la main.
- **Langue** : un réglage pour toute l'instance (français, anglais, néerlandais). `?lang=en` ou `?lang=nl` sur n'importe quelle adresse la change pour un seul navigateur pendant douze heures, `?lang=auto` revient au réglage.
- **Liens de suivi** : activés par défaut ; `-family-links=false` les retire là où aucune famille ne peut joindre le serveur.
- **Mise à jour** : démarrer une version récente sur une base ancienne applique les migrations manquantes, sans retour en arrière possible. Une sauvegarde récente suffit à revenir en arrière.

## Instance de démonstration

Une instance publique où n'importe qui peut cliquer : prêter, cataloguer par ISBN, imprimer des étiquettes. Elle se remet elle-même en place, et rien de ce qu'un visiteur y fait ne survit. C'est ce qui tourne sur [bibli.tintamarre.be](https://bibli.tintamarre.be).

    docker compose -f docker-compose.demo.yml up -d

Tout tient dans une variable, une durée Go (`6h`, `90m`) :

    BIBLI_DEMO_RESET=6h

Au démarrage puis à chaque échéance, toute la collection est supprimée et `app/demo.sql` rechargé en une seule transaction, avec les réglages qu'un visiteur peut modifier. Les sauvegardes sont désactivées d'office, puisqu'il n'y a rien à conserver, et un bandeau prévient sur chaque écran. Le mot de passe reste obligatoire : il suffit de le publier avec le lien.

**Ne jamais mettre cette variable sur l'instance d'une école** : elle supprime la base à chaque échéance. C'est pourquoi c'est une variable d'environnement et non une option de ligne de commande : rien ne l'attrape en recopiant le `command:` donné aux écoles. Une valeur qui n'est pas une durée empêche Bibli de démarrer, plutôt que de laisser tourner une démonstration qui ne se réinitialise plus.
