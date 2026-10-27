// Minimal client-side behaviour, no dependency; everything else is HTMX.
// No text here: this file is cached a year under one URL for every language,
// so its strings come from window.BIBLI_TEXTS (handlers.go, jsKeys).

// A missing key shows as itself, as on the server: a blank goes unnoticed.
function T(key) {
  var texts = window.BIBLI_TEXTS || {};
  var s = texts[key];
  if (s === undefined) { return key; }
  for (var i = 1; i < arguments.length; i++) {
    s = s.split("{" + (i - 1) + "}").join(arguments[i]);
  }
  return s;
}

// Plural rules from Intl.PluralRules (CLDR) rather than a copy of the server's.
function Tn(key, n) {
  var form = "other";
  try {
    form = new Intl.PluralRules(document.documentElement.lang || "fr").select(n);
  } catch (e) {
    form = Math.abs(n) === 1 ? "one" : "other"; // browser from before 2018
  }
  var args = [key + "." + form, n].concat(
    Array.prototype.slice.call(arguments, 2));
  var s = T.apply(null, args);
  // A language may carry only "one" and "other" where CLDR defines six.
  if (s === key + "." + form && form !== "other") {
    return T.apply(null, [key + ".other", n].concat(
      Array.prototype.slice.call(arguments, 2)));
  }
  return s;
}

// There is only ever one scan field at a time, so one querySelector is enough.
function refocusScan() {
  // [data-focus] wins, then the scan field, then any [autofocus].
  var field = document.querySelector('[data-focus]') ||
              document.querySelector('.scan') ||
              document.querySelector('[autofocus]');
  if (field && typeof field.focus === 'function') {
    field.focus();
  }
}

// The confirm button says how many books it will record. The header counter and
// the basket's empty state follow the same count.
function updateConfirmButton() {
  var button = document.getElementById('btn-confirm');
  var basket = document.getElementById('basket');
  if (!button || !basket) { return; }
  var n = basket.children.length;
  var books = Tn("js.books", n);
  button.disabled = n === 0;
  button.textContent = n === 0 ? T("loan.scan_to_start") : T("js.confirm_loan", books);
  basket.classList.toggle('basket-empty', n === 0);
  var count = document.getElementById('basket-count');
  if (count) { count.textContent = n === 0 ? '' : books; }
}

// The server sees the basket only on confirmation: drop a book scanned twice.
function dedupeBasket() {
  var basket = document.getElementById('basket');
  if (!basket) { return; }
  var seen = {};
  var rows = Array.prototype.slice.call(basket.children);
  for (var i = 0; i < rows.length; i++) {
    var id = rows[i].id;
    if (!id) { continue; }
    if (seen[id]) { rows[i].remove(); } else { seen[id] = true; }
  }
}

// After every HTMX swap, refocus and re-evaluate the button.
document.addEventListener('htmx:afterSwap', function () {
  dedupeBasket();
  refocusScan();
  updateConfirmButton();
});

// --- Walking a list of results with the keyboard --------------------------
// Down into the results, Up back out, Escape to the field; Enter is the
// result <button>'s own. On the document, since every such list is swapped in.

// hx-target may sit on the field or on its form: closest() resolves it the way
// HTMX inherits the attribute.
function resultsUnder(field) {
  var owner = field.closest('[hx-target]');
  var box = owner && document.querySelector(owner.getAttribute('hx-target'));
  return box ? box.querySelectorAll('.borrower-result') : [];
}

document.addEventListener('keydown', function (e) {
  var down = e.key === 'ArrowDown';
  var up = e.key === 'ArrowUp';
  if (!down && !up && e.key !== 'Escape') { return; }
  var el = e.target;
  if (!el.classList) { return; }

  // With no results, Down and Escape keep their ordinary meaning in the field.
  if (el.classList.contains('scan')) {
    var first = resultsUnder(el)[0];
    if (down && first) {
      e.preventDefault();
      first.focus();
    }
    return;
  }

  if (!el.classList.contains('borrower-result')) { return; }

  var list = el.closest('ul');
  var all = list ? Array.prototype.slice.call(list.querySelectorAll('.borrower-result')) : [];
  var i = all.indexOf(el);
  e.preventDefault();

  // Up from the first result, or Escape anywhere in the list, goes back to typing.
  if (e.key === 'Escape' || (up && i <= 0)) {
    refocusScan();
    return;
  }
  var next = all[down ? i + 1 : i - 1];
  if (next) { next.focus(); }
});

document.addEventListener('DOMContentLoaded', function () {
  refocusScan();
  updateConfirmButton();
});

// --- Confirmations -------------------------------------------------------
// Every question goes through askConfirm into the one dialog base.html carries.

// The part that matters is marked *between asterisks* in the sentence.
// Built from text nodes and <b>, never innerHTML: titles and names are not markup.
function writeQuestion(el, message) {
  el.textContent = '';
  message.split('*').forEach(function (part, i) {
    if (part === '') { return; }
    if (i % 2 === 0) {
      el.appendChild(document.createTextNode(part));
      return;
    }
    var strong = document.createElement('b');
    strong.textContent = part;
    el.appendChild(strong);
  });
}

// Resolves false, true, or the count chosen. Without <dialog> (Safari < 15.4)
// window.confirm answers, keeping the form's default count.
// The answer comes from the buttons' clicks: close does not fire everywhere the
// dialog closes, and is only listened for to catch Escape.
function askConfirm(message, count) {
  var dialog = document.getElementById('confirm');
  if (!dialog || !dialog.showModal) {
    return Promise.resolve(window.confirm(message.replace(/\*/g, '')));
  }
  writeQuestion(document.getElementById('confirm-text'), message);
  var yes = document.getElementById('confirm-yes');
  var no = document.getElementById('confirm-no');
  var row = document.getElementById('confirm-number-row');
  var field = document.getElementById('confirm-number');

  row.hidden = !count;
  if (count) {
    document.getElementById('confirm-number-label').textContent = count.label;
    field.value = count.value;
  }

  return new Promise(function (resolve) {
    var answered = false;
    function answer(value) {
      if (answered) { return; }        // Escape after a click, say
      answered = true;
      yes.removeEventListener('click', onYes);
      no.removeEventListener('click', onNo);
      field.removeEventListener('keydown', onKey);
      dialog.removeEventListener('close', onClose);
      if (dialog.open) { dialog.close(); }
      resolve(value);
    }
    // Out of range or empty: the default. The server checks it again.
    function chosen() {
      if (!count) { return true; }
      var n = parseInt(field.value, 10);
      if (isNaN(n) || n < 1 || n > 365) { return count.value; }
      return n;
    }
    function onYes() { answer(chosen()); }
    function onNo() { answer(false); }
    function onClose() { answer(false); }   // Escape, or the backdrop
    // Enter in the number field means "that many": it is inside no form.
    function onKey(e) { if (e.key === 'Enter') { e.preventDefault(); answer(chosen()); } }

    yes.addEventListener('click', onYes);
    no.addEventListener('click', onNo);
    field.addEventListener('keydown', onKey);
    dialog.addEventListener('close', onClose);
    dialog.showModal();
    // Not left to autofocus alone, which older Safari ignores inside a dialog.
    if (count) { field.focus(); field.select(); } else { no.focus(); }
  });
}

// hx-confirm: take htmx:confirm over, answer it, then issue the request.
document.addEventListener('htmx:confirm', function (e) {
  if (!e.detail.question) { return; }
  e.preventDefault();
  askConfirm(e.detail.question).then(function (ok) {
    if (ok) { e.detail.issueRequest(true); }
  });
});

// data-confirm on a plain form, captured so it is asked before anything else.
// data-confirm-field names the hidden field the count goes into; it already
// holds the default, so a browser without this script still posts sense.
document.addEventListener('submit', function (e) {
  var form = e.target;
  var question = form.dataset ? form.dataset.confirm : null;
  // data-confirm-change on a field: asked only when it differs from data-saved.
  var watched = form.querySelector ? form.querySelector('[data-confirm-change]') : null;
  if (!question && watched && watched.value.trim() !== watched.dataset.saved) {
    question = watched.dataset.confirmChange;
  }
  if (!question) { return; }
  e.preventDefault();
  var name = form.dataset.confirmField;
  var input = name ? form.elements[name] : null;
  var count = input
    ? { label: form.dataset.confirmLabel || '', value: parseInt(input.value, 10) }
    : null;
  askConfirm(question, count).then(function (ok) {
    if (!ok) { return; }
    if (input && typeof ok === 'number') { input.value = ok; }
    // form.submit() does not raise submit again, so this cannot loop.
    form.submit();
  });
}, true);

// Status saves on selection. Lost and withdrawn close a loan, so they ask
// first and restore the previous value on no.
function confirmStatus(select, proceed) {
  var v = select.value;
  var messages = {
    lost: T("js.confirm_lost"),
    withdrawn: T("js.confirm_withdrawn")
  };
  if (!messages[v]) {
    paintStatus(select, v);
    proceed();
    return;
  }
  askConfirm(messages[v]).then(function (ok) {
    if (ok) {
      paintStatus(select, v);
      proceed();
    } else {
      select.value = select.dataset.status || select.value;
    }
  });
}

// The dropdown's colour follows the value before the row comes back.
function paintStatus(select, v) {
  select.className = select.className.replace(/\bstatus-\w+/, 'status-' + v);
}

// The server renders only the path; location.origin stays right behind a proxy.
function copyLink(btn) {
  var link = location.origin + btn.dataset.link;
  // A class swaps the icon for a tick; replacing the text would drop the icon.
  var ok = function () {
    if (btn.classList.contains("is-copied")) return;
    btn.classList.add("is-copied");
    var label = btn.querySelector(".action-label");
    var previous = label ? label.textContent : null;
    if (label) label.textContent = T("js.link_copied");
    setTimeout(function () {
      btn.classList.remove("is-copied");
      if (label) label.textContent = previous;
    }, 1500);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(link).then(ok).catch(function () { window.prompt(T("js.copy_this_link"), link); });
  } else {
    window.prompt(T("js.copy_this_link"), link);
  }
}

// Streaming search: a log of the catalogues tried, then the form. One stream at
// a time: a slower earlier search must not fill the form over the ISBN now held.
var catalogueStream = null;

function catalogueSearch(e) {
  e.preventDefault();
  var form = e.currentTarget || e.target;
  var field = form.querySelector('[name="isbn"]');
  var isbn = (field && field.value ? field.value : "").trim();
  if (!isbn) { return false; }
  var log = document.getElementById("cat-log");
  if (log) { log.innerHTML = ""; }

  if (catalogueStream) { catalogueStream.close(); }
  var es = new EventSource("/catalogue/stream?isbn=" + encodeURIComponent(isbn));
  catalogueStream = es;
  function stale() { return catalogueStream !== es; }
  es.addEventListener("step", function (ev) {
    if (stale() || !log) { return; }
    var d = document.createElement("div");
    d.className = "log-line";
    d.textContent = ev.data;
    log.appendChild(d);
  });
  es.addEventListener("result", function (ev) {
    es.close();
    if (stale()) { return; }
    catalogueStream = null;
    var cat = document.getElementById("catalogue");
    if (!cat) { return; }
    cat.innerHTML = ev.data;
    if (window.htmx) { window.htmx.process(cat); } // activate the hx-* of the injected form
    var t = cat.querySelector('[name="title"]') || cat.querySelector("[data-focus]");
    if (t) { t.focus(); }
  });
  es.onerror = function () {
    es.close();
    if (stale()) { return; }
    catalogueStream = null;
    if (log) {
      // Most often the 12 h session expired and the stream got a 401.
      var d = document.createElement("div");
      d.className = "log-line err";
      d.textContent = T("js.search_interrupted");
      var a = document.createElement("a");
      a.href = "/login?next=/catalogue";
      a.textContent = T("js.reconnect");
      d.appendChild(a);
      d.appendChild(document.createTextNode(T("js.session_expired")));
      log.appendChild(d);
    }
  };
  return false;
}

// Batch cataloguing. Scans queue up and are asked a few at a time, so the
// scanner never waits for a catalogue and a slow book does not hold the
// others. Nothing is written until "Add": a found book is staged with its
// copies, to be checked, and added under the shelf typed beside the button. A
// book no catalogue could place is refused and listed, to be catalogued book
// by book in another tab.
var batchQueue = [], batchFlying = {}, batchActive = 0, batchSaving = false, batchSeq = 0;
// Lookups at once: enough to get past a slow catalogue, few enough not to be
// rate-limited by one.
var batchLanes = 3;
// A catalogue that did not answer in time often answers the same question at
// once a little later: such a book is asked once more before it is refused.
var batchRetryAfter = 30000, batchLater = 0;

function batchEl(id) { return document.getElementById(id); }

// The ISBN-13 a scan stands for, as staged rows carry it: a price add-on is
// dropped and a valid ISBN-10 becomes its 978 form. A misread ISBN-10 keeps its
// own key, so it is never taken for the book it resembles.
function batchKey(isbn) {
  var d = (isbn || "").toUpperCase().replace(/[^0-9X]/g, "");
  if (/^97[89][0-9]{10}([0-9]{2}|[0-9]{5})$/.test(d)) { d = d.slice(0, 13); }
  if (!/^[0-9]{9}[0-9X]$/.test(d)) { return d; }
  var sum = 0, i;
  for (i = 0; i < 10; i++) { sum += (d[i] === "X" ? 10 : +d[i]) * (10 - i); }
  if (sum % 11) { return d; }
  d = "978" + d.slice(0, 9);
  sum = 0;
  for (i = 0; i < 12; i++) { sum += (+d[i]) * (i % 2 ? 3 : 1); }
  return d + (10 - sum % 10) % 10;
}

function batchScan(e) {
  e.preventDefault();
  var field = (e.currentTarget || e.target).elements.isbn;
  var isbn = field.value.trim();
  field.value = "";
  field.focus();
  if (!isbn) { return false; }
  batchAudio(); // created during the keypress, which browsers require
  var li = document.createElement("li");
  li.className = "batch-row batch-pending";
  li.dataset.seq = ++batchSeq;
  var code = document.createElement("span");
  code.className = "code";
  code.textContent = isbn;
  li.appendChild(code);
  var list = batchEl("batch-list");
  list.insertBefore(li, list.firstChild);
  batchQueue.push({ isbn: isbn, key: batchKey(isbn), li: li });
  batchRefresh();
  batchPump();
  return false;
}

function batchPump() {
  while (batchActive < batchLanes) {
    // A copy of a book being looked up waits for that answer.
    var at = -1;
    for (var i = 0; i < batchQueue.length && at < 0; i++) {
      if (!batchFlying[batchQueue[i].key]) { at = i; }
    }
    if (at < 0) { return; }
    var job = batchQueue.splice(at, 1)[0];
    // A book already staged is one more copy, without a round trip.
    var staged = Array.prototype.find.call(batchEl("batch-list").children, function (li) {
      return li.dataset.kind === "found" && li.dataset.isbn === job.key;
    });
    if (staged) {
      batchMoreCopies(staged, 1, job.li);
      batchRefresh();
      continue;
    }
    batchLookup(job);
  }
}

// Back at the head of the queue, ahead of the copies that waited for it.
function batchAskLater(job) {
  job.retried = true;
  job.later = true;
  batchLater++;
  setTimeout(function () {
    batchLater--;
    job.later = false;
    delete batchFlying[job.key];
    batchQueue.unshift(job);
    batchRefresh();
    batchPump();
  }, batchRetryAfter);
}

function batchLookup(job) {
  batchActive++;
  batchFlying[job.key] = true;
  var body = new URLSearchParams();
  body.set("isbn", job.isbn);
  fetch("/catalogue/batch/add", { method: "POST", body: body, credentials: "same-origin" })
    .then(function (r) { if (!r.ok) { throw new Error(r.status); } return r.text(); })
    .then(function (html) {
      var t = document.createElement("template");
      t.innerHTML = html.trim();
      var row = t.content.firstElementChild;
      // An expired session answers with the sign-in page: no row, same as a fault.
      if (!row || !row.dataset.kind) { throw new Error("no row"); }
      if (row.hasAttribute("data-retry") && !job.retried) {
        batchAskLater(job);
        return;
      }
      var refused = batchPlace(job.li, row);
      if (refused) { batchRefusedQueued(refused, job.key); }
    })
    .catch(function () {
      job.li.className = "batch-row batch-refused";
      job.li.dataset.kind = "refused";
      job.li.dataset.isbn = job.isbn;
      var why = document.createElement("span");
      why.className = "batch-why";
      why.textContent = T("js.batch_add_failed");
      job.li.appendChild(why);
      batchPlace(job.li, job.li);
    })
    .then(function () {
      batchActive--;
      // A book asked again later keeps its copies waiting for that answer.
      if (!job.later) { delete batchFlying[job.key]; }
      batchRefresh();
      batchPump();
    });
}

// A found book is staged; a refused one leaves the list for its own.
// Answers with the refused line the book ended up on.
function batchPlace(pending, row) {
  if (row.dataset.kind === "found") {
    batchStage(pending, row);
    return null;
  }
  batchCardRefused(row);
  batchTone([220], 0.22);
  // The same book twice is one line, said to be scanned twice.
  var same = Array.prototype.find.call(batchEl("batch-refused-list").children, function (li) {
    return li !== row && li.dataset.isbn === row.dataset.isbn;
  });
  if (same) {
    batchRefusedAgain(same);
    pending.remove();
    return same;
  }
  batchEl("batch-refused-list").appendChild(row);
  if (row !== pending) { pending.remove(); }
  return row;
}

// One more scan of a refused book: the line says how many, and the link to
// catalogue it by hand asks for as many copies.
function batchRefusedAgain(same) {
  var n = (parseInt(same.dataset.copies, 10) || 1) + 1;
  same.dataset.copies = n;
  var times = same.querySelector(".batch-times");
  if (!times) {
    times = document.createElement("span");
    times.className = "batch-times";
    same.appendChild(times);
  }
  times.textContent = "×" + n;
  var fix = same.querySelector(".batch-fix");
  if (fix) { fix.href = fix.href.split("&copies=")[0] + "&copies=" + n; }
}

// Copies scanned while their book was being looked up share the server's
// refusal: asking again at once would only make the queue wait as long a second
// time. A scan made after the refusal is shown is a retry, and is asked. A
// failed request says nothing of the book, so it does not count here.
function batchRefusedQueued(same, key) {
  batchQueue = batchQueue.filter(function (job) {
    if (job.key !== key) { return true; }
    batchRefusedAgain(same);
    job.li.remove();
    return false;
  });
}

function batchItem(li) { return JSON.parse(li.dataset.item); }

function batchSetCopies(li, n) {
  var it = batchItem(li);
  it.copies = Math.max(1, Math.min(100, n));
  li.dataset.item = JSON.stringify(it);
  li.querySelector(".batch-n").textContent = it.copies;
  li.querySelector(".batch-qty .btn-qty").disabled = it.copies <= 1;
}

// The same book scanned again is one line with one more copy, moved to the top
// where the eye is; it keeps its place in the scan order (data-seq).
function batchStage(pending, row) {
  var list = batchEl("batch-list");
  var it = batchItem(row);
  var same = it.isbn13 && Array.prototype.find.call(list.children, function (li) {
    return li !== pending && li.dataset.kind === "found" && li.dataset.isbn === row.dataset.isbn;
  });
  if (same) {
    batchMoreCopies(same, it.copies, pending);
    return;
  }
  row.dataset.seq = pending.dataset.seq;
  batchSetCopies(row, it.copies);
  batchCardFound(row, "");
  if (pending.parentNode === list) { pending.replaceWith(row); }
  else { list.insertBefore(row, list.firstChild); }
}

function batchMoreCopies(li, n, pending) {
  batchSetCopies(li, batchItem(li).copies + n);
  var list = batchEl("batch-list");
  list.insertBefore(li, list.firstChild);
  pending.remove();
  batchCardFound(li, Tn("js.batch_copies", batchItem(li).copies));
}

function batchQty(btn, delta) {
  var li = btn.closest("li");
  batchSetCopies(li, batchItem(li).copies + delta);
  batchRefresh();
}

function batchRemove(btn) {
  btn.closest("li").remove();
  batchRefresh();
  refocusScan();
}

function batchRefresh() {
  var staged = batchStaged(), books = staged.length, copies = 0;
  staged.forEach(function (s) { copies += s.item.copies; });
  batchEl("batch-summary").textContent = books ? Tn("js.books", books) + " · " + Tn("js.batch_copies", copies) : "";
  batchEl("batch-save").disabled = books === 0 || batchSaving;
  batchSheet(copies);
  var waiting = batchQueue.length + batchActive + batchLater;
  batchEl("batch-pending").hidden = waiting === 0;
  batchEl("batch-pending").textContent = waiting ? Tn("js.batch_pending", waiting) : "";
  var refused = batchEl("batch-refused-list").children.length;
  batchEl("batch-refused").hidden = refused === 0;
  var working = batchActive > 0 || batchQueue.length > 0 || batchLater > 0;
  // Leaving now would lose what is staged, the refused ISBNs, or scans in flight.
  window.onbeforeunload = (books > 0 || refused > 0 || working)
    ? function (e) { e.preventDefault(); e.returnValue = ""; }
    : null;
}

// One transaction on the server: every staged book is added, under the shelf
// the field holds at that moment, or none is.
function batchSave() {
  var staged = batchStaged();
  if (!staged.length || batchSaving) { return; }
  // Sent, and so labelled, in the order the books were first scanned: the
  // order of the pile. A full sheet takes the first scans; a book straddling
  // the sheet's end sends only what fits.
  var size = batchSheetSize(), total = 0;
  staged.forEach(function (s) { total += s.item.copies; });
  var full = total >= size, rows = [], left = size;
  var body = new URLSearchParams();
  body.set("location", batchEl("batch-location").value.trim());
  for (var i = 0; i < staged.length && (!full || left > 0); i++) {
    var it = staged[i].item;
    var n = full ? Math.min(it.copies, left) : it.copies;
    left -= n;
    rows.push({ li: staged[i].li, sent: n });
    it.copies = n;
    body.append("item", JSON.stringify(it));
  }
  // Opened now, inside the click: a window opened when the answer arrives
  // would be stopped as a pop-up.
  var win = full ? window.open("", "_blank") : null;
  batchSaving = true;
  batchRefresh();
  var result = batchEl("batch-result");
  fetch("/catalogue/batch/save", { method: "POST", body: body, credentials: "same-origin" })
    .then(function (r) { if (!r.ok) { throw new Error(r.status); } return r.text(); })
    .then(function (html) {
      var t = document.createElement("template");
      t.innerHTML = html.trim();
      if (!t.content.querySelector(".batch-result")) { throw new Error("no result"); }
      // Only what was sent comes off: a scan or a + may have landed meanwhile.
      rows.forEach(function (r) {
        if (!r.li.isConnected) { return; }
        var rest = batchItem(r.li).copies - r.sent;
        if (rest > 0) { batchSetCopies(r.li, rest); } else { r.li.remove(); }
      });
      result.innerHTML = html;
      var print = result.querySelector(".batch-print");
      if (win && print) { win.opener = null; win.location = print.href; win = null; }
      if (win) { win.close(); win = null; }
      // What is left may fill the next sheet: that one chimes too.
      batchSheetWasFull = false;
      batchEl("batch-last").hidden = true;
    })
    .catch(function () {
      if (win) { win.close(); win = null; }
      result.innerHTML = "";
      var p = document.createElement("p");
      p.className = "err";
      p.textContent = T("js.batch_save_failed");
      result.appendChild(p);
    })
    .then(function () {
      batchSaving = false;
      batchRefresh();
      refocusScan();
    });
}

// Starts the next batch: the result and the refused books go, the location
// and any scan made since stay.
function batchNew() {
  var refused = batchEl("batch-refused-list").children.length;
  var go = refused ? askConfirm(Tn("js.batch_new_confirm", refused)) : Promise.resolve(true);
  go.then(function (ok) {
    if (!ok) { refocusScan(); return; }
    batchEl("batch-result").innerHTML = "";
    batchEl("batch-refused-list").innerHTML = "";
    batchEl("batch-last").hidden = true;
    batchRefresh();
    refocusScan();
  });
}

function batchSheetSize() { return parseInt(batchEl("batch-sheet").dataset.size, 10) || 40; }

// The staged books in the order they were first scanned.
function batchStaged() {
  return Array.prototype.map.call(document.querySelectorAll('#batch-list li[data-kind="found"]'), function (li) {
    return { li: li, item: batchItem(li), seq: parseInt(li.dataset.seq, 10) || 0 };
  }).sort(function (a, b) { return a.seq - b.seq; });
}

// The sheet under way: how full it is, and when it fills, a chime and the
// add button offering exactly one sheet.
var batchSheetWasFull = false;
function batchSheet(copies) {
  var size = batchSheetSize(), full = copies >= size;
  batchEl("batch-sheet-fill").style.width = Math.min(100, copies * 100 / size) + "%";
  batchEl("batch-sheet").classList.toggle("is-full", full);
  batchEl("batch-sheet-text").textContent = copies
    ? T("js.batch_sheet", Math.min(copies, size), size) + (copies > size ? " · " + Tn("js.batch_sheet_next", copies - size) : "")
    : "";
  batchEl("batch-sheet-full").hidden = !full;
  var save = batchEl("batch-save");
  save.querySelector("[data-save-all]").hidden = full;
  save.querySelector("[data-save-sheet]").hidden = !full;
  if (full && !batchSheetWasFull) { batchTone([660, 880], 0.12); }
  batchSheetWasFull = full;
}

// The card under the scan field: the last answer, until the next one.
function batchCard(kind) {
  var card = batchEl("batch-last");
  card.className = "batch-last " + kind;
  card.innerHTML = "";
  card.hidden = false;
  return card;
}

function batchCardFound(row, again) {
  var card = batchCard(row.querySelector(".batch-badge-known") ? "batch-last-known" : "batch-last-new");
  var cover = row.querySelector(".batch-cover");
  if (cover) { card.appendChild(cover.cloneNode()); }
  var main = document.createElement("div");
  main.className = "batch-last-main";
  var title = document.createElement("b");
  title.textContent = "✓ " + row.querySelector(".batch-title").textContent;
  main.appendChild(title);
  var meta = row.querySelector(".batch-meta");
  if (meta) { var m = document.createElement("span"); m.className = "batch-meta"; m.textContent = meta.textContent; main.appendChild(m); }
  var badge = document.createElement("span");
  badge.className = "batch-last-badge";
  badge.textContent = again || row.querySelector(".batch-badge").textContent;
  main.appendChild(badge);
  card.appendChild(main);
}

function batchCardRefused(row) {
  var card = batchCard("batch-last-refused");
  var main = document.createElement("div");
  main.className = "batch-last-main";
  var title = document.createElement("b");
  title.textContent = "✗ " + (row.querySelector(".batch-why") || {}).textContent;
  main.appendChild(title);
  var code = document.createElement("span");
  code.className = "code";
  code.textContent = row.dataset.isbn;
  main.appendChild(code);
  // A valid ISBN no catalogue knows goes on the pile to catalogue by hand; a
  // misread is scanned again, its message says so.
  if (row.hasAttribute("data-manual")) {
    var aside = document.createElement("span");
    aside.className = "batch-last-badge";
    aside.textContent = T("js.batch_set_aside");
    main.appendChild(aside);
  }
  card.appendChild(main);
}

// Short, quiet tones from the browser itself: no sound file to fetch. Off when
// muted on this computer.
var batchAudioCtx = null;
function batchSoundOn() {
  try { return localStorage.getItem("bibli.batchSound") !== "off"; } catch (e) { return true; }
}

function batchAudio() {
  if (!batchSoundOn()) { return null; }
  try {
    if (!batchAudioCtx) { batchAudioCtx = new (window.AudioContext || window.webkitAudioContext)(); }
    if (batchAudioCtx.state === "suspended") { batchAudioCtx.resume(); }
  } catch (e) { batchAudioCtx = null; }
  return batchAudioCtx;
}

// A sound that fails stays silent: it must never break the scan it follows.
function batchTone(freqs, length) {
  var ctx = batchAudio();
  if (!ctx) { return; }
  try { batchPlay(ctx, freqs, length); } catch (e) { /* no sound */ }
}

function batchPlay(ctx, freqs, length) {
  var t = ctx.currentTime;
  freqs.forEach(function (f, i) {
    var osc = ctx.createOscillator(), gain = ctx.createGain();
    osc.type = "sine";
    osc.frequency.value = f;
    var start = t + i * length;
    gain.gain.setValueAtTime(0.0001, start);
    gain.gain.exponentialRampToValueAtTime(0.12, start + 0.015);
    gain.gain.exponentialRampToValueAtTime(0.0001, start + length);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(start);
    osc.stop(start + length + 0.02);
  });
}

function batchToggleSound() {
  var on = !batchSoundOn();
  try { localStorage.setItem("bibli.batchSound", on ? "on" : "off"); } catch (e) { /* stays as it was */ }
  batchShowSound();
  if (on) { batchTone([660], 0.1); }
  refocusScan();
}

function batchShowSound() {
  var btn = batchEl("batch-sound");
  if (!btn) { return; }
  var on = batchSoundOn();
  btn.setAttribute("aria-pressed", on ? "true" : "false");
  btn.querySelector("[data-sound-on]").hidden = !on;
  btn.querySelector("[data-sound-off]").hidden = on;
}
document.addEventListener("DOMContentLoaded", batchShowSound);

// Sorting by a column heading. The order lives in two hidden fields of the
// filter form, so it always posts with the filter; clicking again reverses.
// "first" is a column's initial direction (sortColDesc, handlers.go).
function sortTable(formId, column, first) {
  var form = document.getElementById(formId);
  if (!form) { return; }
  var sort = form.elements.sort, dir = form.elements.dir;
  if (sort.value === column) {
    dir.value = dir.value === "desc" ? "asc" : "desc";
  } else {
    sort.value = column;
    dir.value = first === "desc" ? "desc" : "asc";
  }
  form.dispatchEvent(new Event("sortchange"));
}

// The same for a dropdown of orders (the wall of covers): value is "sort:dir".
function sortBy(formId, value) {
  var form = document.getElementById(formId);
  var parts = value.split(":");
  if (!form || parts.length !== 2) { return; }
  form.elements.sort.value = parts[0];
  form.elements.dir.value = parts[1];
  form.dispatchEvent(new Event("sortchange"));
}

// The /inventory date fields, revealed by the entry marked data-range. Hidden
// rather than emptied; the server ignores them once a period is chosen.
function toggleRange(select, id) {
  var range = document.getElementById(id);
  if (!range) { return; }
  var chosen = select.options[select.selectedIndex];
  range.hidden = !chosen || !chosen.hasAttribute("data-range");
  if (!range.hidden) {
    var first = range.querySelector("input");
    if (first) { first.focus(); }
  }
}

// A menu entry that opens a form further down the page. The menu is closed
// here: the listener below only handles clicks outside a menu.
function openForm(id) {
  var block = document.getElementById(id);
  if (!block) { return; }
  document.querySelectorAll("details[data-autoclose][open]").forEach(function (d) {
    d.open = false;
  });
  block.open = true;
  block.scrollIntoView({ block: "nearest" });
  var field = block.querySelector("input, select, textarea");
  if (field) { field.focus(); }
}

// A cancel button beside save closes the <details> and resets its form.
function closeForm(id) {
  var block = document.getElementById(id);
  if (!block) { return; }
  block.querySelectorAll("form").forEach(function (f) { f.reset(); });
  block.open = false;
}

// A <details data-autoclose> closes on a click outside or Escape, like a menu;
// a foldable section is never marked. On the document, since HTMX replaces
// one of these menus on every keystroke.
document.addEventListener("click", function (ev) {
  // Nor does answering a question one of its entries asked.
  if (ev.target.closest && ev.target.closest("dialog")) { return; }
  document.querySelectorAll("details[data-autoclose][open]").forEach(function (d) {
    if (!d.contains(ev.target)) { d.open = false; }
  });
});
document.addEventListener("keydown", function (ev) {
  if (ev.key !== "Escape") { return; }
  document.querySelectorAll("details[data-autoclose][open]").forEach(function (d) {
    d.open = false;
    // Focus back to the summary, not a link that is no longer on screen.
    var summary = d.querySelector("summary");
    if (summary) { summary.focus(); }
  });
});

// The theme picker in /settings shows its choice on the page at once, before
// the form is saved; leaving without saving puts the library's theme back.
document.addEventListener("change", function (ev) {
  if (ev.target.matches && ev.target.matches("select[data-theme-preview]")) {
    document.documentElement.dataset.theme = ev.target.value;
  }
});

// While a scan is pending, the desk's "searching…" line polls the server for
// which catalogue it is on and shows the server's sentence (desklookup.go).
(function () {
  var timer = null;
  var line = null;
  var idle = "";

  function stop() {
    if (timer) { clearInterval(timer); timer = null; }
    if (line) { line.textContent = idle; }
  }

  function newId() {
    var b = new Uint8Array(8);
    crypto.getRandomValues(b);
    return Array.prototype.map.call(b, function (x) { return ("0" + x.toString(16)).slice(-2); }).join("");
  }

  document.addEventListener("htmx:configRequest", function (e) {
    if (!e.detail.elt || e.detail.elt.id !== "form-book") { return; }
    stop();
    line = document.getElementById("emp-indic");
    if (!line) { return; }
    idle = line.textContent;
    var id = newId();
    e.detail.parameters.lookup = id;
    timer = setInterval(function () {
      fetch("/borrow/lookup?id=" + id, { credentials: "same-origin", cache: "no-store" })
        .then(function (r) { return r.status === 200 ? r.text() : null; })
        .then(function (t) { if (t && timer) { line.textContent = t; } })
        .catch(function () { /* the line keeps its last words */ });
    }, 400);
  });
  document.addEventListener("htmx:afterRequest", function (e) {
    if (e.detail.elt && e.detail.elt.id === "form-book") { stop(); }
  });

  // Settings tabs: the wrapper's data-tab selects the panel (CSS); a click moves
  // it, and aria-selected follows so a screen reader tracks the active tab.
  document.querySelectorAll(".settings-tabs-wrap").forEach(function (wrap) {
    var tabs = wrap.querySelectorAll("[role=tab]");
    function select(name) {
      wrap.dataset.tab = name;
      tabs.forEach(function (t) {
        t.setAttribute("aria-selected", t.dataset.tab === name ? "true" : "false");
      });
    }
    tabs.forEach(function (t) {
      t.addEventListener("click", function () { select(t.dataset.tab); });
    });
    select(wrap.dataset.tab); // initialise aria from the server-set tab
  });

  // Copy the address a teacher connects to. The button sits next to a readonly
  // input holding the URL; the brief "copied" class is the only feedback, so no
  // text is needed here (app.js carries none).
  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var input = btn.parentNode.querySelector("input");
      if (!input) { return; }
      input.focus();
      input.select();
      if (navigator.clipboard) {
        navigator.clipboard.writeText(input.value).catch(function () {});
      } else {
        try { document.execCommand("copy"); } catch (e) { /* selected for a manual copy */ }
      }
      btn.classList.add("copied");
      setTimeout(function () { btn.classList.remove("copied"); }, 1200);
    });
  });
})();
