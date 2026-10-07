// Live teacher monitor (spec §6.3, §6.9, §7): repaints the waiting room,
// student cards and ranking from SSE, drives the roster/close actions, and
// re-syncs both countdowns on every heartbeat.
(function () {
  "use strict";
  var root = document.getElementById("monitor");
  if (!root) return;
  var quizID = root.getAttribute("data-quiz-id");

  // --- state: page blob uses lowercase keys, snapshots use template keys --
  function normalize(d) {
    if (!d) return null;
    var pick = function (a, b) { return d[a] !== undefined ? d[a] : d[b]; };
    return {
      status: pick("Status", "status"),
      timerType: pick("TimerType", "timer_type"),
      timerLabel: pick("TimerLabel", "timer_label"),
      timerOn: pick("TimerOn", "timer_on"),
      endsAt: pick("EndsUnix", "ends_at") || 0,
      serverNow: pick("ServerNow", "server_now") || 0,
      cards: pick("Cards", "participants") || [],
      rankingLive: pick("RankingLive", "ranking_live"),
      ranking: pick("Ranking", "ranking") || []
    };
  }
  var state = null;
  try {
    state = normalize(JSON.parse(document.getElementById("monitor-data").textContent));
  } catch (e) { state = null; }
  if (!state) return;

  var skew = state.serverNow - Math.floor(Date.now() / 1000); // server − client
  function serverNow() { return Math.floor(Date.now() / 1000) + skew; }

  // latest violation TOTAL per participant, pushed by the `cheat` frames
  // (spec §6.9): a floor over the snapshot's own count — counts only ever
  // grow, so it survives snapshot resets and can be reconciled with max()
  var extraViolations = {};

  // answer pulse: participant id → client-side deadline (ms). renderCards
  // consults this on every rebuild, so the animation class survives the
  // full-DOM repaint each render() does and restarts on a repeat answer.
  var pulseUntil = {};
  var PULSE_MS = 700;

  // --- formatting ---------------------------------------------------------
  function mmss(sec) {
    if (!(sec > 0)) sec = 0;
    var m = Math.floor(sec / 60), s = Math.floor(sec % 60);
    return m + ":" + (s < 10 ? "0" : "") + s;
  }
  function fmtAnswer(raw) {
    if (!raw) return "";
    if (raw.charAt(0) === "[") {
      try {
        var idx = JSON.parse(raw);
        if (Array.isArray(idx)) {
          return idx.map(function (i) { return String.fromCharCode(65 + (+i || 0)); }).join(", ");
        }
      } catch (e) { return raw; }
    }
    try {
      var text = JSON.parse(raw);
      if (typeof text === "string") return text.length > 60 ? text.slice(0, 57) + "…" : text;
    } catch (e) { /* not JSON — show raw */ }
    return raw;
  }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }
  // Icon-only action button: Bootstrap Icon + tooltip hooks. data-quiz-tooltip
  // (not data-bs-toggle="tooltip") so a later data-bs-toggle="modal" on the
  // same node never collides — see web/js/ui.js.
  function iconBtn(cls, icon, label) {
    var b = el("button", cls);
    b.type = "button";
    var i = el("i", "bi bi-" + icon);
    i.setAttribute("aria-hidden", "true");
    b.appendChild(i);
    b.title = label;
    b.setAttribute("aria-label", label);
    b.setAttribute("data-quiz-tooltip", "");
    return b;
  }
  function violationsOf(card) {
    // max(snapshot, pushed total): a `cheat` frame carries the participant's
    // violation count AFTER its insert, so a snapshot generated around the
    // same time can neither double-count a row it already includes nor mask
    // one that arrived as an event first.
    var snap = card.violations || 0;
    var live = extraViolations[card.participant_id] || 0;
    return live > snap ? live : snap;
  }
  function cardOf(pid) {
    for (var i = 0; i < state.cards.length; i++) {
      if (state.cards[i].participant_id === pid) return state.cards[i];
    }
    return null;
  }

  // --- rendering ----------------------------------------------------------
  function renderWaiting() {
    var list = document.getElementById("waiting-list");
    var badge = document.getElementById("pending-count");
    if (!list) return;
    var pending = state.cards.filter(function (c) { return c.status === "pending"; });
    if (badge) badge.textContent = String(pending.length);
    list.textContent = "";
    if (!pending.length) {
      list.appendChild(el("li", "list-group-item text-body-secondary", "Tidak ada yang menunggu."));
      return;
    }
    pending.forEach(function (c) {
      var row = el("li",
        "list-group-item d-flex justify-content-between align-items-center flex-wrap gap-2");
      row.setAttribute("data-pid", c.participant_id);
      row.appendChild(el("span", "", c.name));
      var actions = el("div", "d-flex gap-1");
      [["approve", "btn btn-sm btn-outline-success", "check-lg", "Setujui"],
       ["reject", "btn btn-sm btn-outline-danger", "x-lg", "Tolak"]]
        .forEach(function (spec) {
          var b = iconBtn(spec[1], spec[2], spec[3]);
          b.setAttribute("data-act", spec[0]);
          b.setAttribute("data-pid", c.participant_id);
          actions.appendChild(b);
        });
      row.appendChild(actions);
      list.appendChild(row);
    });
  }

  function renderCards() {
    var wrap = document.getElementById("card-list");
    if (!wrap) return;
    wrap.textContent = "";
    if (!state.cards.length) {
      wrap.appendChild(el("p", "text-body-secondary", "Belum ada yang bergabung."));
      return;
    }
    var now = serverNow();
    state.cards.forEach(function (c) {
      var v = violationsOf(c);
      var cheater = c.cheating || v > 0;
      var col = el("div", "col-md-6 col-xl-4");
      col.setAttribute("data-pid", c.participant_id);
      // this node is rebuilt on every render — re-apply the answer pulse
      // class while its deadline is live so repaints can't wipe it, and
      // drop expired entries so the map stays bounded
      var pulseEnd = pulseUntil[c.participant_id];
      if (pulseEnd) {
        if (pulseEnd > Date.now()) col.classList.add("card-pulse");
        else delete pulseUntil[c.participant_id];
      }
      // detected (violations) or flagged (cheating) → yellow card
      var card = el("div", "card p-2" +
        (cheater ? " bg-warning-subtle border-warning" : ""));

      var head = el("div", "d-flex justify-content-between align-items-center mb-1");
      head.appendChild(el("strong", "", c.name));
      var badges = el("span", "d-flex gap-1 align-items-center");
      if (c.cheating) badges.appendChild(el("span", "badge text-bg-danger", "Ditandai"));
      else if (v > 0) badges.appendChild(el("span", "badge text-bg-danger", v + " pelanggaran"));
      badges.appendChild(el("span", "badge text-bg-secondary", c.status_label || c.status));
      head.appendChild(badges);
      card.appendChild(head);

      var meta = el("div", "small text-body-secondary");
      var bits = [];
      // the live page: the pre-submit review or the current question
      if (c.page === "preview") bits.push("Pratinjau");
      else if (c.current_q > 0) bits.push("Pertanyaan " + c.current_q);
      // dwell ticks live on the question page (the preview has no own
      // since-clock — current_q_since still belongs to the question)
      if (c.status === "started" && c.current_q_since && c.page !== "preview") {
        var dwell = el("span", "", "di sini " + mmss(now - c.current_q_since) + "s");
        dwell.setAttribute("data-live-since", c.current_q_since);
        dwell.setAttribute("data-live-prefix", "di sini ");
        dwell.setAttribute("data-live-suffix", "s");
        meta.appendChild(document.createTextNode(bits.join(" · ") + (bits.length ? " · " : "")));
        meta.appendChild(dwell);
        bits = []; // already flushed
      } else if (bits.length) {
        meta.appendChild(document.createTextNode(bits.join(" · ")));
        bits = [];
      }
      if (c.status === "started" && c.ends_at) {
        var left = el("span", "", "sisa " + mmss(c.ends_at - now));
        left.setAttribute("data-live-ends", c.ends_at);
        if (meta.textContent) meta.appendChild(document.createTextNode(" · "));
        meta.appendChild(left);
      }
      if (meta.textContent) meta.appendChild(document.createTextNode(" · "));
      meta.appendChild(document.createTextNode(c.connected
        ? "tersambung"
        : (c.status === "started" ? "koneksi terputus" : "tidak tersambung")));
      if (c.cheating && v > 0) {
        meta.appendChild(document.createTextNode(" · "));
        meta.appendChild(el("span", "text-danger", v + " pelanggaran"));
      }
      card.appendChild(meta);

      // per-question timer mode (spec §7): time already banked on each left
      // question + the still-running clock on the current one
      if (state.timerType === "per_soal") {
        var spentLine = el("div", "small");
        var parts = [];
        (c.spent || []).forEach(function (s) {
          parts.push("Pertanyaan " + s.q + " " + mmss(s.sec));
        });
        var running = c.status === "started" && c.current_q_since && c.current_q > 0;
        if (parts.length || running) {
          if (parts.length) spentLine.textContent = "terpakai " + parts.join(" · ");
          if (running) {
            if (parts.length) spentLine.appendChild(document.createTextNode(" · "));
            var run = el("span", "", "Pertanyaan " + c.current_q + " " + mmss(now - c.current_q_since));
            run.setAttribute("data-live-since", c.current_q_since);
            run.setAttribute("data-live-prefix", "Pertanyaan " + c.current_q + " ");
            run.setAttribute("data-live-suffix", "");
            spentLine.appendChild(run);
          }
          card.appendChild(spentLine);
        }
      }

      var answer = el("div", "small");
      answer.setAttribute("data-answer-line", "");
      // server-rendered display (letters via THIS murid's qorder) wins;
      // fmtAnswer is only the legacy fallback when it is absent
      var atext = c.answer_display || fmtAnswer(c.answer);
      // the answer carries ITS question (answer_question_pos from qorder) —
      // once the student moves on, the old answer stays attributed to its
      // own question and never reads as the answer to the current one
      answer.textContent = atext
        ? (c.answer_question_pos > 0
            ? "jawaban pertanyaan " + c.answer_question_pos +
              (c.answer_q_teks ? " · " + c.answer_q_teks : "") + ": " + atext
            : "jawaban: " + atext)
        : "";
      answer.hidden = !atext;
      card.appendChild(answer);

      if (c.score_auto !== null && c.score_auto !== undefined) {
        var score = (c.final_score !== null && c.final_score !== undefined)
          ? c.final_score : c.score_auto;
        card.appendChild(el("div", "fw-semibold", (+score).toFixed(2)));
      }

      // spec §6.9 (extended): BOTH action buttons are in the DOM only for a
      // detected (violation) or flagged (cheating) student — never otherwise
      if (cheater) {
        var actions = el("div", "mt-2 d-flex gap-1");
        var toggle = iconBtn("btn btn-sm btn-outline-danger",
          "exclamation-triangle", "Tandai melakukan kecurangan");
        toggle.setAttribute("data-act", "cheat_toggle");
        toggle.setAttribute("data-pid", c.participant_id);
        var remove = iconBtn("btn btn-sm btn-danger", "person-dash", "Keluarkan murid");
        remove.setAttribute("data-act", "remove");
        remove.setAttribute("data-pid", c.participant_id);
        remove.setAttribute("data-name", c.name);
        remove.setAttribute("data-bs-toggle", "modal");
        remove.setAttribute("data-bs-target", "#confirmRemove");
        actions.appendChild(toggle);
        actions.appendChild(remove);
        card.appendChild(actions);
      }
      col.appendChild(card);
      wrap.appendChild(col);
    });
  }

  function renderRank() {
    var section = document.getElementById("monitor-rank-section");
    if (!state.rankingLive) {
      if (section) section.hidden = true;
      return;
    }
    if (section) section.hidden = false;
    var list = document.getElementById("monitor-rank");
    if (!list) return;
    list.textContent = "";
    if (!state.ranking.length) {
      list.appendChild(el("li", "list-group-item text-body-secondary",
        "Peringkat muncul setelah jawaban masuk (jika ranking_live aktif)."));
      return;
    }
    state.ranking.forEach(function (e) {
      var li = el("li",
        "list-group-item d-flex justify-content-between align-items-center" +
        (e.cheating ? " active" : ""));
      var label = e.name;
      if (e.removed) label += " · dikeluarkan";
      if (e.cheating) label += " · ditandai";
      li.appendChild(el("span", "", label));
      li.appendChild(el("span", "fw-semibold", (+e.score).toFixed(2)));
      list.appendChild(li);
    });
  }

  function renderChrome() {
    var stop = document.getElementById("btn-stop");
    var close = document.getElementById("btn-close");
    if (stop) stop.hidden = state.status !== "berjalan";
    if (close) close.hidden = state.status !== "aktif";
    var clock = document.getElementById("monitor-clock");
    if (clock) clock.hidden = !(state.timerOn && state.endsAt);
    tick();
  }

  function render() {
    renderWaiting();
    renderCards();
    renderRank();
    renderChrome();
    // freshly painted icon-only buttons need their Bootstrap tooltips
    if (window.quizTooltips) quizTooltips();
  }

  function tick() {
    var now = serverNow();
    var timer = document.getElementById("monitor-timer");
    if (timer && state.endsAt) timer.textContent = mmss(state.endsAt - now);
    var nodes = document.querySelectorAll("[data-live-ends],[data-live-since]");
    for (var i = 0; i < nodes.length; i++) {
      var n = nodes[i];
      if (n.hasAttribute("data-live-ends")) {
        n.textContent = "sisa " + mmss((+n.getAttribute("data-live-ends")) - now);
      } else {
        // dwell ("here 0:12s") or a labelled clock ("Q2 0:12")
        var prefix = n.getAttribute("data-live-prefix");
        var suffix = n.getAttribute("data-live-suffix");
        if (prefix === null) { prefix = "di sini "; suffix = "s"; }
        n.textContent = prefix +
          mmss(now - (+n.getAttribute("data-live-since"))) + suffix;
      }
    }
  }
  setInterval(tick, 1000);

  // --- actions ------------------------------------------------------------
  function post(url, payload) {
    return fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(payload || {})
    }).then(function (r) {
      return r.json().then(function (b) { return { status: r.status, body: b }; });
    });
  }
  function actionURL(pid) {
    return "/teacher/quiz/" + quizID + "/participants/" + pid + "/action";
  }

  // Messages ride through sessionStorage when the action reloads the page
  // (toast + reload would otherwise wipe the toast before it renders).
  function failThenReload(r) {
    var msg = r && r.body && r.body.message ? r.body.message : "Permintaan gagal. Silakan coba lagi.";
    if (window.quizStoreFlash) quizStoreFlash("danger", msg);
    location.reload();
  }
  function doneThenReload(msg) {
    if (window.quizStoreFlash) quizStoreFlash("success", msg);
    location.reload();
  }
  function confirmThen(message, onYes, tone) {
    if (!window.quizConfirm) {
      console.error("confirmation unavailable — action skipped");
      return;
    }
    quizConfirm(message, onYes, tone);
  }

  var removePid = 0;
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest ? ev.target.closest("[data-act]") : null;
    if (!btn) return;
    var act = btn.getAttribute("data-act");
    var pid = +btn.getAttribute("data-pid");
    if (act === "approve" || act === "reject" || act === "cheat_toggle") {
      post(actionURL(pid), { action: act }).then(function (r) {
        if (r.status >= 400) failThenReload(r); // state moved under us
      }).catch(function () { failThenReload(null); });
    } else if (act === "remove") {
      removePid = pid;
      var name = document.getElementById("remove-student-name");
      if (name) name.textContent = btn.getAttribute("data-name") || "";
    } else if (act === "stop") {
      // stop ends the quiz for everyone — always a user decision (the
      // {confirm:true} body flag is for the server, not for the user)
      confirmThen(
        "Hentikan kuis sekarang? Murid berhenti menjawab seketika dan kuis berakhir.",
        function () {
          post("/teacher/quiz/" + quizID + "/stop", { confirm: true }).then(function (r) {
            if (r.status === 200) doneThenReload("Kuis dihentikan — murid tidak dapat menjawab lagi.");
            else failThenReload(r);
          }).catch(function () { failThenReload(null); });
        }, "danger");
    } else if (act === "close") {
      confirmThen("Tutup kuis sekarang? Murid tidak dapat menjawab lagi.",
        function () {
          // per-question close: 409 {working:N} opens the count modal
          post("/teacher/quiz/" + quizID + "/status", { status: "selesai" }).then(function (r) {
            if (r.status === 200) { doneThenReload("Kuis berhasil ditutup."); return; }
            var working = r.body && r.body.data ? r.body.data.working : undefined;
            if (working === undefined) { failThenReload(r); return; }
            var span = document.getElementById("close-working-count");
            if (span) span.textContent = String(working);
            var modal = window.bootstrap && bootstrap.Modal.getOrCreateInstance(
              document.getElementById("confirmClose"));
            if (modal) modal.show();
          }).catch(function () { failThenReload(null); });
        }, "danger");
    }
  });

  var removeBtn = document.getElementById("remove-confirm-btn");
  if (removeBtn) {
    removeBtn.addEventListener("click", function () {
      post(actionURL(removePid), { action: "remove" }).then(function (r) {
        var m = window.bootstrap && bootstrap.Modal.getOrCreateInstance(
          document.getElementById("confirmRemove"));
        if (m) m.hide();
        if (r.status >= 400) failThenReload(r);
        else if (window.quizToast) {
          var nameEl = document.getElementById("remove-student-name");
          var who = nameEl ? nameEl.textContent : "";
          quizToast("success", who ? "Murid \"" + who + "\" dikeluarkan dari kuis."
            : "Murid dikeluarkan dari kuis.");
        }
      }).catch(function () { failThenReload(null); });
    });
  }

  var closeBtn = document.getElementById("close-confirm-btn");
  if (closeBtn) {
    closeBtn.addEventListener("click", function () {
      post("/teacher/quiz/" + quizID + "/status",
        { status: "selesai", confirm: true }).then(function (r) {
          if (r.status === 200) doneThenReload("Kuis berhasil ditutup.");
          else failThenReload(r);
        }).catch(function () { failThenReload(null); });
    });
  }

  // --- live stream --------------------------------------------------------
  // Every frame's data field carries the whole {"type":…,"data":…} envelope
  // the hub publishes (tests pin this wire shape) — unwrap it so handlers
  // see the payload itself. Without this the greeting snapshot normalized
  // to an empty card list and wiped the monitor right after each load.
  function frameData(ev) {
    var d = JSON.parse(ev.data);
    if (d && typeof d.type === "string" && d.data !== undefined) return d.data;
    return d;
  }
  var es = new EventSource(root.getAttribute("data-stream"));

  es.addEventListener("snapshot", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    var next = normalize(d);
    if (!next) return;
    // extraViolations is NOT reset here: it holds absolute totals (floors),
    // so an older snapshot delivered after a `cheat` push cannot undo it
    state = next;
    skew = state.serverNow - Math.floor(Date.now() / 1000);
    render();
  });

  es.addEventListener("answer", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    var card = cardOf(d.participant_id);
    if (card) {
      card.answer_question_id = d.question_id;
      card.answer_question_pos = +d.question_pos || 0;
      card.answer = typeof d.answer === "string" ? d.answer : JSON.stringify(d.answer);
      // server-rendered letter display + question text for THIS answer —
      // absent/undefined falls back to fmtAnswer in renderCards
      card.answer_display = d.answer_display;
      card.answer_q_teks = d.q_teks;
      // linear mode advances the question on answer — follow it live
      if (d.current_q !== undefined) card.current_q = d.current_q;
      if (d.current_q_since !== undefined) card.current_q_since = d.current_q_since;
      if (d.status !== undefined) card.status = d.status;
    }
    // mark X's card as pulsing (deadline consulted by renderCards, so a
    // repeat answer simply re-arms it and the next paint restarts the run)
    if (d.participant_id) pulseUntil[d.participant_id] = Date.now() + PULSE_MS;
    render(); // meta/dwell/answer line all moved
  });

  // page moves (spec §6.3/§7): start → question changes → preview. Works
  // for every timer mode; the entry disappears again on submit.
  es.addEventListener("page", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    if (!d || !d.participant_id) return;
    var card = cardOf(d.participant_id);
    if (!card) {
      if (!d.name) return; // no name → nothing to build a card from
      card = { participant_id: d.participant_id, violations: 0, answer: "" };
      state.cards.push(card);
    }
    if (d.name !== undefined) card.name = d.name;
    if (d.status !== undefined) card.status = d.status;
    if (d.current_q !== undefined) card.current_q = d.current_q;
    if (d.current_q_since !== undefined) card.current_q_since = d.current_q_since;
    if (d.ends_at !== undefined) card.ends_at = d.ends_at;
    if (d.page !== undefined) card.page = d.page;
    if (d.spent !== undefined) card.spent = d.spent;
    if (d.connected !== undefined) card.connected = d.connected;
    render();
  });

  es.addEventListener("cheat", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    if (!d || !d.participant_id) return;
    // `count` is the participant's ABSOLUTE post-insert row total — the
    // server publishes COUNT(*) after commit, so only a valid positive
    // total may raise the floor. An absent/invalid count is replaced by
    // NEITHER a client-side delta nor a guess (that would fabricate a
    // number the server never sent): the floor then stays as it is and
    // the badge keeps showing max(snapshot, last pushed total) — both
    // server-sourced. NaN/non-positive totals fail the > comparison.
    var seen = extraViolations[d.participant_id] || 0;
    var total = Math.floor(+d.count);
    if (total > seen) extraViolations[d.participant_id] = total;
    render(); // violation > 0 → yellow card + the action buttons enter the DOM
  });

  es.addEventListener("finished", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    // a submitted attempt leaves the cards — tracking runs start → submit
    state.cards = state.cards.filter(function (c) {
      return c.participant_id !== d.participant_id;
    });
    render();
  });

  es.addEventListener("rank", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    if (!state.rankingLive) return; // ranking off — frames must not render
    if (d && Array.isArray(d.ranking)) {
      state.ranking = d.ranking;
      renderRank();
    }
  });

  // heartbeat re-syncs the countdown (spec §6.6)
  es.addEventListener("ping", function (ev) {
    var d;
    try { d = frameData(ev); } catch (e) { return; }
    if (d && d.server_now) skew = d.server_now - Math.floor(Date.now() / 1000);
    tick();
  });

  es.addEventListener("start", function () { location.reload(); });
  es.addEventListener("force_stop", function () { location.reload(); });

  // --- boot ---------------------------------------------------------------
  render();
})();
