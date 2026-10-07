"use strict";
// Student workspace client (spec §6.7): state machine, answer flow, timer,
// live ranking. Talks JSON to /quiz/:code endpoints and listens on the SSE
// stream for start / force_stop / snapshot / rank / ping.
(function () {
  var root = document.getElementById("workspace");
  if (!root) return;
  var state = root.getAttribute("data-state");

  var code = (location.pathname.split("/")[2] || "").trim();
  var streamURL = "/quiz/" + code + "/stream";

  // Set before every programmatic navigation this flow itself initiates
  // (submit-finish, SSE-driven repaints) so the exit guard never prompts
  // for a leave the state machine already decided on.
  var leaving = false;

  // Every frame's data field carries the {"type":…,"data":…} envelope the
  // hub publishes (the same wire shape monitor.js and the tests use) —
  // unwrap it so handlers compare against the payload itself.
  function frameData(ev) {
    var d = JSON.parse(ev.data);
    if (d && typeof d.type === "string" && d.data !== undefined) return d.data;
    return d;
  }

  function showErr(msg) {
    var m = msg || "Terjadi kesalahan. Silakan coba lagi.";
    if (window.quizToast) quizToast("danger", m);
    else console.error(m);
  }

  function post(url, payload) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: payload === undefined ? "" : JSON.stringify(payload)
    }).then(function (res) { return res.json(); });
  }

  // ---- live stream (everything but the finished view) --------------------
  var es = null;
  // The started-attempt section registers the cheat-alert surface through
  // this hook; idle pages (pending / waiting / ready) leave it null and
  // ignore cheat flags entirely.
  var surfaceFlag = null;
  if (state !== "finished") {
    es = new EventSource(streamURL);
    var reload = function () { leaving = true; es.close(); location.reload(); };
    es.addEventListener("start", reload);
    es.addEventListener("force_stop", reload);
    // A snapshot greets every connection AND is republished when state moves
    // behind us (restart / rehydrate / roster change). Repaint by reloading
    // only when it disagrees with the page we already rendered — a greeting
    // after a fresh load matches and must NOT reload (that would loop).
    es.addEventListener("snapshot", function (ev) {
      var d;
      try { d = frameData(ev); } catch (e) { return; }
      if (!d || !d.state) return;
      if (d.state !== root.getAttribute("data-state")) { reload(); return; }
      if (d.state === "started" && blob) {
        if (Number(d.current_q) > 0 && Number(d.current_q) !== Number(blob.current)) {
          reload(); return;
        }
        if (Number(d.ends_at) > 0 && Number(d.ends_at) !== Number(blob.ends_at)) {
          reload(); return;
        }
      }
      // The page still agrees with the server. This greeting's `cheating`
      // field was built from OUR participant row (a per-connection snapshot,
      // never fanned out): a student who joins or reloads an already-flagged
      // attempt sees the alert here. Surfacing runs no reload, so it cannot
      // loop (spec §6.9 — alert only, no lockout).
      if (surfaceFlag) surfaceFlag(d);
    });
    // Heartbeat carries the server clock ({server_now}) — re-sync the
    // countdown every beat so client drift cannot stretch or shrink the
    // attempt (spec §6.6 "resynced on every heartbeat"). The skew update
    // feeds serverNow() — the per-question countdown reads it too (C9).
    es.addEventListener("ping", function (ev) {
      if (!ev.data) return;
      try {
        var d = frameData(ev);
        if (!d || !d.server_now) return;
        skew = d.server_now * 1000 - Date.now();
        if (blob && blob.ends_at > 0) {
          deadline = Date.now() + (blob.ends_at - d.server_now) * 1000;
        }
      } catch (e) { /* keep the current deadline */ }
    });
  }

  var blob = null;
  var tag = document.getElementById("ws-data");
  if (tag) {
    try { blob = JSON.parse(tag.textContent); } catch (e) { blob = null; }
  }

  // ---- state shared by render() and the timer (C8/C9) --------------------
  // IIFE scope: render() runs at boot and must see initialized values no
  // matter which init path ran first.
  var previewOpen = false;  // pre-submit review open — pager stays hidden
  var perQ = false;         // per_soal with a per-question budget (C9)
  var qSince = 0;           // unix seconds the current question's budget started
  var timeupHandled = 0;    // position already handled by the expiry path
  // server clock − client clock (ms), seeded from the blob and re-synced on
  // every SSE ping (same skew pattern the attempt deadline uses)
  var skew = blob && blob.server_now ? blob.server_now * 1000 - Date.now() : 0;
  function serverNow() { return Date.now() + skew; }

  // ---- ready state: start form ------------------------------------------
  var startForm = document.getElementById("start-form");
  if (startForm) {
    startForm.addEventListener("submit", function (e) {
      e.preventDefault();
      post(startForm.action).then(function (body) {
        if (body.ok) location.reload();
        else showErr(body.message);
      }).catch(function () { showErr("Tidak dapat memulai kuis."); });
    });
  }

  // ---- timer -------------------------------------------------------------
  var timer = null;
  var deadline = 0;

  function startTimer() {
    // perQ (per_soal) shows THIS question's budget; the attempt-level cap
    // below keeps running in every mode (C9 — hard-cap finish stays)
    if (!blob || !(perQ || (blob.timer_on && blob.ends_at > 0))) return;
    timer = document.getElementById("timer");
    if (timer) timer.classList.remove("d-none");
    deadline = blob.ends_at > 0
      ? Date.now() + (blob.ends_at - blob.server_now) * 1000 : 0;
    var value = document.getElementById("timer-value");
    var tick = function () {
      // attempt-level hard cap: wall clock reached ends_at → close the attempt
      if (deadline > 0 && Date.now() >= deadline) {
        if (value) value.textContent = "00:00";
        finishAttempt();
        return;
      }
      var rem = deadline - Date.now();
      if (perQ) {
        // per-question countdown: per_question_seconds − (serverNow() − q_since)
        rem = Number(blob.per_question_seconds) * 1000 -
          (serverNow() - qSince * 1000);
        if (rem <= 0) {
          if (value) value.textContent = "00:00";
          handleTimeup(); // latched per question position — never spins
          setTimeout(tick, 250);
          return;
        }
      } else if (rem <= 0) {
        if (value) value.textContent = "00:00";
        finishAttempt(); // wall clock reached — close the attempt
        return;
      }
      var s = Math.ceil(rem / 1000);
      var m = Math.floor(s / 60);
      if (value) {
        value.textContent =
          (m < 10 ? "0" : "") + m + ":" + ((s % 60) < 10 ? "0" : "") + (s % 60);
      }
      setTimeout(tick, 250);
    };
    tick();
  }

  // ---- finished attempt --------------------------------------------------
  var finishing = false;
  function finishAttempt() {
    if (finishing || !blob) return;
    finishing = true;
    post(blob.finish_url).then(function (body) {
      if (body.ok) { leaving = true; location.reload(); return; }
      finishing = false;
      showErr(body.message);
    }).catch(function () {
      finishing = false;
      showErr(); // network failure — never swallow the submit silently
    });
  }

  if (state !== "started" || !blob) return;

  // ---- started state -----------------------------------------------------
  var linear = !!blob.linear;
  // per_soal with a per-question budget (tanpa_timer carries 0 → free nav)
  perQ = !linear && Number(blob.per_question_seconds || 0) > 0;
  // resumes the per-question countdown from the SSR blob; a missing q_since
  // (row predates the column) starts a full budget instead of instant expiry
  qSince = Number(blob.q_since || 0) || Math.floor(serverNow() / 1000);
  var total = blob.total;
  var current = blob.current;
  var busy = false;
  var cards = Array.prototype.slice.call(
    document.querySelectorAll(".ws-question"));

  function cardAt(pos) { // 1-based display position
    for (var i = 0; i < cards.length; i++) {
      if (parseInt(cards[i].getAttribute("data-index"), 10) === pos - 1) {
        return cards[i];
      }
    }
    return null;
  }

  function setDisabled(btn, off) {
    if (!btn) return;
    btn.disabled = off;
    btn.classList.toggle("disabled", off);
  }

  function render() {
    cards.forEach(function (c) {
      var idx = parseInt(c.getAttribute("data-index"), 10) + 1;
      // one question per page: every non-current section is hidden. Only
      // the section toggles — all answer inputs stay in the DOM.
      c.hidden = idx !== current;
      if (!linear) c.classList.toggle("border-primary", idx === current);
    });
    var next = document.getElementById("btn-next");
    var prev = document.getElementById("btn-prev");
    // linear auto-advances and never shows the pager; in review mode the
    // pager stays present (disabled only at the START bound, so the layout
    // does not jump) — SSR pins the last-question disabled class as the
    // static contract, but at runtime "Next" on the final question is the
    // entry into the pre-submit review, so it must stay clickable. The
    // pre-submit preview owns the view while open, and per_soal (perQ)
    // navigation is forward-only (C8/C9).
    if (next) { next.hidden = linear || previewOpen; setDisabled(next, false); }
    if (prev) { prev.hidden = linear || previewOpen || perQ; setDisabled(prev, current <= 1); }
  }

  // ---- answered bookkeeping: the preview grid reads it --------------------
  var answered = {};
  for (var qi = 0; qi < blob.questions.length; qi++) {
    if (blob.questions[qi].answered) answered[blob.questions[qi].id] = true;
  }

  // ---- essay autosave (spec §6.7) -----------------------------------------
  // Every keystroke schedules a save (300 ms debounce, same cadence as the
  // teacher search); saves for one card run through a promise chain, so a
  // burst can neither drop the last characters nor race itself.
  var essayState = {}; // qid -> {chain, saved, timer}

  function essayOf(card) {
    var qid = card.getAttribute("data-qid");
    if (!essayState[qid]) {
      essayState[qid] = { chain: Promise.resolve(), saved: null, timer: null };
    }
    return essayState[qid];
  }

  // after(state) receives "saved" | "same" | "skip" | "error" — callers
  // decide what each state may trigger (navigation, advance, …).
  function saveEssay(card, after) {
    var st = essayOf(card);
    st.chain = st.chain.then(function () {
      var ta = card.querySelector("textarea");
      if (!ta || !ta.value.trim()) return { state: "skip" }; // nothing to store
      if (st.saved === ta.value) return { state: "same" };   // already current
      var sent = ta.value;
      var qid = parseInt(card.getAttribute("data-qid"), 10);
      return post(blob.answer_url, { question_id: qid, answer: sent })
        .then(function (body) {
          if (!body.ok) { showErr(body.message); return { state: "error" }; }
          st.saved = sent;
          markAnswered(card);
          return { state: "saved" };
        })
        .catch(function () {
          showErr("Tidak dapat menyimpan jawaban.");
          return { state: "error" };
        });
    }).then(function (r) {
      if (r.state === "saved") onAnsweredChanged();
      if (after) after(r.state);
    });
    return st.chain;
  }

  function flushEssays() {
    cards.forEach(function (card) {
      if (card.getAttribute("data-type") !== "essay") return;
      var st = essayOf(card);
      clearTimeout(st.timer);
      saveEssay(card);
    });
  }

  // prefill saved answers from the server snapshot
  cards.forEach(function (card) {
    var qid = parseInt(card.getAttribute("data-qid"), 10);
    var q = null;
    for (var i = 0; i < blob.questions.length; i++) {
      if (blob.questions[i].id === qid) { q = blob.questions[i]; break; }
    }
    if (!q || !q.answered) return;
    if (q.type === "essay") {
      var ta = card.querySelector("textarea");
      if (ta && typeof q.given === "string") {
        ta.value = q.given;
        essayOf(card).saved = q.given; // already on the server — no re-save
      }
      return;
    }
    var givens = Array.isArray(q.given) ? q.given : [q.given];
    givens.forEach(function (orig) {
      var input = card.querySelector('[value="' + orig + '"]');
      if (input) input.checked = true;
    });
    var badge = card.querySelector("[data-q-status]");
    if (badge) badge.hidden = false;
  });

  function readPayload(card) {
    if (!card) return null;
    var qid = parseInt(card.getAttribute("data-qid"), 10);
    if (card.getAttribute("data-type") === "essay") {
      var ta = card.querySelector("textarea");
      if (!ta || !ta.value.trim()) return null;
      return { qid: qid, answer: ta.value };
    }
    var checked = card.querySelectorAll("input:checked");
    if (!checked.length) return null;
    var nums = [];
    for (var i = 0; i < checked.length; i++) {
      nums.push(Number(checked[i].value));
    }
    return { qid: qid, answer: nums };
  }

  function markAnswered(card) {
    var badge = card.querySelector("[data-q-status]");
    if (badge) badge.hidden = false;
    answered[parseInt(card.getAttribute("data-qid"), 10)] = true;
  }

  // The review grid follows every save; the FIRST time every question is
  // answered the preview opens by itself (spec §6.7 pre-submit review).
  function onAnsweredChanged() {
    if (previewOpen) { buildGrid(); return; }
    if (!autoPreviewed && allAnswered()) openPreview();
  }

  function allAnswered() {
    for (var i = 0; i < blob.questions.length; i++) {
      if (!answered[blob.questions[i].id]) return false;
    }
    return blob.questions.length > 0;
  }

  function showPreview(card, preview) {
    if (!preview || !card) return Promise.resolve();
    var box = card.querySelector("[data-preview]");
    if (!box) return Promise.resolve();
    box.textContent = preview.correct === true ? "Benar" : "Salah";
    return new Promise(function (resolve) {
      setTimeout(function () { box.textContent = ""; resolve(); }, 2500);
    });
  }

  function advance() {
    if (current < total) { current++; render(); }
  }

  // answer POST: preview (linear) → 2.5 s → auto-advance; no preview →
  // advance immediately (linear only).
  function submitCurrent() {
    var card = cardAt(current);
    var payload = readPayload(card);
    if (!payload || busy) return;
    busy = true;
    post(blob.answer_url,
      { question_id: payload.qid, answer: payload.answer })
      .then(function (body) {
        busy = false;
        if (!body.ok) { showErr(body.message); return; }
        markAnswered(card);
        if (!linear) return;
        var preview = body.data ? body.data.preview : null;
        var after = function () {
          if (current >= total) {
            // every question is answered in linear mode — the pre-submit
            // review replaces the old finish click, AFTER the feedback box
            onAnsweredChanged();
            openPreview();
          } else {
            advance();
          }
        };
        if (preview) showPreview(card, preview).then(after);
        else after();
      })
      .catch(function () {
        busy = false;
        showErr("Tidak dapat menyimpan jawaban.");
      });
  }

  // saves the card's payload in place (no navigation)
  function saveCard(card, onSaved) {
    var p = readPayload(card);
    if (!p || busy) return;
    busy = true;
    post(blob.answer_url, { question_id: p.qid, answer: p.answer })
      .then(function (body) {
        busy = false;
        if (!body.ok) { showErr(body.message); return; }
        markAnswered(card);
        onAnsweredChanged();
        if (onSaved) onSaved();
      })
      .catch(function () {
        busy = false;
        showErr("Tidak dapat menyimpan jawaban.");
      });
  }

  cards.forEach(function (card) {
    if (card.getAttribute("data-type") === "essay") {
      var ta = card.querySelector("textarea");
      if (!ta) return;
      // autosave on every character change — no save button anymore
      ta.addEventListener("input", function () {
        var st = essayOf(card);
        clearTimeout(st.timer);
        st.timer = setTimeout(function () { st.timer = null; saveEssay(card); }, 300);
      });
      // leaving the field flushes immediately (linear mode also advances,
      // the answer is already stored — same gate the old Save button had)
      ta.addEventListener("change", function () {
        var st = essayOf(card);
        clearTimeout(st.timer);
        saveEssay(card, function (state) {
          if (!linear) return;
          if (state === "saved" || state === "same") advanceAfterEssay();
        });
      });
      return;
    }
    card.addEventListener("change", function () {
      if (linear) submitCurrent();
      else saveCard(card); // review mode: save in place, navigate explicitly
    });
  });

  function advanceAfterEssay() {
    if (current >= total) openPreview();
    else advance();
  }

  // ---- navigation (review mode only) -------------------------------------
  function jumpTo(targetPos, targetQid) {
    if (busy || linear) return;
    if (targetPos < 1 || targetPos > total) return;
    var body = targetQid ? { question_id: targetQid } : {};
    busy = true;
    post(blob.next_url, body).then(function (resp) {
      busy = false;
      if (!resp.ok) { showErr(resp.message); return; }
      // preview belongs to the question being left
      if (resp.data && resp.data.preview) {
        showPreview(cardAt(current), resp.data.preview);
      }
      current = resp.data.current_q;
      // the advance landed: restart the per-question budget from the
      // server's new current_q_since and re-arm the expiry guard for the
      // new position (C9)
      var since = Number((resp.data && resp.data.current_q_since) ||
        resp.current_q_since || 0);
      // /next always stamps current_q_since = NOW server-side — without the
      // field in the response, restart the budget from the synced clock
      qSince = since > 0 ? since : Math.floor(serverNow() / 1000);
      timeupHandled = 0;
      render();
      var el = cardAt(current);
      if (el) el.scrollIntoView({ block: "nearest" });
    }).catch(function () { busy = false; showErr("Tidak dapat berpindah."); });
  }

  // ---- per-question expiry (C9) ------------------------------------------
  // Shown once when the current question's budget runs out on an unanswered
  // question; hidden again with the new question — Bootstrap modal only.
  function showTimeup() {
    var m = document.getElementById("timeup-modal");
    if (!m || !window.bootstrap) return;
    bootstrap.Modal.getOrCreateInstance(m).show();
  }
  function hideTimeup() {
    var m = document.getElementById("timeup-modal");
    if (!m || !window.bootstrap) return;
    bootstrap.Modal.getOrCreateInstance(m).hide();
  }

  // One action per question position: timeupHandled latches to the position
  // the moment an action starts and resets only when an advance lands, so a
  // stalled POST or the last question can never spin this path. A busy
  // navigation/save defers the action (unlatched) to the next tick instead
  // of dropping it. Only an ACTUALLY expired budget may act: the boot path
  // runs this on every refresh too, and a refresh while time remains must
  // never advance (C9); an attempt-level finish already in flight outranks
  // the per-question expiry.
  function handleTimeup() {
    if (!perQ || busy || finishing || timeupHandled === current) return;
    var rem = Number(blob.per_question_seconds) * 1000 -
      (serverNow() - qSince * 1000);
    if (rem > 0) return;
    timeupHandled = current;
    var card = cardAt(current);
    var qid = card ? parseInt(card.getAttribute("data-qid"), 10) : 0;
    var hasAnswer = !!((qid && answered[qid]) || (card && readPayload(card)));
    if (!hasAnswer) showTimeup(); // modal hides again with the new question
    timeupAdvance();
  }

  // advance through the same POST /next path jumpTo(current+1, 0) takes
  function timeupAdvance() {
    busy = true;
    post(blob.next_url, {}).then(function (resp) {
      busy = false;
      if (!resp.ok) { showErr(resp.message); return; } // no silent error (C9)
      var data = resp.data || {};
      var pos = Number(data.current_q || 0);
      if (!(pos > 0)) {
        showErr("Tidak dapat berpindah ke pertanyaan berikutnya.");
        return;
      }
      hideTimeup();
      if (pos === current) {
        // last question: the server clamps target == pos — review instead
        openPreview();
        return;
      }
      current = pos;
      var since = Number(data.current_q_since || resp.current_q_since || 0);
      qSince = since > 0 ? since : Math.floor(serverNow() / 1000);
      timeupHandled = 0; // advance landed — re-arm for the new position
      render();
      var el = cardAt(current);
      if (el) el.scrollIntoView({ block: "nearest" });
    }).catch(function () {
      busy = false;
      showErr("Tidak dapat berpindah ke pertanyaan berikutnya.");
    });
  }

  var nextBtn = document.getElementById("btn-next");
  if (nextBtn) {
    nextBtn.addEventListener("click", function () {
      // a disabled bound button must never move the view
      if (nextBtn.disabled || linear) return;
      var card = cardAt(current);
      var go = function () {
        // the end of the review queue is the pre-submit preview
        if (current >= total) openPreview();
        else jumpTo(current + 1, 0);
      };
      if (card && card.getAttribute("data-type") === "essay") {
        saveEssay(card, function (state) { if (state !== "error") go(); });
        return;
      }
      // save the current answer first, then advance
      var p = readPayload(card);
      if (p && !busy) saveCard(card, go);
      else if (!p) go();
    });
  }
  var prevBtn = document.getElementById("btn-prev");
  if (prevBtn) {
    prevBtn.addEventListener("click", function () {
      // a disabled bound button must never move the view
      if (prevBtn.disabled || linear) return;
      var target = cardAt(current - 1);
      var qid = target
        ? parseInt(target.getAttribute("data-qid"), 10) : 0;
      jumpTo(current - 1, qid);
    });
  }

  // ---- pre-submit preview (spec §6.7) ------------------------------------
  // previewOpen itself is module state (see the top of the IIFE — render()
  // reads it at boot, C8).
  var autoPreviewed = false;

  function beacon(page) {
    // best-effort monitor beacon: the review is a client-side view, so the
    // live monitor only learns about it through this report (never toast —
    // a closed quiz must not interrupt the student)
    post(blob.page_url, { page: page }).catch(function (e) {
      console.warn("page beacon failed:", e); // log only — never toast
    });
  }

  function previewParts() {
    return {
      questions: document.getElementById("questions"),
      pager: document.getElementById("ws-pager"),
      panel: document.getElementById("ws-preview")
    };
  }

  // one box per question: green = answered, red = not answered yet
  function buildGrid() {
    var grid = document.getElementById("preview-grid");
    var summary = document.getElementById("preview-summary");
    if (!grid) return;
    grid.textContent = "";
    var done = 0;
    blob.questions.forEach(function (q) {
      var ok = !!answered[q.id];
      if (ok) done++;
      var box = document.createElement("button");
      box.type = "button";
      box.className = "badge fs-6 px-3 py-2 border-0 " +
        (ok ? "text-bg-success" : "text-bg-danger");
      box.textContent = String(q.index + 1);
      box.title = "Pertanyaan " + (q.index + 1) + " — " +
        (ok ? "terjawab" : "belum terjawab");
      box.setAttribute("data-preview-q", q.id);
      grid.appendChild(box);
    });
    if (summary) {
      summary.textContent = done + " dari " + blob.questions.length + " terjawab";
    }
  }

  function openPreview() {
    if (previewOpen) return;
    previewOpen = true;
    autoPreviewed = true; // shown at least once — never hijack saves again
    buildGrid();
    var p = previewParts();
    if (p.questions) p.questions.hidden = true;
    if (p.pager) p.pager.hidden = true;
    if (p.panel) {
      p.panel.hidden = false;
      if (p.panel.scrollIntoView) p.panel.scrollIntoView({ block: "start" });
    }
    beacon("preview");
  }

  function closePreview() {
    if (!previewOpen) return;
    previewOpen = false;
    var p = previewParts();
    if (p.questions) p.questions.hidden = false;
    if (p.pager) p.pager.hidden = false;
    if (p.panel) p.panel.hidden = true;
    beacon("question");
  }

  var grid = document.getElementById("preview-grid");
  if (grid) {
    // a red box jumps straight to the question that still needs an answer
    grid.addEventListener("click", function (ev) {
      var box = ev.target && ev.target.closest
        ? ev.target.closest("[data-preview-q]") : null;
      if (!box || linear) return;
      var qid = parseInt(box.getAttribute("data-preview-q"), 10);
      var target = null;
      for (var i = 0; i < cards.length; i++) {
        if (parseInt(cards[i].getAttribute("data-qid"), 10) === qid) {
          target = cards[i];
          break;
        }
      }
      var targetPos = target
        ? parseInt(target.getAttribute("data-index"), 10) + 1 : 0;
      if (perQ && targetPos && targetPos < current) {
        // forward-only in per_soal (C9): no POST — the server 409s too (C7)
        if (window.quizToast) {
          quizToast("info", "Pertanyaan sebelumnya tidak dapat dibuka kembali.");
        }
        return; // stay in the review grid
      }
      closePreview();
      if (targetPos) jumpTo(targetPos, qid);
    });
  }

  var pvBack = document.getElementById("btn-preview-back");
  if (pvBack) {
    pvBack.addEventListener("click", function () {
      closePreview();
      var el = cardAt(current);
      if (el) el.scrollIntoView({ block: "nearest" });
    });
  }

  var pvSubmit = document.getElementById("btn-preview-submit");
  if (pvSubmit) {
    pvSubmit.addEventListener("click", function () {
      // manual submit is a decision point (auto-finish by timer expiry stays
      // unconfirmed — nothing is left to decide there)
      if (!window.quizConfirm) {
        console.error("confirmation unavailable — action skipped");
        return;
      }
      flushEssays(); // last keystrokes reach the server before /finish
      quizConfirm(
        "Kirim kuis sekarang? Kuis ini akan diakhiri dan jawaban Anda tidak dapat diubah.",
        finishAttempt);
    });
  }

  // ---- live ranking ------------------------------------------------------
  if (es) {
    es.addEventListener("rank", function (ev) {
      var data;
      try { data = frameData(ev); } catch (e) { return; }
      if (!data || !Array.isArray(data.ranking)) return;
      var panel = document.getElementById("rank-panel");
      var list = document.getElementById("rank-list");
      if (!panel || !list) return;
      panel.hidden = false;
      list.innerHTML = "";
      data.ranking.forEach(function (entry) {
        var li = document.createElement("li");
        li.className =
          "list-group-item d-flex justify-content-between align-items-center";
        var left = document.createElement("span");
        left.textContent = entry.name + (entry.cheating ? " · ditandai" : "");
        var right = document.createElement("span");
        right.className = "fw-semibold";
        right.textContent = Number(entry.score || 0).toFixed(2);
        li.appendChild(left);
        li.appendChild(right);
        list.appendChild(li);
      });
    });

    // ---- marked as cheating (spec §6.9 — alert, never a lockout) ---------
    // The teacher's cheat_toggle fans a `flagged` frame to the whole student
    // topic of this quiz, carrying the target participant_id: only the page
    // whose own attempt id matches reacts, classmates' clients stay silent.
    es.addEventListener("flagged", function (ev) {
      var d;
      try { d = frameData(ev); } catch (e) { return; }
      if (!d || d.cheating !== true || d.participant_id === undefined) return;
      if (String(d.participant_id) !== String(blob.participant_id)) return;
      showFlagged();
    });
  }

  // One alert per page session: dismissal sticks until a snapshot reports
  // the flag gone (teacher unflagged) — that re-arms the surface so a later
  // flag is heard again. Never reloads, so it cannot loop.
  var flaggedShown = false;
  function showFlagged() {
    if (flaggedShown) return;
    var m = document.getElementById("flagged-modal");
    if (!m || !window.bootstrap) return;
    flaggedShown = true;
    bootstrap.Modal.getOrCreateInstance(m).show();
  }
  surfaceFlag = function (d) {
    if (typeof d.cheating !== "boolean") return;
    if (!d.cheating) { flaggedShown = false; return; }
    showFlagged(); // greeting snapshot: our own row says we are flagged
  };

  // ---- exit guard (every timer mode: the started state is shared) --------
  // Leaving the page mid-attempt asks first. In-page navigation (brand
  // link, "back to dashboard", …) gets the Bootstrap confirmation modal;
  // tab close / refresh / back get the browser's own leave prompt (the one
  // dialog that cannot be styled). Programmatic navigations the flow itself
  // triggers — submit-finish, SSE repaints — set `leaving` first and never
  // prompt. The attempt itself stays open either way: the server keeps the
  // clock and the monitor learns of the exit through the stream lifecycle.
  window.addEventListener("beforeunload", function (e) {
    if (leaving) return;
    e.preventDefault();
    e.returnValue = "";
  });
  document.addEventListener("click", function (ev) {
    if (leaving) return;
    if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey ||
        (ev.button !== undefined && ev.button !== 0)) return;
    var a = ev.target && ev.target.closest ? ev.target.closest("a[href]") : null;
    if (!a) return;
    if (a.target === "_blank" || a.hasAttribute("download")) return;
    var href = a.getAttribute("href");
    if (!href || href.charAt(0) === "#") return;
    var url;
    try { url = new URL(href, location.href); } catch (e) { return; }
    if (url.origin !== location.origin) return; // external link: browser's own
    if (url.pathname === location.pathname && url.search === location.search) {
      return; // same page — not a leave
    }
    ev.preventDefault();
    if (!window.quizConfirm) {
      console.error("confirmation unavailable — action skipped");
      return;
    }
    quizConfirm(
      "Anda masih mengerjakan kuis ini. Tinggalkan halaman? Kuis tetap terbuka dan Anda dapat kembali lagi.",
      function () {
        leaving = true;
        location.href = url.href;
      });
  });

  // ---- boot --------------------------------------------------------------
  render();
  startTimer();
  // refresh mid-question: the SSR blob's q_since may already be spent —
  // run the expiry check on boot too (same one-action-per-position guard)
  if (perQ) handleTimeup();
})();
