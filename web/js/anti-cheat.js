// Anti-cheat capture (spec §6.9): report minimize / blur / sleep while an
// attempt is running. Client-side flood control mirrors the server's 10 s
// collapse window: same-kind repeats are dropped, AND blur/minimize are one
// "exit" group — closing or minimizing the browser fires both listeners,
// while the server collapses only SAME-kind events, so sending both would
// log two rows for a single exit.
(function () {
  "use strict";
  var page = document.getElementById("workspace");
  if (!page) return;
  var code = page.getAttribute("data-code");
  if (!code || page.getAttribute("data-state") !== "started") return;

  var WINDOW = 10000; // ms — mirrors quizengine.CollapseWindow (spec §6.9)
  var EXIT_WINDOW = 1000; // ms — one physical exit fires visibilitychange +
    // blur + pagehide within milliseconds of each other, so only a burst
    // this tight is a single "exit". Anything further apart is a genuinely
    // distinct focus-loss event; swallowing it for a full 10 s undercounts
    // (the server's 10 s SAME-kind collapse still absorbs repeats).
  var lastReport = {}; // kind → Date.now() of the last accepted report
  var lastExit = 0; // Date.now() of the last accepted blur/minimize report
  var backgrounded = false; // the page hid since the last tick — a tick gap
    // spanning that period is background throttling, not machine sleep

  function report(kind) {
    var now = Date.now();
    // "minimize" = the page physically hid (visibilitychange/pagehide).
    // Record it BEFORE any debounce return: the hide happened even when this
    // POST is suppressed, and the sleep detector needs that fact.
    if (kind === "minimize") backgrounded = true;
    if (kind === "blur" || kind === "minimize") {
      if (now - lastExit < EXIT_WINDOW) return; // one exit burst → one report
    }
    if (lastReport[kind] && now - lastReport[kind] < WINDOW) return;
    lastReport[kind] = now;
    if (kind === "blur" || kind === "minimize") lastExit = now;
    fetch("/quiz/" + code + "/visibility", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      // keepalive: the request must survive the page being torn down right
      // after it is sent (tab/browser close) — otherwise the exit is lost.
      keepalive: true,
      body: JSON.stringify({ kind: kind })
    }).catch(function () {});
  }

  // exitAttempted: true once an exit-class report has been attempted since
  // the page was last seen visible (set by the hide/blur listeners below,
  // cleared by visibilitychange on a visible transition — which never
  // reports). It gates the `freeze` backstop so a frozen page can never add
  // a SECOND row for an exit these listeners already reported: state-based
  // source dedup per hide period, not a second time-window debounce.
  var exitAttempted = false;

  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      // hide (new tab, tab switch, window minimize, app recents): report.
      // The `document.hidden` guard is also the restore guard — the same
      // event fires with hidden=false when a tab is focused again or a
      // bfcache page returns, and that must never log a violation.
      exitAttempted = true;
      report("minimize");
    } else {
      exitAttempted = false;
    }
  });
  // Window-level blur: only a real browsing-context focus loss reaches this
  // (element `blur` does not bubble; `focusout` WOULD bubble from inner
  // inputs/options and false-positive on every in-page focus move).
  window.addEventListener("blur", function () {
    exitAttempted = true;
    report("blur");
  });
  // Last-resort exit signal: fires on tab/browser close even when the
  // blur/visibilitychange listeners were skipped; the exit-group debounce
  // keeps a single close to a single report.
  window.addEventListener("pagehide", function () {
    report("minimize");
  });
  // Page Lifecycle backstop (Chromium/Android WebView only; registering is
  // a harmless no-op on engines without it): `freeze` fires when a hidden
  // page is suspended — belt-and-braces for OEM WebViews where the
  // recents/app-switch transition delivered no usable
  // visibilitychange/blur. Freeze only ever happens on an already-hidden
  // page, never on a bfcache restore (restore is `pageshow`, which must
  // and does report nothing), and the exitAttempted gate skips it whenever
  // this hide period already attempted a report — so it adds a row only
  // when nothing else caught the exit, never a duplicate of one that did.
  window.addEventListener("freeze", function () {
    if (document.hidden && !exitAttempted) {
      exitAttempted = true;
      report("minimize");
    }
  });

  // sleep: a 30 s+ gap between 5 s ticks means the machine slept — but ONLY
  // when the page stayed visible throughout. Background tabs get their
  // timers throttled (≈1/min once hidden >5 min) or fully suspended
  // (mobile), so a gap that spans a hidden period is just a tab switch; the
  // minimize report already covers that, and posting "sleep" here would add
  // a spurious second (or repeated) violation for one focus-loss event.
  var lastTick = Date.now();
  setInterval(function () {
    var now = Date.now();
    var gap = now - lastTick;
    lastTick = now;
    if (document.hidden) {
      // Hidden right now (possibly the whole gap): never machine sleep —
      // exit reports cover this period.
      backgrounded = true;
      return;
    }
    if (backgrounded) {
      // First tick back after a hidden period: rebase the baseline instead
      // of reporting — the gap includes throttled/suspended background time.
      backgrounded = false;
      return;
    }
    if (gap > 30000) report("sleep");
  }, 5000);
})();
