// Teacher CRUD helpers: fetch-submit for forms and one-shot POST buttons.
// The server answers with {"ok":true,"data":...} or
// {"ok":false,"error":CODE,"message":...}. Success stashes the data-success
// toast flash before the reload/redirect; failures raise a Bootstrap Toast.
// Confirmations go through the Bootstrap Modal (window.quizConfirm) —
// browser alert()/confirm() are banned (see web/js/ui.js).
function toastFailure(message) {
  if (window.quizToast) quizToast("danger", message || "Request failed. Please try again.");
}
function confirmThen(message, onYes) {
  if (!window.quizConfirm) {
    console.error("confirmation unavailable — action skipped");
    return;
  }
  quizConfirm(message, onYes);
}
function storeSuccess(message) {
  if (window.quizStoreFlash) quizStoreFlash("success", message);
}

async function runFetch(form) {
  var btn = form.querySelector("[type=submit]");
  if (btn) btn.disabled = true;
  var opts = { method: form.method || "POST" };
  var fd = new FormData(form);
  if (form.dataset.json) {
    opts.headers = { "Content-Type": "application/json" };
    opts.body = JSON.stringify(Object.fromEntries(fd));
  } else {
    opts.body = fd;
  }
  var resp, body;
  try {
    resp = await fetch(form.action, opts);
    body = await resp.json().catch(function () { return {}; });
  } catch (e) {
    if (btn) btn.disabled = false;
    toastFailure("Network error — please try again.");
    return;
  }
  if (resp.ok && body.ok) {
    storeSuccess(successMessage(form.dataset.success, fd));
    if (form.dataset.nextId && body.data && body.data[form.dataset.nextId]) {
      location.href = form.dataset.next + body.data[form.dataset.nextId];
    } else if (form.dataset.next) {
      location.href = form.dataset.next;
    } else {
      location.reload();
    }
    return;
  }
  if (btn) btn.disabled = false;
  toastFailure(body.message);
}

// successMessage makes create/update toasts name the entity: "{field}"
// tokens in data-success are filled from the submitted form fields
// (e.g. data-success='Quiz "{judul}" created successfully.'). Tokens whose
// field is empty disappear together with their quotes.
function successMessage(template, fd) {
  if (!template) return template;
  return template
    .replace(/"?\{(\w+)\}"?/g, function (m, key) {
      var v = fd.get(key);
      return v ? '"' + v + '"' : "";
    })
    .replace(/ {2,}/g, " ")
    .trim();
}

document.addEventListener("submit", async function (ev) {
  var form = ev.target;
  if (!form.matches || !form.matches("form[data-fetch]")) return;
  ev.preventDefault();
  if (form.dataset.confirm !== undefined) {
    confirmThen(form.dataset.confirm, function () { runFetch(form); });
    return;
  }
  runFetch(form);
});

async function runPost(btn) {
  btn.disabled = true;
  var resp, body;
  try {
    resp = await fetch(btn.dataset.post, { method: "POST" });
    body = await resp.json().catch(function () { return {}; });
  } catch (e) {
    btn.disabled = false;
    toastFailure("Network error — please try again.");
    return;
  }
  if (resp.ok && body.ok) {
    storeSuccess(btn.dataset.success);
    if (btn.dataset.next) {
      location.href = btn.dataset.next;
    } else {
      location.reload();
    }
    return;
  }
  btn.disabled = false;
  toastFailure(body.message);
}

document.addEventListener("click", function (ev) {
  var btn = ev.target.closest ? ev.target.closest("button[data-post]") : null;
  if (!btn) return;
  if (btn.dataset.confirm !== undefined) {
    confirmThen(btn.dataset.confirm, function () { runPost(btn); });
    return;
  }
  runPost(btn);
});

// --- quiz manage page: question table + bank modal + share modal -----------
function questionTypeLabel(type) {
  return type === "pg" ? "Single choice" : (type === "multi" ? "Multiple choice" : "Essay");
}

// bumpQuestionCount keeps the tab badge and the toolbar count in sync after
// an add — only the trailing "(N)" text node is touched, never the icon.
function bumpQuestionCount() {
  var count = document.getElementById("q-count");
  if (count) count.textContent = String((parseInt(count.textContent, 10) || 0) + 1);
  var tab = document.querySelector('.nav-link[data-bs-target="#tab-questions"]');
  if (!tab) return;
  var node = null;
  for (var i = 0; i < tab.childNodes.length; i++) {
    if (tab.childNodes[i].nodeType === 3) node = tab.childNodes[i];
  }
  if (!node) return;
  node.nodeValue = node.nodeValue.replace(/\((\d+)\)\s*$/, function (m, n) {
    return "(" + (parseInt(n, 10) + 1) + ")";
  });
}

function markAdded(item) {
  if (!item) return;
  item.dataset.added = "1";
  item.dataset.adding = "";
  item.disabled = true;
  item.setAttribute("aria-disabled", "true");
  if (!item.querySelector(".badge")) {
    var badge = document.createElement("span");
    badge.className = "badge text-bg-secondary";
    badge.textContent = "In quiz";
    item.appendChild(badge);
  }
}

// appendQuestionRow adds the SSR-template row for a freshly added question.
// Server-derived text goes in via textContent only — never innerHTML.
function appendQuestionRow(item) {
  var tpl = document.getElementById("question-row-tpl");
  var tbody = document.getElementById("q-body");
  if (!tpl || !tbody || !item) return;
  var rows = tbody.querySelectorAll("tr[data-seq]");
  var lastSeq = rows.length ? parseInt(rows[rows.length - 1].getAttribute("data-seq"), 10) || 0 : 0;
  var seq = lastSeq + 1;
  var row = document.importNode(tpl.content, true).querySelector("tr");
  if (!row) return;
  row.setAttribute("data-seq", String(seq));
  row.setAttribute("data-qid", item.dataset.addQ || "");
  row.querySelector(".q-seq").textContent = String(seq);
  row.querySelector(".q-teks").textContent = item.dataset.teks || "";
  row.querySelector(".q-type").textContent = questionTypeLabel(item.dataset.type);
  var modal = document.getElementById("bankModal");
  var endpoint = modal ? modal.dataset.endpoint : "";
  var actions = row.querySelector(".q-actions");
  // order buttons only on the unfiltered list (same rule as the SSR rows)
  if (reorderEndpoint()) {
    actions.appendChild(moveButton("up"));
    actions.appendChild(moveButton("down"));
  }
  var btn = document.createElement("button");
  btn.type = "button";
  btn.className = "btn btn-sm btn-outline-danger";
  btn.setAttribute("data-post", endpoint + "/" + item.dataset.addQ + "/delete");
  btn.setAttribute("data-confirm", "Remove this question from the quiz?");
  btn.setAttribute("data-success", "Question removed from the quiz.");
  btn.setAttribute("title", "Remove from quiz");
  btn.setAttribute("aria-label", "Remove from quiz");
  btn.setAttribute("data-bs-toggle", "tooltip");
  btn.setAttribute("data-bs-placement", "bottom");
  var icon = document.createElement("i");
  icon.className = "bi bi-trash3";
  icon.setAttribute("aria-hidden", "true");
  btn.appendChild(icon);
  actions.appendChild(btn);
  var empty = document.getElementById("q-empty");
  if (empty) empty.classList.add("d-none");
  var card = document.getElementById("q-card");
  if (card) card.classList.remove("d-none");
  tbody.appendChild(row);
  syncMoveButtons();
  if (window.quizTooltips) quizTooltips(row);
}

// --- question order: up/down buttons ---------------------------------------

// reorderEndpoint is the POST target for the composed order — set on
// #q-card ONLY when the full (unfiltered) list is rendered, because a
// ?qq= slice could never post a complete order.
function reorderEndpoint() {
  var card = document.getElementById("q-card");
  return card ? card.dataset.reorderEndpoint || "" : "";
}

// moveButton builds one order button for a JS-added row (SSR rows carry
// theirs in the template).
function moveButton(dir) {
  var btn = document.createElement("button");
  btn.type = "button";
  btn.className = "btn btn-sm btn-outline-secondary";
  btn.setAttribute("data-move", dir);
  btn.setAttribute("title", dir === "up" ? "Move up" : "Move down");
  btn.setAttribute("aria-label", dir === "up" ? "Move question up" : "Move question down");
  btn.setAttribute("data-bs-toggle", "tooltip");
  btn.setAttribute("data-bs-placement", "bottom");
  var icon = document.createElement("i");
  icon.className = "bi " + (dir === "up" ? "bi-chevron-up" : "bi-chevron-down");
  icon.setAttribute("aria-hidden", "true");
  btn.appendChild(icon);
  return btn;
}

// syncMoveButtons disables the boundary buttons: the first row cannot move
// up, the last row cannot move down.
function syncMoveButtons() {
  var tbody = document.getElementById("q-body");
  if (!tbody) return;
  var rows = tbody.querySelectorAll("tr[data-seq]");
  for (var i = 0; i < rows.length; i++) {
    var up = rows[i].querySelector('[data-move="up"]');
    var down = rows[i].querySelector('[data-move="down"]');
    if (up) up.disabled = i === 0;
    if (down) down.disabled = i === rows.length - 1;
  }
}

// wireQuestionReorder makes the up/down buttons POST the new order:
// OPTIMISTIC (the rows swap and the # column renumbers before the request),
// then rolled back to the snapshotted order and labels when the server
// rejects or the network fails — the DB stays the source of truth.
(function wireQuestionReorder() {
  var tbody = document.getElementById("q-body");
  if (!tbody || !reorderEndpoint()) return; // filtered or no table at all

  function rows() {
    return Array.prototype.slice.call(tbody.querySelectorAll("tr[data-seq]"));
  }

  function snapshot() {
    return rows().map(function (row) {
      var cell = row.querySelector(".q-seq");
      return { row: row, seq: cell ? cell.textContent : "" };
    });
  }

  // relayout re-appends rows in the given order and rewrites each # cell
  // from that entry's label (the pre-save labels on a rollback).
  function relayout(entries) {
    var i;
    for (i = 0; i < entries.length; i++) tbody.appendChild(entries[i].row);
    for (i = 0; i < entries.length; i++) {
      var cell = entries[i].row.querySelector(".q-seq");
      if (cell) cell.textContent = entries[i].seq;
      entries[i].row.setAttribute("data-seq", entries[i].seq);
    }
    syncMoveButtons();
  }

  function renumber() {
    var list = rows();
    for (var i = 0; i < list.length; i++) {
      var cell = list[i].querySelector(".q-seq");
      if (cell) cell.textContent = String(i + 1);
      list[i].setAttribute("data-seq", String(i + 1));
    }
    syncMoveButtons();
  }

  function setBusy(busy) {
    var buttons = tbody.querySelectorAll("button[data-move]");
    for (var i = 0; i < buttons.length; i++) buttons[i].disabled = busy;
  }

  var busy = false;
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest ? ev.target.closest("button[data-move]") : null;
    if (!btn || busy || !tbody.contains(btn)) return;
    var row = btn.closest("tr[data-seq]");
    if (!row) return;
    var list = rows();
    var i = list.indexOf(row);
    var target = btn.getAttribute("data-move") === "up" ? i - 1 : i + 1;
    if (target < 0 || target >= list.length) return;

    var before = snapshot(); // order + labels to restore on failure
    if (target < i) tbody.insertBefore(row, list[target]);
    else tbody.insertBefore(list[target], row);
    renumber();
    busy = true;
    setBusy(true);
    var ids = rows().map(function (r) { return Number(r.getAttribute("data-qid")); });

    fetch(reorderEndpoint(), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question_ids: ids })
    })
      .then(function (resp) {
        return resp.json().catch(function () { return {}; }).then(function (body) {
          return { resp: resp, body: body };
        });
      })
      .then(function (r) {
        busy = false;
        setBusy(false);
        if (!r.resp.ok || !r.body.ok) {
          relayout(before);
          toastFailure(r.body.message || "Could not save the question order.");
          return;
        }
        syncMoveButtons();
      })
      .catch(function () {
        busy = false;
        setBusy(false);
        relayout(before);
        toastFailure("Network error — please try again.");
      });
  });

  syncMoveButtons();
})();

// --- results page: expandable per-student answer panel ---------------------

// One delegated handler: the rows are re-rendered by the toolbar live
// search, so per-element wiring would not survive a swap.
document.addEventListener("click", function (ev) {
  var btn = ev.target.closest ? ev.target.closest("button[data-answer-toggle]") : null;
  if (!btn) return;
  var host = btn.closest("tr");
  if (!host || !host.nextElementSibling) return;
  var hidden = host.nextElementSibling.classList.toggle("d-none");
  btn.setAttribute("aria-expanded", hidden ? "false" : "true");
  var icon = btn.querySelector(".bi");
  if (icon) icon.className = "bi " + (hidden ? "bi-chevron-right" : "bi-chevron-down");
});

// addQuestion posts one picked question immediately (JSON envelope). The
// modal stays open; success appends the table row in place.
async function addQuestion(item) {
  if (!item || item.dataset.added === "1" || item.dataset.adding === "1") return;
  item.dataset.adding = "1";
  var modal = document.getElementById("bankModal");
  var endpoint = modal ? modal.dataset.endpoint : "";
  if (!endpoint) {
    item.dataset.adding = "";
    toastFailure("Network error — please try again.");
    return;
  }
  var resp, body;
  try {
    resp = await fetch(endpoint, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question_ids: [Number(item.dataset.addQ)], length: "" })
    });
    body = await resp.json().catch(function () { return {}; });
  } catch (e) {
    item.dataset.adding = "";
    toastFailure("Network error — please try again.");
    return;
  }
  if (resp.ok && body.ok) {
    markAdded(item);
    appendQuestionRow(item);
    bumpQuestionCount();
    if (window.quizToast) quizToast("success", "Question added to this quiz.");
    return;
  }
  if (resp.status === 409) {
    // already in the quiz — reconcile the row state, then show the server's reason
    markAdded(item);
    toastFailure(body.message);
    return;
  }
  item.dataset.adding = "";
  toastFailure(body.message);
}

// bucketOf reads the server-rendered data-len (bucket name or raw length)
// and falls back to measuring data-teks. Buckets mirror the bank filter:
// <80 short, 80–200 medium, >200 long.
function bucketOf(item) {
  var raw = item.dataset.len;
  if (raw === "short" || raw === "medium" || raw === "long") return raw;
  var n = raw ? parseInt(raw, 10) : NaN;
  if (isNaN(n)) n = (item.dataset.teks || "").length;
  return n < 80 ? "short" : (n <= 200 ? "medium" : "long");
}

var bankActiveLen = "";

// filterBankList applies the client-side LENGTH filter only — the ?bq= text
// search is server-side (attachLiveSearch), and a live-search swap of the
// list re-enters through afterSwap so the active length keeps applying.
// Runs on length-button clicks and on every modal show.
function filterBankList() {
  var list = document.getElementById("bank-list");
  if (!list) return;
  var items = list.querySelectorAll("[data-add-q]");
  var visible = 0;
  for (var i = 0; i < items.length; i++) {
    var match = !bankActiveLen || bucketOf(items[i]) === bankActiveLen;
    items[i].classList.toggle("d-none", !match);
    if (match) visible++;
  }
  var noMatch = document.getElementById("bank-no-match");
  if (noMatch) noMatch.classList.toggle("d-none", !(items.length > 0 && visible === 0));
}

// --- SSR live search: debounced fetch + region swap ------------------------

// buildLiveSearchURL is pure: it merges the form's hidden inputs (the other
// page params) with the search param, drops the param entirely when the
// value trims to empty (the server then returns the full list), and keeps
// the location hash (quiz tabs) on the result.
function buildLiveSearchURL(form, param, value) {
  var url = new URL(form.getAttribute("action"), location.href);
  var params = new URLSearchParams();
  var hidden = form.querySelectorAll("input[type=hidden]");
  for (var i = 0; i < hidden.length; i++) {
    if (hidden[i].name) params.append(hidden[i].name, hidden[i].value);
  }
  var v = (value || "").trim();
  if (v) params.set(param, v);
  else params.delete(param);
  url.search = params.toString();
  return url.pathname + url.search + location.hash;
}

// attachLiveSearch wires a debounced (300 ms) server-side search onto a
// form input. The server response is the sole source of rows: each fetch
// pulls the full page, parses it, and swaps only the configured regions
// (innerHTML for containers, textContent for counts), syncs the toolbar's
// server-driven "Reset filters" link, and replaces the URL in place. A
// monotonic sequence drops out-of-order responses. Native submit is
// intercepted and runs the same fetch immediately; without JS the form is
// still a plain GET that reloads the page with the filter applied.
function attachLiveSearch(input, opts) {
  if (!input) return;
  var form = opts.form || input.form;
  if (!form) return;
  var param = opts.param;
  var swap = opts.swap || [];
  var afterSwap = opts.afterSwap;
  var seq = 0;
  var timer = null;

  function applyRegions(doc) {
    for (var i = 0; i < swap.length; i++) {
      var s = swap[i];
      if (s.count) {
        var src = doc.querySelector(s.count);
        var dst = document.querySelector(s.count);
        if (src && dst) {
          dst.textContent = src.textContent;
        } else if (src && s.parent) {
          var host = document.querySelector(s.parent);
          if (host) host.appendChild(document.importNode(src, true));
        } else if (!src && dst) {
          dst.parentNode.removeChild(dst);
        }
      } else if (s.from) {
        var region = doc.querySelector(s.from);
        var target = document.querySelector(s.to);
        if (target) target.innerHTML = region ? region.innerHTML : "";
      }
    }
    syncResetLink(doc);
    if (afterSwap) afterSwap();
  }

  // The Reset-filters anchor appears/disappears server-driven; the toolbar
  // itself never swaps because the focused input lives inside it.
  function syncResetLink(doc) {
    var srcBar = doc.querySelector(".table-toolbar");
    var dstBar = document.querySelector(".table-toolbar");
    if (!srcBar || !dstBar) return;
    var srcForm = srcBar.querySelector("form");
    var dstForm = dstBar.querySelector("form");
    if (!srcForm || !dstForm) return;
    var srcLink = srcForm.querySelector("a.btn-link");
    var dstLink = dstForm.querySelector("a.btn-link");
    if (srcLink && !dstLink) {
      dstForm.appendChild(document.importNode(srcLink, true));
    } else if (!srcLink && dstLink) {
      dstLink.parentNode.removeChild(dstLink);
    } else if (srcLink && dstLink) {
      dstLink.setAttribute("href", srcLink.getAttribute("href"));
    }
  }

  function run() {
    var mySeq = ++seq;
    var url = buildLiveSearchURL(form, param, input.value);
    fetch(url)
      .then(function (resp) { return resp.text(); })
      .then(function (html) {
        if (mySeq !== seq) return; // a newer request owns the DOM
        applyRegions(new DOMParser().parseFromString(html, "text/html"));
        try { history.replaceState(null, "", url); } catch (e) { /* ignore */ }
      })
      .catch(function () { /* network hiccup — the native form still works */ });
  }

  input.addEventListener("input", function () {
    clearTimeout(timer);
    timer = setTimeout(function () { timer = null; run(); }, 300);
  });
  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    clearTimeout(timer);
    timer = null;
    run();
  });
}

(function wireQuizManage() {
  // bank modal: one POST per pick
  var bankList = document.getElementById("bank-list");
  if (bankList) {
    bankList.addEventListener("click", function (ev) {
      var el = ev.target.closest ? ev.target.closest("[data-add-q]") : null;
      if (!el || el.dataset.added === "1") return;
      addQuestion(el);
    });
  }
  // bank modal: SSR live search (?bq= fetch + #bank-results swap) plus the
  // client-side length filter (state is kept when the modal is re-opened;
  // the length filter re-runs after every swap and on every show)
  attachLiveSearch(document.getElementById("bank-search"), {
    param: "bq",
    swap: [{ from: "#bank-results", to: "#bank-results" }],
    afterSwap: filterBankList
  });
  var lenBtns = document.querySelectorAll("[data-bank-len]");
  for (var bi = 0; bi < lenBtns.length; bi++) {
    lenBtns[bi].addEventListener("click", function (ev) {
      var btn = ev.currentTarget;
      bankActiveLen = btn.getAttribute("data-bank-len") || "";
      for (var k = 0; k < lenBtns.length; k++) {
        var on = (lenBtns[k].getAttribute("data-bank-len") || "") === bankActiveLen;
        lenBtns[k].className = "btn " + (on ? "btn-primary" : "btn-outline-secondary");
      }
      filterBankList();
    });
  }
  var bankModal = document.getElementById("bankModal");
  if (bankModal) bankModal.addEventListener("show.bs.modal", filterBankList);
  // questions tab: SSR live search (?qq=) — swaps the results region and
  // syncs the filtered count; the input stays outside the swapped node
  attachLiveSearch(document.getElementById("q-search"), {
    param: "qq",
    swap: [
      { from: "#q-results", to: "#q-results" },
      { count: "#q-count" }
    ]
  });
  // toolbar search on guru table pages (?q=): swaps #table-results + count
  var tableSearch = document.getElementById("table-search");
  if (tableSearch && tableSearch.form && tableSearch.form.hasAttribute("data-live-search")) {
    attachLiveSearch(tableSearch, {
      param: "q",
      swap: [
        { from: "#table-results", to: "#table-results" },
        { count: "#table-count", parent: ".table-toolbar" }
      ]
    });
  }
  // share modal: copy link / copy code
  var copyUrlBtn = document.getElementById("share-copy-url");
  if (copyUrlBtn) {
    copyUrlBtn.addEventListener("click", function () {
      var input = document.getElementById("share-url");
      if (input) copyText(input.value, input);
    });
  }
  var copyCodeBtn = document.getElementById("share-copy-code");
  if (copyCodeBtn) {
    copyCodeBtn.addEventListener("click", function () {
      var code = document.getElementById("share-code");
      if (code) copyText(code.textContent, null);
    });
  }
})();

function copyFailed(input) {
  if (window.quizToast) quizToast("danger", "Copy failed — select the text and copy manually.");
  if (input) {
    try { input.focus(); input.select(); } catch (e) { /* selection unavailable */ }
  }
}

function copyText(value, input) {
  try {
    if (!navigator.clipboard || !navigator.clipboard.writeText) throw new Error("clipboard unavailable");
    navigator.clipboard.writeText(value).then(function () {
      if (window.quizToast) quizToast("success", "Copied to clipboard.");
    }, function () { copyFailed(input); });
  } catch (e) {
    copyFailed(input);
  }
}

// --- tab persistence: keep the open tab across save/reload -----------------
document.addEventListener("shown.bs.tab", function (ev) {
  var el = ev.target;
  if (!el || !el.getAttribute) return;
  var target = el.getAttribute("data-bs-target");
  if (!target) return;
  var tabs = document.getElementById("quiz-tabs");
  if (!tabs || !tabs.contains(el)) return;
  try { history.replaceState(null, "", location.pathname + location.search + target); } catch (e) { /* ignore */ }
});

function activateTabFromHash() {
  try {
    if (!window.bootstrap || !bootstrap.Tab) return;
    var tabs = document.getElementById("quiz-tabs");
    if (!tabs || !location.hash) return;
    var btn = tabs.querySelector('[data-bs-target="' + location.hash + '"]');
    if (!btn) return;
    bootstrap.Tab.getOrCreateInstance(btn).show();
  } catch (e) { /* stale or malformed hash — stay on the default tab */ }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", activateTabFromHash);
} else {
  activateTabFromHash();
}
