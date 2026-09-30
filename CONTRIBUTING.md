# Contribuer à Bibli

Merci de votre intérêt. Bibli est pensé pour **survivre au départ de son auteur** : chaque contribution se juge à l'aune de « qu'est-ce qui casse dans cinq ans si personne n'y touche ».

Pour lancer le code, charger le jeu de démonstration et faire les captures : [docs/development.md](docs/development.md).

## Principes

- **Simplicité d'abord.** Go et la bibliothèque standard suffisent. Une seule dépendance de production : `modernc.org/sqlite`. Toute dépendance supplémentaire doit être justifiée explicitement.
- **Aucun CDN.** HTMX, ZXing, JsBarcode et la police sont vendorés dans `app/static/vendor/` et servis par le binaire ; leurs versions et licences sont listées dans `app/static/vendor/README.md`.
- **Pas de framework web, pas d'ORM, pas de générateur.** SQL écrit à la main, requêtes paramétrées.
- **Vie privée des enfants.** Un emprunteur, c'est un prénom, l'initiale du nom et une classe, rien d'autre. Seul un ISBN quitte le serveur. Un emprunteur n'est jamais supprimé : il est désactivé, puis anonymisé après la durée de conservation.
- **Migrations immuables.** On ne modifie jamais une migration déjà jouée sur une instance qui contient des données : on en crée une nouvelle (`app/migrations/00N_*.sql`). `001_initial.sql` est figé depuis la v1 ; les instances antérieures à la v1 ne sont pas migrables et se recréent depuis zéro.
- **Tout le texte affiché passe par le catalogue.** Rien n'est écrit en dur, ni dans un gabarit ni dans un handler : chaque phrase vit dans `app/locales/fr.json`, `en.json` **et** `nl.json`, sous une clé anglaise stable groupée par écran (`loan.confirm_button`). Les gabarits appellent `{{T "clé"}}`, ou `{{Tn "clé" n}}` quand un nombre commande l'accord. **Chaque nouvelle phrase s'écrit donc dans chaque catalogue** : c'est le prix des autres langues, et `i18n_test.go` échoue tant que ce n'est pas fait.

## Avant de proposer un changement

    make check

C'est `gofmt -l .`, `go vet ./...` et `go test ./...` ; la CI rejoue exactement ces étapes, puis construit l'image Docker. Pour reformater : `gofmt -w .`.

## Style

- Commits en **anglais**, à l'impératif, au format [Conventional Commits](https://www.conventionalcommits.org/) : `type(scope): description`. Le numéro de version et le changelog sont calculés à partir de ces messages : on n'écrit jamais un numéro de version à la main.
- **Le code est en anglais** : schéma, identifiants, routes, classes CSS, commentaires, journaux. Seul le texte affiché est traduit (voir ci-dessus).
- Un commit = un changement compréhensible isolément. Le commentaire dit ce que fait le code ; le message de commit dit pourquoi.
- Des tests pour la logique (ISBN, dates, rotation des sauvegardes…) et pour les écrans et les routes (`httptest`), sur le petit jeu de `app/testdata/fixture.sql`.
- Interface : cibles tactiles ≥ 44 px, aucune information réservée au survol, pas de glisser-déposer ; le rouge veut dire « en retard » et rien d'autre. Chaque écran se comprend sans formation. Français, anglais et néerlandais, le français faisant référence ; une langue par instance, réglée dans Réglages. `?lang=en` (ou `?lang=nl`) force la langue pour un seul navigateur, le temps de relire une traduction.

## Périmètre

Toute fonctionnalité nouvelle attend six mois d'usage réel avant d'être envisagée. En cas de doute, ouvrez une issue avant d'écrire du code.

Hors périmètre, sauf demande d'une école : réservations, catalogue public, notifications aux parents, comptes élèves, amendes, acquisitions, export MARC, multi-écoles, livres numériques.
