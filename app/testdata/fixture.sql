-- Bibli — the fixture the tests reason about.
--
-- Small on purpose, and fixed: every database-level test counts these rows by
-- hand ("v_available: want 3"), names these copies (VOL204572 is out to Tom and
-- overdue) and would have to be rewritten the day one row is added here. It is
-- loaded by testDB (app/db_test.go) into a fresh schema, so it also checks that
-- hand-written SQL still fits the migration.
--
-- The dataset a school is shown is app/demo.sql: a hundred books, two
-- classes and six months of loans. It grew out of this file and is a superset
-- of it — same first three books, same five copies, same five borrowers — so
-- the codes quoted in the README mean the same thing in both.

-- Books ---------------------------------------------------------------------
INSERT INTO book (id, isbn13, isbn10, title, subtitle, authors, publisher, year, language, source_metadata) VALUES
 (1, '9782070408504', '2070408507', 'Le Petit Prince', 'avec des aquarelles de l''auteur', 'Saint-Exupéry, Antoine de', 'Gallimard', 1999, 'fr', 'demo'),
 (2, '9782211037495', '2211037496', 'Le loup est revenu', NULL, 'Pennart, Geoffroy de', 'École des loisirs', 1996, 'fr', 'demo'),
 (3, NULL, NULL, 'Album maternelle (sans ISBN)', NULL, 'Anonyme', NULL, NULL, 'fr', 'manual');

-- Copies --------------------------------------------------------------------
INSERT INTO copy (id, book_id, code, location, status) VALUES
 (1, 1, 'VOL204572', 'bac albums',  'available'),
 (2, 1, 'VOL811045', 'bac albums',  'available'),
 (3, 2, 'VOL350929', 'classe P3',   'available'),
 (4, 2, 'VOL627437', 'classe P3',   'available'),
 (5, 3, 'VOL146302', 'coin lecture','available');

-- Readers, plus one staff member without a group ---------------------------------------------------
-- Only the initial of the family name is kept (data minimisation).
-- Codes carry no order: LEC or VOL, random digits, a check digit (codes.go).
INSERT INTO borrower (id, first_name, last_initial, group_name, card_code, active) VALUES
 (101, 'Léa',   'D.', 'P4', 'LEC73048', 1),
 (102, 'Tom',   'B.', 'P3', 'LEC28462', 1),
 (103, 'Zoé',   'P.', 'P4', 'LEC51932', 1),
 (104, 'Noah',  'M.', 'P3', 'LEC06813', 1),
 (105, 'Claire','L.', NULL,  'LEC40275', 1);

-- Loans: one running on time, one overdue ------------------------------------
INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on) VALUES
 (3, 101, date('now', '-3 days'),  date('now', '+18 days')),   -- Léa, on time
 (1, 102, date('now', '-26 days'), date('now', '-5 days'));    -- Tom, 5 days late
