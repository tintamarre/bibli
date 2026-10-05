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

// Batch cataloguing. Scans queue up and are asked one after the other, so the
// scanner never waits for a catalogue. A book no catalogue could place comes back
// "aside" and is filled in by hand, one after the other, once the scanning is done.
var batchQueue = [], batchBusy = false, batchSeen = {}, batchCurrent = null;

function batchEl(id) { return document.getElementById(id); }

function batchScan(e) {
  e.preventDefault();
  var field = (e.currentTarget || e.target).elements.isbn;
  var isbn = field.value.trim();
  field.value = "";
  field.focus();
  if (!isbn) { return false; }
  var li = document.createElement("li");
  li.className = "batch-row batch-pending";
  var code = document.createElement("span");
  code.className = "code";
  code.textContent = isbn;
  li.appendChild(code);
  var log = batchEl("batch-log");
  log.insertBefore(li, log.firstChild);
  batchQueue.push({ isbn: isbn, li: li });
  batchRefresh();
  batchPump();
  return false;
}

function batchPump() {
  if (batchBusy || !batchQueue.length) { return; }
  batchBusy = true;
  var job = batchQueue.shift();
  var key = job.isbn.replace(/[^0-9Xx]/g, "").toUpperCase();
  var body = new URLSearchParams();
  body.set("isbn", job.isbn);
  body.set("location", batchEl("batch-location").value.trim());
  if (batchSeen[key]) { body.set("again", "1"); }
  fetch("/catalogue/batch/add", { method: "POST", body: body, credentials: "same-origin" })
    .then(function (r) { if (!r.ok) { throw new Error(r.status); } return r.text(); })
    .then(function (html) {
      var t = document.createElement("template");
      t.innerHTML = html.trim();
      var row = t.content.firstElementChild;
      // An expired session answers with the sign-in page: no row, same as a fault.
      if (!row || !row.dataset.kind) { throw new Error("no row"); }
      if (row.dataset.kind === "ok") { batchSeen[key] = true; }
      batchPlace(job.li, row);
    })
    .catch(function () {
      job.li.className = "batch-row batch-aside";
      job.li.dataset.kind = "aside";
      job.li.dataset.isbn = job.isbn;
      batchPlace(job.li, job.li);
    })
    .then(function () {
      batchBusy = false;
      batchRefresh();
      batchPump();
    });
}

// A saved book takes the place of its line in the log; an aside one leaves it.
function batchPlace(pending, row) {
  if (row.dataset.kind === "aside") {
    // The same book twice is one line, to be filled in with two copies.
    var same = Array.prototype.find.call(batchEl("batch-aside-list").children, function (li) {
      return li !== row && li.dataset.isbn === row.dataset.isbn;
    });
    if (same) {
      same.dataset.copies = (parseInt(same.dataset.copies, 10) || 1) + 1;
      var times = same.querySelector(".batch-times");
      if (!times) {
        times = document.createElement("span");
        times.className = "batch-times";
        same.appendChild(times);
      }
      times.textContent = "×" + same.dataset.copies;
      pending.remove();
      return;
    }
    batchEl("batch-aside-list").appendChild(row);
    if (row !== pending) { pending.remove(); }
  } else {
    pending.replaceWith(row);
  }
}

function batchRefresh() {
  var codes = [];
  document.querySelectorAll("#batch-log [data-codes]").forEach(function (li) {
    codes = codes.concat(li.dataset.codes.split(","));
  });
  batchEl("batch-count").textContent = codes.length;
  var labels = batchEl("batch-labels");
  labels.hidden = codes.length === 0;
  labels.href = "/print/labels?codes=" + codes.join(",");
  batchEl("batch-done").hidden = batchEl("batch-log").children.length === 0;
  var aside = batchEl("batch-aside-list").children.length;
  batchEl("batch-aside").hidden = aside === 0;
  batchEl("batch-aside-count").textContent = aside;
  var working = batchBusy || batchQueue.length > 0;
  // The set-aside books wait for the others to be done.
  batchEl("batch-complete").disabled = working;
  // Leaving now would lose the ISBNs set aside, or the scans still in flight.
  window.onbeforeunload = (aside > 0 || working)
    ? function (e) { e.preventDefault(); e.returnValue = ""; }
    : null;
}

function batchComplete() {
  var li = batchEl("batch-aside-list").firstElementChild;
  if (!li || !window.htmx) { return; }
  batchCurrent = li;
  window.htmx.ajax("GET", "/catalogue/manual?batch=1&isbn=" + encodeURIComponent(li.dataset.isbn) +
    "&copies=" + (li.dataset.copies || 1) +
    "&location=" + encodeURIComponent(batchEl("batch-location").value.trim()),
    { target: "#batch-panel", swap: "innerHTML" });
}

// "Later": the book goes to the back of the line and the panel closes.
function batchClosePanel() {
  if (batchCurrent && batchCurrent.parentNode) {
    batchCurrent.parentNode.appendChild(batchCurrent);
  }
  batchCurrent = null;
  batchEl("batch-panel").innerHTML = "";
  refocusScan();
}

document.addEventListener("htmx:afterSwap", function (e) {
  var panel = batchEl("batch-panel");
  if (!panel || e.detail.target !== panel) { return; }
  var saved = panel.querySelector("[data-batch-saved]");
  if (!saved) {
    panel.scrollIntoView({ block: "nearest" });
    var title = panel.querySelector('[name="title"]');
    if (title) { title.focus(); }
    return;
  }
  var log = batchEl("batch-log");
  log.insertBefore(saved.querySelector("li"), log.firstChild);
  if (batchCurrent) { batchCurrent.remove(); }
  batchCurrent = null;
  panel.innerHTML = "";
  batchRefresh();
  if (batchEl("batch-aside-list").firstElementChild) { batchComplete(); } else { refocusScan(); }
});

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
