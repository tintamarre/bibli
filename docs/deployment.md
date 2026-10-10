# Déployer un serveur Bibli

Ce guide s'adresse à la personne qui installe Bibli **pour plusieurs appareils** : les postes de la bibliothèque, les tablettes, ou un accès depuis la maison. Pour un seul ordinateur, l'[application de bureau](installation.md) suffit et ne demande rien de tout ceci.

## Sommaire

1. [Quelle installation choisir ?](#quelle-installation-choisir-)
2. [Prérequis](#prérequis)
3. [Configuration](#configuration)
4. [HTTPS ou réseau local : choisir le bon mode](#https-ou-réseau-local--choisir-le-bon-mode)
5. [Binaire + systemd](#binaire--systemd)
6. [Windows (service)](#windows-service)
7. [Docker](#docker)
8. [Reverse proxy](#reverse-proxy)
9. [Sauvegarde et restauration](#sauvegarde-et-restauration)
10. [Capacité](#capacité)
11. [Bon à savoir](#bon-à-savoir)
12. [Instance de démonstration](#instance-de-démonstration)

## Quelle installation choisir ?

![Quelle installation choisir : application de bureau, serveur HTTP sur le réseau local, serveur HTTPS sur le réseau local, serveur sur Internet](img/which-setup.svg)

- **Un seul ordinateur** : l'application de bureau, avec le [guide d'installation](installation.md). Rien d'autre ne l'atteint.
- **Plusieurs appareils, douchettes ou ISBN tapé** : un serveur sur le réseau local, en HTTP ([binaire + systemd](#binaire--systemd) sous Linux, [Windows (service)](#windows-service) sous Windows, ou [Docker](#docker)), avec `-secure-cookies=false`.
- **Plusieurs appareils, caméra des tablettes** : le même serveur derrière un [reverse proxy](#reverse-proxy) HTTPS. Sur un réseau fermé, le plus simple est un vrai nom de domaine avec un certificat obtenu par défi DNS (DNS-01), qu'aucun appareil n'a besoin d'approuver.
- **Accès depuis la maison** (liens de suivi) : un serveur sur Internet, derrière un reverse proxy HTTPS.

Dans les quatre cas, Bibli reste un seul programme et un seul fichier de base de données SQLite, avec ses sauvegardes automatiques. Le schéma se modifie dans draw.io : `docs/img/which-setup.svg` contient le diagramme éditable.

## Prérequis

| Pour | Il faut |
|---|---|
| Utiliser | Un navigateur récent. Une douchette USB au comptoir (25–40 €, elle se comporte comme un clavier). |
| Héberger | Une machine allumée en permanence : mini-PC, Raspberry Pi 4/5, serveur Linux, ou PC **Windows 10 ou 11** ([Windows (service)](#windows-service)). Aucune base de données ni serveur d'applications à installer. |
| Compiler | Go ≥ 1.27, sur n'importe quel système. |
| Déployer avec Docker | Docker et le plugin `compose`, sur une machine amd64 ou arm64 (Raspberry Pi 4/5 avec un système 64 bits). |

## Configuration

### Variables d'environnement

| Variable | Rôle |
|---|---|
| `BIBLI_ADMIN_PASSWORD` | **Obligatoire.** Le mot de passe bibliothécaire, unique pour tout l'établissement. Sans lui (ou sans `-password-file`), Bibli refuse de démarrer, et sur une instance accessible par le réseau il exige **au moins 12 caractères** (une phrase de trois ou quatre mots suffit ; l'application de bureau et la démonstration en sont dispensées). Une session dure 12 heures sans utilisation, et jamais plus de 7 jours après la connexion ; changer le mot de passe déconnecte tout le monde. |
| `BIBLI_GOOGLE_BOOKS_KEY` | Optionnelle. Clé API Google Books : relève le quota d'enrichissement et évite les erreurs 429 quand on catalogue beaucoup de livres d'affilée. Elle peut aussi être collée dans **Réglages** (« Clé Google Books ») ; si la variable est définie, elle l'emporte et le champ est désactivé. |
| `BIBLI_DEMO_RESET` | **Réservée à une instance de démonstration publique**, jamais à une vraie installation : voir [Instance de démonstration](#instance-de-démonstration). |
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
| `-tracking-links` | `true` | Proposer les liens de suivi. `false` sur une installation que personne d'autre que l'utilisateur ne peut joindre (les applications de bureau le passent). |
| `-log-file` | vide | Écrire le journal dans ce fichier (à la suite, renouvelé à 5 Mo, l'ancien gardé en `.1`) plutôt que sur la sortie d'erreur. Le service Windows le passe. |
| `-password-file` | vide | Lire le mot de passe bibliothécaire dans ce fichier plutôt que dans `BIBLI_ADMIN_PASSWORD` (tout le fichier, sans le saut de ligne final). Le service Windows le passe. |

Les migrations du schéma s'appliquent seules au démarrage.

## HTTPS ou réseau local : choisir le bon mode

Un navigateur **ignore sans rien dire** un cookie `Secure` reçu en HTTP : le mot de passe est accepté, puis l'écran de connexion revient, en boucle. Bibli détecte ce cas et l'explique à l'écran, mais il faut choisir :

| Situation | Options |
|---|---|
| Exposé sur Internet, derrière un reverse proxy TLS | `-trust-proxy` |
| Réseau local, accès en `http://IP:8080` | `-secure-cookies=false` |

`-trust-proxy` permet de distinguer les clients : sans lui, derrière un proxy, toutes les requêtes semblent venir de la même adresse, et cinq mots de passe erronés bloquent la connexion pour tout le monde pendant une minute. Ne l'activez **que** s'il y a réellement un proxy devant, sinon l'en-tête est falsifiable et le comptage ne vaut plus rien. Bibli retient la **dernière** adresse de `X-Forwarded-For`, celle qu'ajoute le proxy : il doit donc la renseigner (Caddy, Traefik et nginx avec `$proxy_add_x_forwarded_for` le font), et Bibli ne doit être joignable **que** par lui, en écoutant sur `127.0.0.1`.

La **caméra** des tablettes n'est disponible qu'en HTTPS ou sur `localhost` : c'est une exigence des navigateurs, pas un choix de Bibli. Les douchettes, elles, fonctionnent partout.

## Binaire + systemd

La solution la plus simple : un seul fichier exécutable, relancé par le système.

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
    # -secure-cookies=false : accès en HTTP sur le réseau local.
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

## Windows (service)

Sur un PC Windows allumé en permanence, Bibli tourne en **service Windows** : il démarre tout seul à l'allumage, redémarre après un plantage, et sert toutes les tablettes et tous les postes du réseau, sans que personne ait à ouvrir une fenêtre. C'est l'équivalent Windows de [binaire + systemd](#binaire--systemd), sans rien à compiler, et sans second programme à installer : `bibli.exe` est lui-même le service. Arrêter le service ou éteindre le PC ferme la base proprement.

Télécharger `Bibli-vX.Y.Z-windows.exe` depuis la [page de la dernière version](https://github.com/tintamarre/bibli/releases/latest) et double-cliquer dessus (SmartScreen peut prévenir : *Informations complémentaires → Exécuter quand même*). La fenêtre d'accueil (version, licence, liens vers la documentation et les nouveautés) demande comment l'utiliser sur ce PC : choisir **Serveur pour plusieurs appareils**. Windows demande l'autorisation d'administrateur, puis Bibli le mot de passe de la bibliothèque (au moins 12 caractères). C'est tout. Sans compte administrateur, rien n'est installé et Bibli explique quoi faire : clic droit sur le `.exe` téléchargé → **Exécuter en tant qu'administrateur**, avec le mot de passe d'un compte administrateur du PC. C'est le même fichier que l'[application de bureau](installation.md) : seul ce choix diffère.

**Il faut** un PC **Windows 10 ou 11** (Famille, Professionnel ou Éducation), de préférence un **PC de bureau** qui reste branché. Windows 7, 8 et 8.1 ne conviennent pas : Bibli n'y démarre pas. Le mode S de Windows non plus, il n'accepte que les applications du Microsoft Store. Sur un PC à processeur ARM, il faut Windows 11.

L'installateur :

- copie `bibli.exe` dans `C:\Program Files\Bibli` et garde la base dans `C:\ProgramData\Bibli` (base, sauvegardes, couvertures, journaux) : une mise à jour remplace le programme sans toucher aux données ;
- enregistre le service **Bibli** (visible dans `services.msc`), lancé au démarrage sous le compte restreint `SERVICE LOCAL` (un serveur ouvert au réseau n'a pas besoin de plus de droits), que Windows relance s'il plante ;
- écrit le mot de passe dans un fichier que seuls le serveur, `SYSTEM` et les administrateurs peuvent lire, jamais sur la ligne de commande ;
- empêche le PC de se mettre en veille quand il est sur secteur : un PC en veille ne répond plus aux tablettes ;
- ouvre le port **8080** en entrée, sur les profils de pare-feu *Privé* et *Domaine* seulement ;
- pose une icône **Bibli** sur le Bureau (elle ouvre `http://localhost:8080` sur ce PC), une **icône dans la zone de notification** (près de l'horloge, à chaque ouverture de session) et un dossier **Bibli (serveur)** dans le menu Démarrer : démarrer, arrêter, redémarrer, démarrage automatique, état, voir le journal, adresse réseau, mettre à jour, désinstaller ;
- inscrit **Bibli (serveur)** dans *Paramètres → Applications*, d'où il se désinstalle comme tout programme.

**Démarrer, arrêter, redémarrer** : par l'icône de la zone de notification (clic droit) ou le menu **Bibli (serveur)**. Ces trois actions pilotent le service et demandent donc l'autorisation d'administrateur (une fenêtre UAC) à chaque fois. Un serveur se laisse normalement allumé ; l'arrêter met Bibli hors service pour tous les postes jusqu'au prochain démarrage (il redémarre de lui-même au prochain allumage du PC, sauf si **Démarrer le serveur avec le PC** est décoché dans le menu de l'icône, ou désactivé par l'entrée **Démarrage automatique** du menu Démarrer). « Cacher l'icône » la retire, aussi aux ouvertures de session suivantes, sans arrêter le serveur ; **Afficher l'icône de notification** (menu Démarrer) la ramène.

**Pour qu'il reste joignable** :

- Bibli redémarre seul après une mise à jour de Windows ou un plantage, sans que personne ait à ouvrir de session.
- Un **portable** se met en veille quand on rabat l'écran ou qu'il passe sur batterie, même avec ce réglage : préférer un PC de bureau, ou régler *Fermeture du capot → Ne rien faire* (Panneau de configuration → Options d'alimentation).
- Après une **coupure de courant**, le PC ne se rallume tout seul que si son BIOS/UEFI le prévoit (option du type *Restore on AC Power Loss* ou *Après une coupure : allumer*), à régler une fois au démarrage du PC.
- Si les tablettes n'y accèdent plus alors que Bibli répond sur ce PC, vérifier d'abord que le réseau est toujours en **Privé** (Paramètres → Réseau et Internet).
- Si Bibli ne répond plus du tout, le motif est à la fin du journal (menu **Voir le journal**) : port 8080 déjà pris par un autre programme, base abîmée… Windows retente le démarrage chaque minute.

Bibli répond alors sur `http://localhost:8080` depuis ce PC, et sur `http://NOM-DU-PC:8080` depuis les tablettes et les autres postes (l'entrée **« Adresse pour les tablettes »** donne l'adresse exacte, à recopier). Mettre le réseau local en **Privé** (et non *Public*), sinon le pare-feu bloque l'accès, et réserver une **IP fixe** dans le routeur pour que l'adresse ne change jamais. Le nom du PC suffit aux autres postes Windows, mais les tablettes Android et les iPad ne le reconnaissent souvent pas : leur donner l'adresse IP.

Comme pour tout accès en HTTP, les cookies sont servis sans `Secure` (`-secure-cookies=false`) et la **caméra des tablettes n'est pas disponible** (voir [HTTPS ou réseau local](#https-ou-réseau-local--choisir-le-bon-mode)) ; les douchettes, elles, fonctionnent partout. Les liens de suivi sont désactivés (`-tracking-links=false`), un serveur HTTP sur le réseau local n'étant pas joignable depuis la maison.

L'installateur, l'icône de la zone de notification et le menu suivent la **langue de Windows** (français, néerlandais ou anglais ; français par défaut), comme l'application elle-même.

**Poser l'icône sur un autre poste** : y double-cliquer sur le même `.exe` et choisir **Autre poste** ; il demande le nom ou l'adresse IP du serveur, sans rien installer d'autre.

**Mettre à jour** : double-cliquer sur le nouveau `.exe` téléchargé (ou menu **Bibli (serveur) → Mettre à jour Bibli**, qui le demande) ; Bibli s'arrête, copie la base par sécurité, remplace le programme et redémarre. Ces copies (`C:\ProgramData\Bibli\backups\pre-update-…`) ne sont jamais effacées automatiquement : supprimer les plus anciennes de temps en temps. Le journal est `C:\ProgramData\Bibli\logs\bibli.log` (menu **Voir le journal**), renouvelé tous les 5 Mo. **Désinstaller** : *Paramètres → Applications → Bibli (serveur)*, ou menu **Bibli (serveur) → Désinstaller Bibli**, ou l'icône de notification. Les données de `C:\ProgramData\Bibli` sont **conservées**, sauf si la case « Supprimer aussi toutes les données » est cochée (y compris les fiches des lecteurs, sans retour possible).

## Docker

    cp .env.example .env        # y définir BIBLI_ADMIN_PASSWORD
    docker compose up -d

Le `docker-compose.yml` fourni est prévu pour tourner **derrière un reverse proxy** : Bibli n'écoute que sur `127.0.0.1:8087` et lit `X-Forwarded-For` (`-trust-proxy`). Le proxy s'adresse donc à `127.0.0.1:8087` (voir [Reverse proxy](#reverse-proxy)).

**Un seul PC sur le réseau local, sans proxy** (le cas le plus courant : le portable de la bibliothèque, une douchette au comptoir, l'équipe qui se connecte depuis ses appareils) : utiliser le fichier prêt à l'emploi `docker-compose.lan.yml`.

    cp .env.example .env
    docker compose -f docker-compose.lan.yml up -d

Bibli répond alors sur `http://IP-de-la-machine:8080`, en clair sur le réseau local. L'écran **Réglages** affiche cette adresse (bloc « Accès local ») à donner aux utilisateurs. La caméra des tablettes n'est pas disponible en HTTP (voir [HTTPS ou réseau local](#https-ou-réseau-local--choisir-le-bon-mode)) ; la douchette du comptoir, si.

La base vit dans `./data` sur l'hôte, avec les sauvegardes (`./data/backups`) et les couvertures (`./data/cache`). **Ne jamais** placer ce dossier sur un partage réseau (NFS/CIFS) : le verrouillage SQLite y est cassé et la base se corrompt.

Le compose suit le tag **`:stable`**, republié à chaque version (les versions exactes, `:1.2.3`, sont aussi disponibles). L'image n'est construite qu'à la publication d'une version, pour amd64 et arm64.

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

Bibli est **testé jusqu'à 50 000 exemplaires et 2 500 emprunteurs**, avec cinq ans de prêts (plus de 400 000), soit bien au-delà d'une école primaire ou secondaire, d'une maison de repos ou d'un centre culturel. Mesures sur une base de cette taille (184 Mo), servie par un ordinateur de bureau récent :

| Écran ou tâche | Temps |
|---|---|
| Prêt, retour, recherche d'un livre ou d'un emprunteur au comptoir | moins d'une milliseconde |
| Accueil, prêts en cours, inventaire, emprunteurs, fiche d'un livre | moins de 0,2 s |
| Statistiques | 0,3 s |
| Export CSV ou Excel de toute la collection | 0,2 à 0,3 s |
| Bilan annuel de la collection | 1,4 s |
| Sauvegarde quotidienne | moins d'une seconde |

Sur un Raspberry Pi, compter quelques fois plus lent : les écrans du quotidien restent instantanés.

Les seules limites fixes sont celles des codes imprimés sur les étiquettes et les cartes, tirés au hasard :

- **Exemplaires** : `VOL` suivi de 5 chiffres et d'un chiffre de contrôle, soit 100 000 codes possibles. Au-delà d'environ 60 000 exemplaires, un nouveau code pourrait ne pas être trouvé.
- **Cartes** : `LEC` suivi de 4 chiffres et d'un chiffre de contrôle, soit 10 000 codes possibles. Un emprunteur parti garde sa carte jusqu'à son anonymisation : avec la conservation par défaut (3 ans), un établissement de 2 500 emprunteurs en occupe moins de 4 000. Avec la conservation maximale (10 ans), un établissement de cette taille approche la limite.
- **Import d'emprunteurs** : 2 000 lignes par fichier ; au-delà, importer en deux fois.

## Bon à savoir

- **Catalogues interrogés** : BnF, UniCat, Google Books et Open Library, à partir de l'ISBN seul. Rien d'autre ne quitte le serveur.
- **Couvertures** : récupérées par le serveur (Open Library, puis BnF) et servies par lui, jamais par le navigateur. Elles sont conservées dans `-cache-dir`, un fichier par ISBN, absences comprises. Rien n'est écrit en base : le dossier peut être vidé à la main.
- **Langue** : un réglage pour toute l'instance (français, anglais, néerlandais). `?lang=en` ou `?lang=nl` sur n'importe quelle adresse la change pour un seul navigateur pendant douze heures, `?lang=auto` revient au réglage.
- **Liens de suivi** : activés par défaut ; `-tracking-links=false` les retire là où personne ne peut joindre le serveur.
- **Mise à jour** : démarrer une version récente sur une base ancienne applique les migrations manquantes, sans retour en arrière possible. Une sauvegarde récente suffit à revenir en arrière.

## Instance de démonstration

Une instance publique où n'importe qui peut cliquer : prêter, cataloguer par ISBN, imprimer des étiquettes. Elle se remet elle-même en place, et rien de ce qu'un visiteur y fait ne survit. C'est ce qui tourne sur [bibli.tintamarre.be](https://bibli.tintamarre.be).

    docker compose -f docker-compose.demo.yml up -d

Tout tient dans une variable, une durée Go (`6h`, `90m`) :

    BIBLI_DEMO_RESET=6h

Au démarrage puis à chaque échéance, toute la collection est supprimée et `app/demo.sql` rechargé en une seule transaction, avec les réglages qu'un visiteur peut modifier. Les sauvegardes sont désactivées d'office, puisqu'il n'y a rien à conserver, et un bandeau prévient sur chaque écran. Le mot de passe reste obligatoire : il suffit de le publier avec le lien.

**Ne jamais mettre cette variable sur une vraie instance** : elle supprime la base à chaque échéance. C'est pourquoi c'est une variable d'environnement et non une option de ligne de commande : rien ne l'attrape en recopiant le `command:` donné pour une vraie installation. Une valeur qui n'est pas une durée empêche Bibli de démarrer, plutôt que de laisser tourner une démonstration qui ne se réinitialise plus.
