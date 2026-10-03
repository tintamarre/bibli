#!/usr/bin/env python3
"""Regenerate the loans and the classes in app/demo.sql.

The dataset a visitor sees has to look like a school library rather than like a
random number generator, because two of the home screen's readings draw the
whole year at once and noise is visible in a way it never was in a list. What a
school actually does (as a school librarian described it):

  - the library opens once or twice a week, the same days each week;
  - three or four classes come at an opening, a handful of pupils from each;
  - a pupil holds at most two books at a time; a teacher borrows a batch for a
    lesson and is not held to that;
  - nothing at all happens during a closure.

Dates stay relative to the day the file is loaded, so the demonstration is
never out of date -- and the openings are real Tuesdays and Thursdays all the
same, because SQLite can find a weekday: date('now', '-140 days', 'weekday 2')
lands on a Tuesday whatever day the file is loaded on, and the modifiers chain,
so the due date and the return follow from it. A plain offset was tried first
and put the library's second opening of the week on a Sunday, which is the kind
of thing a year drawn at once makes obvious and a list never did.

Every anchor is a whole number of weeks before the load day, so they all share
its weekday and 'weekday 2' shifts every one of them by the same amount: the
schedule keeps its shape. Tuesday and Thursday shift by different amounts
though, so the gap between a week's two openings depends on the load day, and
the simulation below leaves a week of slack around every return so no copy can
be lent twice at once and no pupil can hold three books whatever that gap is.
TestDemoDatasetLoadsOnEveryDayOfTheWeek checks it for all seven.

The closures sit a plausible distance apart rather than on the Fédération's
real dates, which no relative offset can name.

Run it from the repository root:  python3 scripts/gen-demo-loans.py
"""

import collections
import random
import re

# One loan. anchor/modifier say which opening it was taken at, and are what the
# SQL is written from; day/due are the same dates as plain offsets, which is
# what the simulation orders events by. kept is None when the book is still out.
Loan = collections.namedtuple("Loan", "copy borrower anchor modifier day due kept")

SRC = "app/demo.sql"
SEED = 20260922          # the picture must be the same on every run
WEEKS = 57               # weeks of history: a school year and the term before
HISTORY = WEEKS * 7 + 7
PERIOD = 21              # the loan period demo.sql sets in /settings
TEACHERS_GROUP = "Enseignants"
CLASSES = ["P1", "P2", "P3", "P4"]

# Where the pupils the fixture shares must stay (app/testdata/fixture.sql is a
# subset of this file, down to these four classes).
PINNED = {101: "P4", 102: "P3", 103: "P4", 104: "P3"}

# The two loans the README quotes, written last so they are the last rows.
LEA, TOM = 101, 102
LEA_COPY, TOM_COPY = 3, 1     # VOL350929 and VOL204572
TOM_LATE = 5                  # days past its due date

# Closures, in weeks before the load day. Laid out backwards from a term in
# progress: seven weeks of class, then the summer, then a year of terms and the
# breaks between them.
CLOSED_WEEKS = [
    (8, 14),    # the summer
    (22, 23),   # spring
    (31, 31),   # carnival
    (39, 40),   # winter
    (47, 47),   # autumn
    (55, WEEKS),
]

# Two openings a week, and SQLite's numbering of the days: Tuesday and Thursday.
OPENINGS = [("weekday 2", 2), ("weekday 4", 4)]

# A week of slack around every return, so the schedule holds whatever weekday
# the file is loaded on (see the note at the top).
SLACK = 7


def closed(week):
    return any(lo <= week <= hi for lo, hi in CLOSED_WEEKS)


def read(path):
    return open(path, encoding="utf-8").read()


def borrowers(sql):
    """The pupils and teachers demo.sql declares, in file order. The teachers
    are the borrowers of the group TEACHERS_GROUP."""
    block = re.search(r"INSERT INTO borrower .*?;", sql, re.S).group(0)
    out = []
    for row in re.finditer(r"\((\d+), '[^']*(?:''[^']*)*', '[^']*', (NULL|'[^']*')", block):
        kind = "teacher" if row.group(2) == "'%s'" % TEACHERS_GROUP else "student"
        out.append((int(row.group(1)), kind))
    return out


def available_copies(sql):
    """Only copies that are available now are ever lent: a copy recorded lost or
    withdrawn must not be out, and a damaged one cannot be lent."""
    block = re.search(r"INSERT INTO copy .*?;", sql, re.S).group(0)
    return [int(m.group(1)) for m in re.finditer(r"\((\d+), \d+, 'VOL\d+', '[^']*', 'available'", block)]


def assign_classes(students, rng):
    """Four classes of roughly equal size, the fixture's four pupils pinned."""
    free = [b for b in students if b not in PINNED]
    rng.shuffle(free)
    out = dict(PINNED)
    for i, b in enumerate(free):
        out[b] = CLASSES[i % len(CLASSES)]
    return out


def generate():
    rng = random.Random(SEED)
    sql = read(SRC)

    people = borrowers(sql)
    students = [b for b, kind in people if kind == "student"]
    teachers = [b for b, kind in people if kind == "teacher"]
    copies = available_copies(sql)
    by_class = assign_classes(students, rng)
    roster = {c: [b for b in students if by_class[b] == c] for c in CLASSES}

    # Copies 1 and 3 are held back for the two loans the README quotes.
    pool = [c for c in copies if c not in (LEA_COPY, TOM_COPY)]

    # Every day below is a count of days before the load day, so time runs as
    # the number falls and a larger number is older. A copy is free at `day`
    # once it has come back, which is to say once its return is further in the
    # past: back > day. A loan still out on the load day is recorded as -1 and
    # so is never free again, which is what still out means.
    loans = []                  # (copy, borrower, loaned, due, returned)
    free_from = {}              # copy -> the day it came back
    holding = {b: [] for b in students}   # borrower -> the days its loans come back

    # Two openings a week, oldest first, each anchored on a real weekday. Some
    # weeks only the first of them: a library run by volunteers does not open
    # twice every week of the year.
    lesson = 0
    for week in range(WEEKS, -1, -1):
        if closed(week):
            continue
        for modifier, offset in OPENINGS:
            anchor = week * 7 + 7
            day = anchor - offset          # nominal, for ordering and slack
            if modifier == "weekday 4" and rng.random() < 0.3:
                continue                   # the second opening did not happen

            free = [c for c in pool if free_from.get(c, HISTORY + 1) > day + SLACK]
            for b in students:
                holding[b] = [d for d in holding[b] if d <= day + SLACK]

            # Three or four classes at an opening, a handful of pupils from each.
            for cls in rng.sample(CLASSES, rng.choice([3, 3, 4, 4])):
                for pupil in rng.sample(roster[cls], min(len(roster[cls]), rng.choice([2, 3, 3, 4]))):
                    room = 2 - len(holding[pupil])
                    for _ in range(min(room, rng.choice([1, 1, 1, 2]))):
                        if not free:
                            break
                        copy = free.pop(rng.randrange(len(free)))
                        loans.append(lend(rng, copy, pupil, anchor, modifier, day,
                                          free_from, holding))

            # A teacher takes a batch for a lesson every few weeks, and the
            # two-book rule is not theirs.
            lesson += 1
            if lesson % 5 == 0 and teachers:
                teacher = rng.choice(teachers)
                for _ in range(rng.randint(5, 9)):
                    if not free:
                        break
                    copy = free.pop(rng.randrange(len(free)))
                    loans.append(lend(rng, copy, teacher, anchor, modifier, day,
                                      free_from, None))

    loans = settle_overdue(rng, loans)
    loans.sort(key=lambda l: (-l.day, l.copy))

    # The two the README quotes, on the two copies held back for them. These
    # two are plain offsets: the README names the exact lateness, so they must
    # not drift with the weekday.
    loans.append(Loan(LEA_COPY, LEA, 3, None, 3, 3 - PERIOD, None))
    loans.append(Loan(TOM_COPY, TOM, PERIOD + TOM_LATE, None, PERIOD + TOM_LATE, TOM_LATE, None))
    return loans, by_class


def settle_overdue(rng, loans):
    """A school that runs well has a couple of books out late, not a column of
    them: a library where a third of the books were overdue made every screen
    look like a chase. One is left outstanding here, the README's Tom being the
    other, and everything else that ran over is given back."""
    out = []
    outstanding = 0
    for l in loans:
        # due is a count of days before the load day too, so a due date still
        # to come is negative and only due > 0 is actually late.
        if l.kept is None and l.due > 0 and l.modifier is not None:
            if outstanding:
                l = l._replace(kept=l.day - rng.randint(1, l.due))  # back, late
            else:
                outstanding += 1
        out.append(l)
    return out


def lend(rng, copy, borrower, anchor, modifier, day, free_from, holding):
    """One loan, kept for a week or three. Late is marginal: a library where a
    third of the books were overdue looked like a library in trouble.

    The dates are written as the opening's own date plus a number of days, so
    the three of them move together whatever weekday the file is loaded on."""
    kept = rng.choice([7, 7, 9, 12, 14, 14, 16, 19, 21])
    if rng.random() < 0.07:
        kept = PERIOD + rng.randint(1, 9)     # back after the day it was due
    back = day - kept
    if back <= SLACK:
        kept = None                            # still out on the load day
        free_from[copy] = -1
    else:
        free_from[copy] = back
    if holding is not None:
        holding[borrower].append(back if kept is not None else -1)
    return Loan(copy, borrower, anchor, modifier, day, day - PERIOD, kept)


def plus(days):
    if days == 0:
        return ""
    return ", '%s%d days'" % ("+" if days > 0 else "-", abs(days))


def date_of(l, add):
    """The SQL for one of a loan's three dates. An opening is written as the
    weekday it falls on, and the due date and the return as days from there."""
    if l.modifier is None:
        return "date('now'%s)" % plus(-l.day + add)
    return "date('now', '-%d days', '%s'%s)" % (l.anchor, l.modifier, plus(add))


def render(loans):
    """Blocks of sixty rows, as the file was written: SQLite parses one
    statement of nine hundred values happily, but nothing else does."""
    chunks = []
    for i in range(0, len(loans), 60):
        rows = []
        for l in loans[i:i + 60]:
            rows.append(" (%d, %d, %s, %s, %s)" % (
                l.copy, l.borrower, date_of(l, 0), date_of(l, PERIOD),
                date_of(l, l.kept) if l.kept is not None else "NULL"))
        chunks.append("INSERT INTO loan (copy_id, borrower_id, loaned_on, due_on, returned_on) VALUES\n"
                      + ",\n".join(rows) + ";")
    return "\n\n".join(chunks)


def main():
    loans, by_class = generate()
    sql = read(SRC)

    # The classes, rewritten in place on the borrower rows.
    def reclass(m):
        bid = int(m.group(1))
        return m.group(0) if bid not in by_class else m.group(0).replace(
            m.group(2), "'%s'" % by_class[bid], 1)

    block = re.search(r"INSERT INTO borrower .*?;", sql, re.S).group(0)
    fixed = re.sub(r"\((\d+), '(?:[^']|'')*', '[^']*', (NULL|'[^']*')",
                   reclass, block)
    sql = sql.replace(block, fixed)

    # The loans, between their heading and the settings that follow.
    head = sql.index("INSERT INTO loan (")
    tail = sql.index("\n\n-- Settings", head)
    sql = sql[:head] + render(loans) + sql[tail:]

    open(SRC, "w", encoding="utf-8").write(sql)

    out = sum(1 for l in loans if l.kept is None)
    late = sum(1 for l in loans if l.kept is None and l.due > 0)
    late_back = sum(1 for l in loans if l.kept is not None and l.kept > PERIOD)
    print("loans=%d out=%d overdue=%d returned-late=%d (%.0f%%)" % (
        len(loans), out, late, late_back, 100 * late_back / max(len(loans) - out, 1)))
    print("classes=" + ", ".join("%s:%d" % (c, sum(1 for v in by_class.values() if v == c))
                                 for c in CLASSES))


if __name__ == "__main__":
    main()
