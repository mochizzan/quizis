// Shared UI feedback — Bootstrap-only, SSR-first:
//   * quizToast(kind, message)   → Bootstrap Toast (bottom-right stack)
//   * quizConfirm(message, onYes[, tone]) → Bootstrap Modal confirmation;
//     browser confirm()/alert() are banned app-wide, and a missing modal
//     never falls back to them — the action is skipped instead.
//   * quizStoreFlash(kind, message) → stashes a message in sessionStorage
//     right before a reload/redirect, consumed on the next page load so a
//     success Toast survives the round trip.
(function () {
  "use strict";

  var FLASH_KEY = "quiz.flash";
  var SUCCESS = "success";
  var DANGER = "danger";

  function kindOf(kind) { return kind === SUCCESS ? SUCCESS : DANGER; }
  function titleOf(kind) { return kind === SUCCESS ? "Success" : "Error"; }
  function delayOf(kind) { return kind === SUCCESS ? 4000 : 6000; } // success / error

  function buildToast(kind, message) {
    var el = document.createElement("div");
    el.className = "toast";
    el.setAttribute("role", "alert");
    el.setAttribute("aria-live", "assertive");
    el.setAttribute("aria-atomic", "true");

    var header = document.createElement("div");
    header.className = "toast-header text-bg-" + kind;
    var title = document.createElement("strong");
    title.className = "me-auto";
    title.textContent = titleOf(kind);
    var close = document.createElement("button");
    close.type = "button";
    close.className = "btn-close";
    close.setAttribute("data-bs-dismiss", "toast");
    close.setAttribute("aria-label", "Close");
    header.appendChild(title);
    header.appendChild(close);

    var body = document.createElement("div");
    body.className = "toast-body";
    body.textContent = message; // textContent — server copy is never markup

    el.appendChild(header);
    el.appendChild(body);
    return el;
  }

  function showToast(kind, message) {
    var stack = document.getElementById("toast-stack");
    if (!stack || !window.bootstrap || !message) return;
    kind = kindOf(kind);
    var el = buildToast(kind, message);
    stack.appendChild(el);
    el.addEventListener("hidden.bs.toast", function () { el.remove(); });
    bootstrap.Toast.getOrCreateInstance(el, { autohide: true, delay: delayOf(kind) }).show();
  }
  window.quizToast = showToast;

  // --- Bootstrap tooltips on icon-only controls ---------------------------
  // Two hooks, because data-bs-toggle is single-purpose in HTML:
  //   [data-bs-toggle="tooltip"]  — element has no other data-bs-toggle use
  //   [data-quiz-tooltip]         — element already triggers a component
  //                                 (offcanvas/modal/collapse)
  // Both need a title=… (tooltip content) plus aria-label (screen readers).
  window.quizTooltips = function (scope) {
    if (!window.bootstrap || !window.bootstrap.Tooltip) return;
    var nodes = (scope || document).querySelectorAll(
      '[data-bs-toggle="tooltip"], [data-quiz-tooltip]');
    for (var i = 0; i < nodes.length; i++) {
      bootstrap.Tooltip.getOrCreateInstance(nodes[i]);
    }
  };

  window.quizStoreFlash = function (kind, message) {
    if (!message) return;
    try {
      sessionStorage.setItem(FLASH_KEY,
        JSON.stringify({ kind: kindOf(kind), message: message }));
    } catch (e) {
      /* storage unavailable — the success toast is simply skipped */
    }
  };

  // --- Bootstrap Modal confirmation ---------------------------------------
  var pendingConfirm = null;

  window.quizConfirm = function (message, onYes, tone) {
    var modalEl = document.getElementById("confirm-modal");
    var body = document.getElementById("confirm-modal-body");
    var ok = document.getElementById("confirm-modal-ok");
    if (!modalEl || !body || !ok || !window.bootstrap) {
      console.error("quizConfirm: confirmation modal missing — action skipped");
      return;
    }
    body.textContent = message || "Are you sure?";
    ok.className = "btn btn-" + (tone === "primary" ? "primary" : "danger");
    pendingConfirm = onYes || null;
    bootstrap.Modal.getOrCreateInstance(modalEl).show();
  };

  function init() {
    // server-rendered toast (handler set .Flash)
    if (window.bootstrap) {
      var ssr = document.querySelectorAll(".toast[data-ssr-toast]");
      for (var i = 0; i < ssr.length; i++) {
        var delay = +(ssr[i].getAttribute("data-bs-delay") || 4000);
        bootstrap.Toast.getOrCreateInstance(ssr[i], { autohide: true, delay: delay }).show();
      }
      // icon-only controls render with their tooltip markup on first paint
      window.quizTooltips();
      // server-rendered notice modal (handler set data-ssr-modal — e.g. the
      // dashboard's ongoing-quiz notice right after sign-in)
      var ssrModals = document.querySelectorAll(".modal[data-ssr-modal]");
      for (var j = 0; j < ssrModals.length; j++) {
        bootstrap.Modal.getOrCreateInstance(ssrModals[j]).show();
      }
    }

    // flash stashed by the previous page (before its reload/redirect)
    try {
      var raw = sessionStorage.getItem(FLASH_KEY);
      if (raw) {
        sessionStorage.removeItem(FLASH_KEY);
        var f = JSON.parse(raw);
        if (f && f.message) showToast(f.kind, f.message);
      }
    } catch (e) { /* ignore malformed flash */ }

    // confirm modal wiring (one instance per page)
    var modalEl = document.getElementById("confirm-modal");
    if (!modalEl || !window.bootstrap) return;
    var ok = document.getElementById("confirm-modal-ok");
    ok.addEventListener("click", function () {
      bootstrap.Modal.getOrCreateInstance(modalEl).hide();
      var fn = pendingConfirm;
      pendingConfirm = null;
      if (fn) fn();
    });
    modalEl.addEventListener("hidden.bs.modal", function () { pendingConfirm = null; });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
