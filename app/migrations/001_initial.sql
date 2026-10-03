-- Bibli — the whole schema, in one migration. Once it has run
-- on a real library it is never edited again: add 002_something.sql instead.
-- schema_migrations is created by db.go, which must know what has already run.
-- STRICT tables throughout, dates as ISO-8601 TEXT (YYYY-MM-DD).

-- ---------------------------------------------------------------------------
-- The work: one row per title, whatever the number of physical copies.
-- ---------------------------------------------------------------------------
CREATE TABLE book (
    id              INTEGER PRIMARY KEY,
    isbn13          TEXT    UNIQUE,          -- canonical form. NULL for books without ISBN
    isbn10          TEXT,                    -- NULL when absent, or when the prefix is 979
    title           TEXT    NOT NULL,
    subtitle        TEXT,
    authors         TEXT,                    -- free text, separated by " ; "
    publisher       TEXT,
    year            INTEGER,
    language        TEXT,                    -- ISO 639-1 code: 'fr', 'nl', 'en'

    -- Provenance of the automatic enrichment. Covers live in -cache-dir,
    -- never in the database.
    source_metadata TEXT,                    -- 'bnf' | 'unicat' | 'google' | 'openlibrary' | 'manual' | 'local'
    source_url      TEXT,                    -- permanent URL of the catalogue record
    source_payload  TEXT,                    -- raw catalogue response
    source_date     TEXT,

    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- ---------------------------------------------------------------------------
-- The physical book: what carries a label and what gets borrowed.
-- ---------------------------------------------------------------------------
CREATE TABLE copy (
    id          INTEGER PRIMARY KEY,
    book_id     INTEGER NOT NULL REFERENCES book(id) ON DELETE RESTRICT,
    code        TEXT    NOT NULL UNIQUE,     -- e.g. 'VOL204572' — printed on the label
    location    TEXT,                        -- 'classe P3', 'bac albums', 'réserve'
    status      TEXT    NOT NULL DEFAULT 'available'
                CHECK (status IN ('available','damaged','lost','withdrawn')),
    acquired_on TEXT,
    notes       TEXT,

    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- ---------------------------------------------------------------------------
-- Readers and staff alike. Minors' data: first name, last-name initial and
-- group only — never the full last name.
-- ---------------------------------------------------------------------------
CREATE TABLE borrower (
    id             INTEGER PRIMARY KEY,
    first_name     TEXT    NOT NULL,
    last_initial   TEXT    NOT NULL,         -- "Durant" is stored as "D."
    group_name     TEXT,                     -- class, floor, team: 'P3A', '2nd floor'. Changes every year
    card_code      TEXT    UNIQUE,           -- card barcode, e.g. 'LEC73048'
    active         INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    tracking_token TEXT,                     -- read-only link to the open loans, NULL by default
    deactivated_on TEXT,                     -- start of the anonymisation delay

    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- ---------------------------------------------------------------------------
-- returned_on IS NULL means the loan is open.
-- ---------------------------------------------------------------------------
CREATE TABLE loan (
    id          INTEGER PRIMARY KEY,
    copy_id     INTEGER NOT NULL REFERENCES copy(id)     ON DELETE RESTRICT,
    borrower_id INTEGER NOT NULL REFERENCES borrower(id) ON DELETE RESTRICT,
    loaned_on   TEXT    NOT NULL DEFAULT (date('now')),
    due_on      TEXT    NOT NULL,
    returned_on TEXT
) STRICT;

-- ---------------------------------------------------------------------------
-- Settings changeable without redeploying.
-- ---------------------------------------------------------------------------
CREATE TABLE setting (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    label      TEXT NOT NULL,                -- human-readable, for the settings screen
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
) STRICT;

-- ---------------------------------------------------------------------------
-- Indexes. The partial unique index on loan is what guarantees at database
-- level that a copy is out at most once, whatever two tablets do at once.
-- ---------------------------------------------------------------------------
CREATE INDEX idx_book_title  ON book(title);
CREATE INDEX idx_book_isbn10 ON book(isbn10) WHERE isbn10 IS NOT NULL;
CREATE INDEX idx_copy_book ON copy(book_id);
CREATE INDEX idx_borrower_group ON borrower(group_name) WHERE active = 1;
CREATE INDEX idx_borrower_name  ON borrower(last_initial, first_name);
CREATE UNIQUE INDEX idx_borrower_token ON borrower(tracking_token)
    WHERE tracking_token IS NOT NULL;
CREATE UNIQUE INDEX idx_loan_copy_active ON loan(copy_id) WHERE returned_on IS NULL;
CREATE INDEX idx_loan_borrower_active    ON loan(borrower_id) WHERE returned_on IS NULL;
CREATE INDEX idx_loan_overdue            ON loan(due_on) WHERE returned_on IS NULL;
-- Every loan ever, for the histories and counts: without them each borrower
-- list, book page and statistic reads the whole table, which grows every year.
CREATE INDEX idx_loan_borrower ON loan(borrower_id);
CREATE INDEX idx_loan_copy     ON loan(copy_id);

-- ---------------------------------------------------------------------------
-- Views.
-- ---------------------------------------------------------------------------

-- What sits on the shelf, ready to go out.
CREATE VIEW v_available AS
SELECT c.id AS copy_id,
       c.code,
       c.location,
       b.title,
       b.authors
FROM copy c
JOIN book b ON b.id = c.book_id
WHERE c.status = 'available'
  AND NOT EXISTS (
      SELECT 1 FROM loan l
      WHERE l.copy_id = c.id AND l.returned_on IS NULL
  );

-- Who has what right now.
CREATE VIEW v_active_loan AS
SELECT l.id  AS loan_id,
       br.id AS borrower_id,
       b.id  AS book_id,
       br.first_name, br.last_initial, br.group_name,
       c.code,
       b.title,
       l.loaned_on,
       l.due_on,
       CAST(julianday('now') - julianday(l.due_on) AS INTEGER) AS days_overdue
FROM loan l
JOIN copy     c  ON c.id  = l.copy_id
JOIN book     b  ON b.id  = c.book_id
JOIN borrower br ON br.id = l.borrower_id
WHERE l.returned_on IS NULL;

-- Overdue loans, by group (printable weekly reminder).
CREATE VIEW v_overdue AS
SELECT * FROM v_active_loan
WHERE days_overdue > 0
ORDER BY group_name, last_initial, first_name;

-- ---------------------------------------------------------------------------
-- The sentinel borrower that receives anonymised loans: inactive, so it
-- never shows up at the desk. Its name comes from the locale catalogue, filled
-- at startup by syncAnonymousName().
-- ---------------------------------------------------------------------------
INSERT INTO borrower (id, first_name, last_initial, group_name, card_code, active, deactivated_on)
VALUES (1, '', '', NULL, NULL, 0, NULL);

-- ---------------------------------------------------------------------------
-- Default settings. session_secret is absent on purpose: auth.go draws it at
-- first start and stores it, so no two instances share a signing key.
-- ---------------------------------------------------------------------------
INSERT INTO setting (key, value, label) VALUES
 ('loan_days',             '14', 'Default loan period, in days'),
 ('library_name',          '',   'Library name, shown in the header and on printouts'),
 ('language',              'fr', 'Language of the interface, the printouts and the exports'),
 ('theme',                 'ink',   'Colour theme of the screens (themes.go); printouts ignore it'),
 ('retention_years',       '3',  'Years before returned loans and departed readers are anonymised'),
 -- Cataloguing an unknown ISBN at the desk. Only '0' turns it off;
 -- any other value, or no row at all, leaves it on (expressCatalogue()).
 ('express_catalogue',     '1',  'Allow cataloguing an unknown book from the lending desk'),
 ('anonymous_borrower_id', '1',  'Id of the borrower receiving anonymised loans (do not change)');
