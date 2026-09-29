# Client-side data logic audit — non-search-bar findings (report-only)

This audit covers client-side logic in the teacher/student web app that touches **business data or
state transitions outside the search bars** — timers, answer payloads, navigation state, anti-cheat
event classification, violation counts, count/sequence bookkeeping, validation duplicates, and the
generic fetch/post transport hooks. Search bars are explicitly out of scope here: they were
converted to unified server-side search by the same mandate (see
[Search bars converted by this task](#search-bars-converted-by-this-task) below). Every finding is
**report-only** — no code changes are proposed or made by this document; recommendations state
whether the current behavior should be kept as UI and what the server already (or should) enforce.
The appendix [Methodology](#methodology) lists the files scanned and the server-side corroboration
reads backing each claim.

## Table of contents

- [F1 — Workspace timer: client deadline drives attempt closure](#f1--workspace-timer-client-deadline-drives-attempt-closure)
- [F2 — Workspace answer payload assembly ("empty = no POST")](#f2--workspace-answer-payload-assembly-empty--no-post)
- [F3 — Workspace navigation position state (`advance()` / local `current`)](#f3--workspace-navigation-position-state-advance--local-current)
- [F4 — Anti-cheat visibility event classification (`report()` + sleep detector)](#f4--anti-cheat-visibility-event-classification-report--sleep-detector)
- [F5 — Monitor violation count aggregation (`extraViolations` map)](#f5--monitor-violation-count-aggregation-extraviolations-map)
- [F6 — Teacher question count badge bump (`bumpQuestionCount()`)](#f6--teacher-question-count-badge-bump-bumpquestioncount)
- [F7 — Composed-row sequence guessed in the browser (`appendQuestionRow()`)](#f7--composed-row-sequence-guessed-in-the-browser-appendquestionrow)
- [F8 — Bank length bucket rule duplicated in the client (`bucketOf()`)](#f8--bank-length-bucket-rule-duplicated-in-the-client-bucketof)
- [F9 — Question-form validation + answer-key index assembly (inline IIFE)](#f9--question-form-validation--answer-key-index-assembly-inline-iife)
- [F10 — Settings timer-seconds pre-submit guard (inline IIFE)](#f10--settings-timer-seconds-pre-submit-guard-inline-iife)
- [F11 — Join-panel code validation + `pending_join` cookie (inline IIFE)](#f11--join-panel-code-validation--pending_join-cookie-inline-iife)
- [F12 — `runFetch()`/`runPost()` generic data hooks (transport pass-through)](#f12--runfetchrunpost-generic-data-hooks-transport-pass-through)
- [Audited and cleared](#audited-and-cleared)
- [Search bars converted by this task](#search-bars-converted-by-this-task)
- [Methodology](#methodology)

---

## F1 — Workspace timer: client deadline drives attempt closure

- **File / function / lines:** `web/js/workspace.js` — `startTimer()` / inner tick (IIFE: workspace client), lines 88–99 (deadline calc :92; finish trigger :98).
- **Category:** client-side timer computation + client-initiated attempt closure (business state transition).
- **Data:** attempt deadline (`blob.ends_at`), server clock (`blob.server_now`, re-synced per SSE ping at :55–64), participant attempt status.

```js
function startTimer() {
  if (!blob || !blob.timer_on || !(blob.ends_at > 0)) return;
  timer = document.getElementById("timer");
  if (timer) timer.classList.remove("d-none");
  deadline = Date.now() + (blob.ends_at - blob.server_now) * 1000;
  var value = document.getElementById("timer-value");
  var tick = function () {
    var rem = deadline - Date.now();
    if (rem <= 0) {
      if (value) value.textContent = "00:00";
      finishAttempt(); // wall clock reached — close the attempt
```

- **Risk:** MEDIUM (design-sensitive): the browser clock decides WHEN the finish POST fires, and a frozen/patched clock delays it. Not exploitable for extra answers: (a) deadline is re-derived from `server_now` on every heartbeat (:55-64), (b) the answer handler enforces the deadline inside its transaction and force-scores the attempt (`internal/handlers/student.go:927-941` "wall clock passed inside the transaction (§8): close the attempt"), (c) a server-side 1 s watchdog closes timed-out quizzes (`internal/handlers/global.go:822-847` `WatchOnce`, started in `cmd/server/main.go:206`). Residual risk: if the client never fires finish, a `per_soal` attempt can linger in `started` until its next server contact (`FinishAttempt` itself does not check `EndsAt` — `internal/handlers/student.go:1289-1330`).
- **Recommendation:** KEEP as UI — the countdown must live in the browser. Server-verify already present for answers/quizzes; RECOMMEND adding an EndsAt check (or per-participant sweep) to `FinishAttempt` and/or the watchdog so attempt closure never depends on the client calling finish.

## F2 — Workspace answer payload assembly ("empty = no POST")

- **File / function / lines:** `web/js/workspace.js` — `readPayload()` (answer payload assembly), used by `submitCurrent`/`saveCard`/`nextBtn`; lines 188–203 (callers :231-232, :261, :318).
- **Category:** client answer payload assembly + client rule ("empty" answer means no POST at all).
- **Data:** `question_id`, selected option indices / essay text.

```js
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
```

- **Risk:** LOW-MEDIUM (spoofable payload shape, but server is authoritative): the browser decides which `qid` to send and that an empty selection is "no request". Server validates: question must belong to the quiz (`internal/handlers/student.go:942-949` joins `quiz_questions`), body is shape/index-checked by `parseAnswer` (student.go:955-959), correctness computed server-side via `quizengine.IsCorrect` (student.go:961-964). "Clear my answers" can never be transmitted (no POST = unanswered = wrong at finish) — consistent with server scoring.
- **Recommendation:** KEEP as UI (payload assembly must happen in the browser); server already validates qid scope + answer shape — retain `parseAnswer`/`IsCorrect` as the single scoring authority (currently correct).

## F3 — Workspace navigation position state (`advance()` / local `current`)

- **File / function / lines:** `web/js/workspace.js` — `advance()` + local `current` state in `submitCurrent()`; lines 128–130 (state init), 220–222, 237–248.
- **Category:** client navigation/position state (duplicates server `current_q` bookkeeping).
- **Data:** attempt position (current question index), total question count.

```js
function advance() {
  if (current < total) { current++; render();
}
// ...and in submitCurrent():
    if (!linear) return;
    var preview = body.data ? body.data.preview : null;
    if (preview) {
      showPreview(card, preview).then(function () {
        if (current === total) finishAttempt();
        else advance();
```

- **Risk:** LOW (display drift only): the client increments its position without asking the server, while the server independently advances `current_q` inside the answer transaction (`internal/handlers/student.go:973-991` "linear mode: the answer itself advances the monitor's question clock"). A mismatch self-heals because the SSE snapshot handler reloads the page when `d.current_q`/blob disagree (workspace.js:38-52).
- **Recommendation:** KEEP as UI because the server `current_q` is authoritative and the snapshot re-sync already reconciles; `jumpTo()` (:290-308) already takes the server's `resp.data.current_q` — no change needed.

## F4 — Anti-cheat visibility event classification (`report()` + sleep detector)

- **File / function / lines:** `web/js/anti-cheat.js` — IIFE `report()` + sleep detector; lines 11–22 (10 s debounce), 31–37 (30 s sleep rule), listeners :24-29.
- **Category:** client business rule (event classification: what counts as minimize/blur/sleep) + duplicated validation (10 s same-kind collapse mirror).
- **Data:** anti-cheat event kinds posted to `/quiz/:code/visibility`.

```js
var lastReport = {}; // kind → Date.now() of the last accepted report
function report(kind) {
  var now = Date.now();
  if (lastReport[kind] && now - lastReport[kind] < 10000) return;
  lastReport[kind] = now;
  fetch("/quiz/" + code + "/visibility", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "same-origin",
    body: JSON.stringify({ kind: kind })
  }).catch(function () {});
}
// sleep: a 30 s+ gap between 5 s ticks means the tab or machine slept
var lastTick = Date.now();
setInterval(function () {
  var now = Date.now();
  if (now - lastTick > 30000) report("sleep");
```

- **Risk:** MEDIUM (inherent to the domain): classification thresholds (10 s collapse, 30 s sleep) exist only in the browser, and a determined student can suppress or spoof `kind` via devtools — client enforcement is bypassable by construction. Mitigations verified server-side: kind is ENUM-validated and the server independently applies a 10 s same-kind collapse (`internal/handlers/student.go:1375-1378`; `internal/quizengine/anticheat.go:14-22` `ShouldRecord`), and the server keeps its own connection-truth heartbeat registry (`internal/handlers/live.go:26-32`, `Connected`/`SweepDisconnects`) it could cross-check against reported events.
- **Recommendation:** KEEP — must run in the browser (only the browser observes visibility/blur/sleep). Server-verify: treat posted kinds as untrusted hints and corroborate flags server-side with heartbeat/SSE gaps (e.g., auto-flag when disconnect silence exceeds a threshold without matching client reports); never let the client count be the sole basis for a penalty.

## F5 — Monitor violation count aggregation (`extraViolations` map)

- **File / function / lines:** `web/js/monitor.js` — `extraViolations` map + `violationsOf()` gating `renderCards()` enforcement UI; lines 33–35 (map), 79–80 (sum), 176–197 (count display + button gating), 400–406 (increment on SSE).
- **Category:** client aggregation (violation count) + client business rule (client decides when enforcement controls render).
- **Data:** per-participant violation count (server snapshot value + client-side increments), participant cheating flags.

```js
// cheat events bump counts client-side until the next snapshot repaints
// the server truth
var extraViolations = {};
...
function violationsOf(card) {
  return (card.violations || 0) + (extraViolations[card.participant_id] || 0);
}
...
  var v = violationsOf(c);
  if (v > 0) {
    card.appendChild(el("div", "small text-danger", v + " violation(s)"));
  }
...
  // spec §6.9: BOTH action buttons are in the DOM only while a violation
  // exists — when v === 0 they are never created, not merely invisible
  if (v > 0) {
```

- **Risk:** MEDIUM (data inconsistency, no privilege hole): the displayed count is `server_count + client_increments`, while the server collapses same-kind events within 10 s (`quizengine ShouldRecord`) — so between snapshots the client count can exceed the server's truth (every SSE `cheat` event bumps it at :404 regardless of collapse), and enforcement buttons appear/disappear based on a client-computed number. Actual actions (`cheat_toggle`/`remove`) POST to the server which authorizes them independently (`actionURL` → `/participants/:pid/action`, :314-316), so no security bypass; snapshot resets the map (:377).
- **Recommendation:** KEEP the live bump as UI (instant feedback is the point), but have the server publish the authoritative violation total in the `cheat` SSE event (or re-publish the card's `violations` field) and render buttons from that field instead of a client-side sum; server-verify: enforcement endpoints must keep ignoring the client count (they do today).

## F6 — Teacher question count badge bump (`bumpQuestionCount()`)

- **File / function / lines:** `web/js/teacher.js` — `bumpQuestionCount()`; lines 121–136.
- **Category:** client aggregation / count maintenance duplicating a server-derived business metric.
- **Data:** quiz question count (toolbar `#q-count` span, tab badge `(N)`).

```js
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
```

- **Risk:** LOW-MEDIUM (staleness): the client blindly does +1 because the ComposeQuestions success response carries NO data (`internal/handlers/teacher_quiz.go:746` `return ok(c, nil)`). Any concurrent add (second teacher/session), partial state, or a count that was already stale drifts the badge until a reload. Presentation-only — no server decision reads the DOM.
- **Recommendation:** Server should return `{seq, total}` (or the recomputed count) from `POST /teacher/quiz/:id/questions` and `teacher.js` should set the badges from that response; keep the DOM-walking regex as fallback only. Server X = `handlers/teacher_quiz.go` `ComposeQuestions`.

## F7 — Composed-row sequence guessed in the browser (`appendQuestionRow()`)

- **File / function / lines:** `web/js/teacher.js` — `appendQuestionRow()` (seq assignment); lines 154–162 (seq math :159-160), tail :193 called `filterQuestionTable()` (that call was removed by the search-bar conversion; the row-insert path itself is unchanged).
- **Category:** client transform / client business rule (row ordering assigned in the browser).
- **Data:** `quiz_questions.seq` (question order within the quiz).

```js
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
```

- **Risk:** LOW-MEDIUM (inconsistency with server): the server assigns its own sequence — questions without `seq_<id>` "append after MAX(seq) of the quiz" in arrival order (`internal/handlers/teacher_quiz.go:697-723`). The client guesses last-DOM-row+1; if the DB's MAX(seq) differs (concurrent compose, prior out-of-order rows), the freshly appended row displays a sequence number that is not what the server stored, until reload.
- **Recommendation:** `ComposeQuestions` should return the assigned seq (and it already plans to return data — see F6) and `appendQuestionRow` should render `server_seq`; server X = `handlers/teacher_quiz.go`.

## F8 — Bank length bucket rule duplicated in the client (`bucketOf()`)

- **File / function / lines:** `web/js/teacher.js` — `bucketOf()` (shared rule behind `filterBankList`); lines 236–245 (rule :244); consumption in `filterBankList` :258-259.
- **Category:** duplicated validation / client business rule (data classification: text-length → bucket).
- **Data:** question text length (`CHAR_LENGTH`) → short/medium/long bucket.

```js
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
```

- **Risk:** LOW: thresholds are a second copy of the server rule (`internal/handlers/teacher_questions.go:29-42` `bucketFor`: max 79 / 80-200 / min 201 — currently identical). Drift risk only if server buckets change. Server re-validates on compose (`teacher_quiz.go:624-657` rejects ids outside the declared bucket) — but NOTE: `addQuestion()` posts `length:""` (:214) instead of the active `bankActiveLen`, so that server-side re-check is inert for browser adds (harmless: buckets are a UX filter, not a security boundary).
- **Recommendation:** Keep as UI for instant filtering (the bank length buttons remain client-side after the search-bar conversion — see cross-reference below); make the server the single source of truth (it already is for queries via `bucketFor.clause`) and, if buckets ever change, ship the thresholds in the payload instead of hard-coding 80/200 twice.

## F9 — Question-form validation + answer-key index assembly (inline IIFE)

- **File / function / lines:** `views/teacher/question_form.html` — inline IIFE; validation :126-155; `renumber()` :78-82; hydrate :157-171.
- **Category:** duplicated validation (question business rules) + client answer-key index computation (`renumber()`: `correct[]` values are DOM positions).
- **Data:** question type, `options[]`, `correct[]` indices — i.e. the answer key itself.

```js
var checked = 0;
optList.querySelectorAll('input[name="correct[]"]').forEach(function (p) {
  if (p.checked) checked++;
});
if (type === "pg" && checked !== 1) {
  reject("Pilih satu jawaban benar.");
  return;
}
if (type === "multi" && checked < 1) {
  reject("Pilih minimal satu jawaban benar.");
  return;
}
var opts = optList.querySelectorAll('input[name="options[]"]');
var optionsValid = opts.length >= 2;
opts.forEach(function (o) {
  if (!o.value.trim()) optionsValid = false;
});
if (!optionsValid) reject("Isi minimal dua opsi jawaban tanpa kosong.");
```

- **Risk:** LOW-MEDIUM (bypassable by design, server authoritative): the guard mirrors the server's rules exactly and the server re-validates everything — `internal/handlers/teacher_questions.go` `questionInput` (:205-251: ≥2 options, non-empty, pg exactly 1 correct, multi ≥1, every correct index in range, `teks` required → 400). The client-computed `correct[]` indices are therefore untrusted input the server bounds-checks.
- **Recommendation:** KEEP as UX pre-check (it stops bad submits off the network — that is its stated purpose); server-verify already in place via `questionInput` — no server change needed. If the client rules ever diverge from server rules, fix the client, never relax the server.

## F10 — Settings timer-seconds pre-submit guard (inline IIFE)

- **File / function / lines:** `views/teacher/settings_fields.html` — inline IIFE, pre-submit timer-seconds guard; lines 90–108.
- **Category:** duplicated validation (timer configuration business rule).
- **Data:** `total_seconds`, `per_question_seconds` (quiz timer config).

```js
form.addEventListener("submit", function (ev) {
  var msg = "";
  if (sel.value === "global") {
    if (!(Number(document.getElementById("total_seconds").value) >= 1)) {
      msg = "Enter the total time in seconds.";
    }
  } else if (sel.value === "per_soal") {
    if (!(Number(document.getElementById("per_question_seconds").value) >= 1)) {
      msg = "Enter the seconds per question.";
    }
  }
  if (!msg) return;
  ev.preventDefault();
  ev.stopPropagation();
  if (window.quizToast) window.quizToast("danger", msg);
});
```

- **Risk:** LOW: bypassable (devtools), but the server enforces the identical rules — `internal/handlers/teacher_quiz.go:197-202` (`TimerType=="global" && TotalSeconds < 1` → 400; `TimerType=="per_soal" && PerQuestionSeconds < 1` → 400). Note the adjacent `syncTimerFields()` (:80-88) is pure UI (field visibility, deliberately not disabling inputs).
- **Recommendation:** KEEP as UX guard; server already validates both branches — no change.

## F11 — Join-panel code validation + `pending_join` cookie (inline IIFE)

- **File / function / lines:** `views/layout/join_panel.html` — inline IIFE; validation :80-90; cookie write :75-78; auto-join :94-103.
- **Category:** duplicated validation (code format) + client-written state the server consumes (`pending_join` cookie).
- **Data:** 6-char quiz join code; `pending_join` cookie (client-set, server-read).

```js
function saveAndSignIn(code) {
  document.cookie = "pending_join=" + encodeURIComponent(code) + "; path=/; max-age=1800";
  location.href = "/login";
}

form.addEventListener("submit", function (e) {
  e.preventDefault();
  var code = (input.value || "").trim().toUpperCase();
  if (code.length !== 6) {
    if (window.quizToast) quizToast("danger", "Enter the 6-character join code.");
    input.focus();
    return;
  }
  if (authed) submitCode(code);
  else saveAndSignIn(code);
});
```

- **Risk:** LOW (server treats cookie/code as untrusted): the code is only a lookup key — the server resolves it against the DB (`internal/handlers/student.go:316-343` `Join` → `quizByCode` → `joinQuiz`) and the cookie is explicitly documented as untrusted input that the server bounds (`internal/quizengine/joincode.go:33` "it only bounds untrusted input such as the pending_join cookie"). A forged/odd-length cookie can only produce a failed join. Client uppercase-normalizes before posting/setting.
- **Recommendation:** KEEP as UX (6-char check + normalization save round trips); server already verifies the code exists via DB lookup — ensure server-side normalization (trim/upper) matches the client's, which `quizByCode` should keep doing (verified untrusted-input handling exists).

## F12 — `runFetch()`/`runPost()` generic data hooks (transport pass-through)

- **File / function / lines:** `web/js/teacher.js` — `runFetch()`/`runPost()` generic data hooks (data flowing through); lines 21–54 (`runFetch`), 87–114 (`runPost`), `successMessage` :60-72.
- **Category:** transport (no data logic) — reported because the assignment asked what data flows through these hooks.
- **Data:** verbatim pass-through of: quiz settings incl. timer/join-mode/shuffle/score-visibility flags (`settings_fields`), question forms incl. `options[]` + `correct[]` answer key (`question_form`), grading scores (`grading.html:15` `data-json`), student add form (`quiz_detail.html:99` `data-json`), students/refs CRUD; `runPost` sends empty-body action POSTs (delete/approve etc.).

```js
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
```

- **Risk:** LOW / cleared: no client-side value transformation or rule — `FormData` is serialized as-is (the `data-json` branch) and the server 400s invalid payloads (verified for questions `teacher_questions.go:205-251`, timer `teacher_quiz.go:197-202`, grade score `results.go:352-397` "Score must be between 0 and 100"). `successMessage()` (:60-72) only fills toast text tokens from submitted fields — presentation. Redirect logic (:43-49) consumes `body.data[nextId]` — UI navigation.
- **Recommendation:** KEEP as UI/transport (X = server validate-then-store, already the case for every payload observed). Note `grading.html` relies on HTML `min`/`max=0..100` attributes only for input constraints — server clamp at `results.go:397` is the real guard (fine).

---

## Audited and cleared

Display-only UI/SSE code reviewed and found to hold **no client-side data rules** (no sums, sorts,
classifications, or state transitions beyond what the server authorizes):

| Area | Why it is clear |
|---|---|
| `web/js/ui.js` (entire file, 142 lines) | Toasts, confirm modal, tooltips, sessionStorage flash — message formatting only; no data reads/computation. Server copy rendered via textContent only. |
| `web/js/theme.js` (entire file, 35 lines) + inline one-liners `views/layout/app_head.html:14`, `head.html:14`, `landing_head.html:14` | localStorage theme toggle / FOUC prevention — pure UI. |
| `views/teacher/password_resets.html` inline script (POST approve/reject + reload) | Outcome decided server-side (`password_resets.go`); UI only toggles button disabled state. |
| `web/js/teacher_dashboard.js` pure helpers: `buildChartData` :38-53, `summaryCells` :58-68, `quizRows` :70-84, `scoreStats` :87-95, `filterOptions` :99-115, `appliedFilters` :120-127, `buildFilterQuery` :130-139 | Display-only reshaping of server-computed aggregates: the only `reduce()` splits `{label,value}` blocks into chart labels/values; no sums/averages/percentages are computed client-side (verified: no `.sort`/`.reduce` over values anywhere else). All aggregation happens in `GET /teacher/api/overview` (`handlers/teacher_overview.go`); charts/summary cards consume that JSON — `toFixed(2)` is formatting matching server `%.2f` convention. `buildFilterQuery` is HTTP query-string plumbing. |
| `web/js/monitor.js` — SSE handlers snapshot/answer/cheat/finished/rank/ping (:364-438), `normalize()` (:10-27), `fmtAnswer()` (:43-57), clock math `serverNow`/`skew`/`tick` (:30-31, :251-264), score/rank display `toFixed` (:177-181, :223), `renderWaiting` badge (:94-95) | Realtime display of server data (SSE/ALLOWED). `normalize` is key-name adaptation; `fmtAnswer` maps stored option indices to letters for the teacher's eyes; ranking array is server-ordered — NO client sorting or score math (verified: zero `.sort(` in `web/js`); `toFixed` is formatting of server-provided `score_auto`/`final_score`; clock skew only drives displayed countdowns re-synced by server pings; pending badge counts a fully server-rendered list — no cross-record rule, cannot go stale within a snapshot. Action buttons all POST to server which approves/rejects/cheat_toggle/remove/stop/close; the 409 `{working:N}` close-count is server-computed and merely displayed. |
| `web/js/monitor.js` — clock skew recompute on snapshot/ping (:377-379, :430-434) | Re-derives display clock from `server_now` — display-side only; enforcement of real time happens server-side. |
| `web/js/workspace.js` — SSE handlers snapshot (:38-52) / ping (:55-64) / rank (:358-380), prefill loop (:171-186), `render()` (:149-168), `showPreview` (:210-218) | SSE/display: snapshot drives reload decisions (UI), ping only re-syncs the visible countdown, rank renders a server-ordered array with no client score math (`entry.score` formatted via `toFixed` only), prefill paints saved answers from the server snapshot, preview text comes from the server's response blob. |
| JSON blobs `views/student/workspace.html:109`, `views/teacher/dashboard.html:129`, `views/teacher/monitor.html:128` | Server-provided data carriers (`{{.WSData}}`/`{{.OverviewJSON}}`/`{{.MonitorData}}`) — rendering of server JSON, ALLOWED; their consumers analyzed above. |
| `views/teacher/quiz_detail.html` | No inline script — only `<script src=/js/teacher.js>`. Its `data-*` attributes (bankModal endpoint, `#q-count`, `tr[data-seq]`, `data-fetch` participant-add form) drive findings F6/F7/F8/F12. |
| `views/teacher/results.html`, `views/student/history.html`, `views/student/home.html`, `views/student/profile.html`, `join.html`, and all `auth/*` pages | Server-rendered; grep confirms zero `<script>` tags (results/history compute Correct/Wrong/Most-chosen server-side — `handlers/results.go`). `grading.html`: browser min/max only, JS is transport (F12 note). |
| `views/teacher/monitor.html`, `dashboard.html` inline content | Only the JSON blob + `<script src>` tags — no executable inline logic (grep verified). |
| Duplicate one-liner scripts `app_foot.html:5` / `foot.html:3` | `bootstrap.bundle.min.js` vendor include — not app logic. |

## Search bars converted by this task

The three `type="search"` inputs in the teacher app were converted from client-side DOM filtering
(or dead inputs) to unified **server-side search with debounced live updates** (300 ms debounce →
fetch → region swap → `history.replaceState`; the server response is the sole source of rows):

| Input | Where | Old logic | Now |
|---|---|---|---|
| `#table-search` (shared toolbar) | 8 server pages incl. `/history` (student) | Already SSR via `?q=`; no auto-submit | Guru pages opt in via `data-live-search` on the form → debounced fetch swaps `#table-results` + `#table-count`. Student `/history` markup/logic unaffected (marker is rendered only for the guru role). |
| `#q-search` (composed-questions tab) | `/teacher/quiz/:id` | Client-side DOM substring filter: `filterQuestionTable()` (`teacher.js` :267-281, wired :311-313, also called from `appendQuestionRow` :193) | **Removed.** GET form with `name="qq"` → `?qq=` filters composed rows in the handler (`matchSearch`); live search swaps `#q-results` + `#q-count`. |
| `#bank-search` (bank modal) | `/teacher/quiz/:id` | Client-side text + length filter in `filterBankList()` (`teacher.js` :249-265, wired :296) | Text-query clause **removed** — `?bq=` filters the bank list server-side; live search swaps `#bank-results` and re-invokes the now length-only `filterBankList`. |

- **Params:** `q` = roster/toolbar search (unchanged semantics), `qq` = composed-questions search (new), `bq` = bank-modal search (new); each narrows only its own list. Non-reserved params round-trip through `Table.Params` hidden inputs (`list.go`).
- **Still client-side (F8):** the bank modal's **length buttons** (`data-bank-len`) keep their client-side bucket filtering — documented above as F8 and explicitly out of this mandate's scope.

---

## Methodology

- Read-only audit: no files were edited, no builds/tests run during the audit itself.
- Every claim is backed by direct file reads; line numbers come from `grep -n` on the same files in the audit session.
- Server corroboration claims verified by reading: `internal/handlers/student.go` (:316 `Join`, :927 deadline guard, :942-991 answer validation/advance, :1289 `FinishAttempt`), `teacher_questions.go` (:29-42 buckets, :205-251 `questionInput`), `teacher_quiz.go` (:197-202 timer guards, :556-746 `ComposeQuestions` incl. `return ok(c, nil)`), `results.go` (:352-397 score bounds), `internal/quizengine/anticheat.go` (:14-22), `internal/handlers/live.go` (:26-121 heartbeat registry), `internal/handlers/global.go` (:820-933 watchdog), `cmd/server/main.go` (:206 `WatchLoop`).
- Search-bar scope note: `web/js/teacher.js` `filterBankList` and `filterQuestionTable` (client-side substring filtering of the bank modal list and questions table over already-rendered DOM rows) were owned by the search-bar conversion and are covered in the cross-reference section above; `filterQuestionTable` was deleted and `filterBankList` trimmed to length-only by that work.
