// Guru dashboard analytics — summary cards, Kelas/Major/Status filters, the
// three Chart.js charts and the quiz list. Data comes from the SSR blob
// (OverviewJSON → #teacher-overview-data) on load and from GET
// /teacher/api/overview?kelas=&jurusan=&status= on every filter change
// (envelope {ok:true,data:...}). Pure data shaping is defined first and
// exposed on window.teacherDashboard so it can be verified in Node without a
// DOM; all rendering lives inside boot() below. No framework, no alert().
(function () {
  "use strict";

  var ENDPOINT = "/teacher/api/overview";
  var FALLBACK_CHIP = "badge text-bg-secondary";
  var FILTERS = [
    { id: "dash-filter-kelas", key: "kelas" },
    { id: "dash-filter-jurusan", key: "jurusan" },
    { id: "dash-filter-status", key: "status" }
  ];

  // ======================================================================
  // Pure helpers (no DOM, no globals) — logic-level test surface
  // ======================================================================

  function num(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  function str(v) {
    return v === undefined || v === null ? "" : String(v);
  }

  function list(v) {
    return Array.isArray(v) ? v : [];
  }

  // Chart series per {label,value} block of the payload:
  //   {kelas:{labels,values}, jurusan:{labels,values}, nilai:{labels,values}}
  function buildChartData(payload) {
    var p = payload || {};
    function series(rows) {
      return list(rows).map(function (r) {
        return { label: str(r && r.label), value: r ? num(r.value) : 0 };
      }).reduce(function (acc, item) {
        acc.labels.push(item.label);
        acc.values.push(item.value);
        return acc;
      }, { labels: [], values: [] });
    }
    return {
      kelas: series(p.per_kelas),
      jurusan: series(p.per_jurusan),
      nilai: series(p.rekap_nilai ? p.rekap_nilai.buckets : null)
    };
  }

  // The four summary cards: data-summary key → number (murid_nonaktif is
  // part of the payload but has no card, so it is intentionally dropped).
  function summaryCells(payload) {
    var s = (payload && payload.summary) || {};
    return {
      total_murid: num(s.total_murid),
      quiz_aktif: num(s.quiz_aktif),
      quiz_nonaktif: num(s.quiz_nonaktif),
      peserta_mengerjakan: num(s.peserta_mengerjakan)
    };
  }

  // Quiz table rows — chip/label are precomputed server-side (same contract
  // as the SSR quiz tables), so they pass through verbatim.
  function quizRows(payload) {
    return list(payload && payload.quizzes).map(function (q) {
      q = q || {};
      return {
        id: num(q.id),
        judul: str(q.judul),
        code: str(q.code),
        status: str(q.status),
        chip: str(q.chip) || FALLBACK_CHIP,
        label: str(q.label),
        peserta: num(q.peserta)
      };
    });
  }

  // rekap_nilai headline numbers as display strings (2 decimals, matching
  // the app's fmtScore "%.2f" convention).
  function scoreStats(payload) {
    var r = (payload && payload.rekap_nilai) || {};
    return {
      avg: num(r.rata2).toFixed(2),
      count: String(num(r.jumlah)),
      min: num(r.nilai_min).toFixed(2),
      max: num(r.nilai_max).toFixed(2)
    };
  }

  // Select options: kelas/jurusan get a leading empty-value "All" entry
  // (their lists ship {id,nama}); status ships {value,label} including one.
  function filterOptions(payload) {
    var f = (payload && payload.filters) || {};
    var all = [{ value: "", label: "Semua" }];
    function idOpts(rows) {
      return list(rows).map(function (o) {
        return { value: str(o && o.id), label: str(o && o.nama) };
      });
    }
    function valOpts(rows) {
      return list(rows).map(function (o) {
        return { value: str(o && o.value), label: str(o && o.label) };
      });
    }
    return {
      kelas: all.concat(idOpts(f.kelas)),
      jurusan: all.concat(idOpts(f.jurusan)),
      status: valOpts(f.status)
    };
  }

  // payload.applied → select values (0 / "" both mean "All").
  function appliedFilters(payload) {
    var a = (payload && payload.applied) || {};
    return {
      kelas: num(a.kelas) ? String(num(a.kelas)) : "",
      jurusan: num(a.jurusan) ? String(num(a.jurusan)) : "",
      status: str(a.status)
    };
  }

  // Query string for the endpoint — empty values omitted, "" if none apply.
  function buildFilterQuery(filters) {
    var f = filters || {};
    var parts = [];
    ["kelas", "jurusan", "status"].forEach(function (key) {
      var v = str(f[key]);
      if (v) parts.push(key + "=" + encodeURIComponent(v));
    });
    return parts.length ? "?" + parts.join("&") : "";
  }

  // Expose the pure surface for logic-level verification / console debugging.
  var api = {
    buildChartData: buildChartData,
    summaryCells: summaryCells,
    quizRows: quizRows,
    scoreStats: scoreStats,
    filterOptions: filterOptions,
    appliedFilters: appliedFilters,
    buildFilterQuery: buildFilterQuery
  };
  (typeof window !== "undefined" ? window : globalThis).teacherDashboard = api;

  // Everything below needs a DOM — a Node harness evaluating this file stops
  // here and only sees the pure functions above.
  if (typeof document === "undefined") return;

  // ======================================================================
  // Rendering (DOM, Chart.js, fetch)
  // ======================================================================

  function boot() {
    var root = document.getElementById("teacher-dashboard");
    if (!root) return;

    var state = { charts: null, seq: 0 };

    // --- initial payload (SSR blob; no fetch on load) --------------------
    var initial = null;
    var blob = document.getElementById("teacher-overview-data");
    if (blob) {
      try { initial = JSON.parse(blob.textContent); } catch (e) { initial = null; }
    }
    if (!initial || typeof initial !== "object") {
      showError("Data dasbor tidak dapat dimuat. Muat ulang halaman untuk mencoba lagi.");
      return;
    }

    // --- inline error slot (fallback when quizToast is unavailable) ------
    function errorEl() { return document.getElementById("dash-error"); }
    function showError(msg) {
      var el = errorEl();
      if (!el) return;
      el.textContent = msg;
      el.hidden = false;
    }
    function hideError() {
      var el = errorEl();
      if (!el) return;
      el.textContent = "";
      el.hidden = true;
    }
    // ui.js quizToast is loaded app-wide (head, defer) — by the time a
    // filter fetch fails it exists; otherwise fall back to the inline slot.
    function notifyError(msg) {
      if (typeof window.quizToast === "function") {
        window.quizToast("danger", msg);
        hideError();
      } else {
        showError(msg);
      }
    }

    // --- theme-aware chart colors (CSS custom properties) ----------------
    function cssVar(name, fallback) {
      var v = getComputedStyle(document.documentElement).getPropertyValue(name);
      v = v ? v.trim() : "";
      return v || fallback;
    }
    function palette() {
      // Bootstrap-ish neutrals as last-resort fallbacks
      return [
        cssVar("--bs-primary", "#0D6EFD"),
        cssVar("--bs-info", "#0DCAF0"),
        cssVar("--bs-success", "#198754"),
        cssVar("--bs-warning", "#FFC107"),
        cssVar("--bs-danger", "#DC3545"),
        cssVar("--bs-secondary", "#6C757D")
      ];
    }
    function chartTheme() {
      return {
        tick: cssVar("--bs-secondary-color", "#6C757D"),
        grid: cssVar("--bs-border-color", "rgba(0, 0, 0, 0.1)"),
        font: cssVar("--bs-body-font-family", "sans-serif")
      };
    }
    function barScales(th) {
      var tick = { color: th.tick };
      var grid = { color: th.grid };
      return {
        x: { ticks: tick, grid: grid },
        y: { beginAtZero: true, ticks: { color: th.tick, precision: 0 }, grid: grid }
      };
    }

    // --- charts (created once, then updated in place) --------------------
    function createCharts(data) {
      var th = chartTheme();
      var pal = palette();
      var common = {
        responsive: true,
        maintainAspectRatio: false
      };
      var kelas = document.getElementById("dash-chart-kelas");
      var jurusan = document.getElementById("dash-chart-jurusan");
      var nilai = document.getElementById("dash-chart-nilai");
      state.charts = {
        kelas: new Chart(kelas, {
          type: "bar",
          data: {
            labels: data.kelas.labels,
            datasets: [{ label: "Murid", data: data.kelas.values,
                         backgroundColor: pal[0], borderRadius: 6 }]
          },
          options: { responsive: common.responsive, maintainAspectRatio: common.maintainAspectRatio,
                     plugins: { legend: { display: false } }, scales: barScales(th) }
        }),
        jurusan: new Chart(jurusan, {
          type: "doughnut",
          data: {
            labels: data.jurusan.labels,
            datasets: [{ data: data.jurusan.values, backgroundColor: pal, borderWidth: 0 }]
          },
          options: { responsive: common.responsive, maintainAspectRatio: common.maintainAspectRatio,
                     cutout: "60%",
                     plugins: { legend: { position: "right",
                       labels: { color: th.tick, font: { family: th.font } } } } }
        }),
        nilai: new Chart(nilai, {
          type: "bar",
          data: {
            labels: data.nilai.labels,
            datasets: [{ label: "Percobaan", data: data.nilai.values,
                         backgroundColor: pal[1], borderRadius: 6 }]
          },
          options: { responsive: common.responsive, maintainAspectRatio: common.maintainAspectRatio,
                     plugins: { legend: { display: false } }, scales: barScales(th) }
        })
      };
    }

    function setSeries(chart, series) {
      if (!chart) return;
      chart.data.labels = series.labels;
      if (chart.data.datasets[0]) chart.data.datasets[0].data = series.values;
    }

    // First call creates the charts; later calls update data in place
    // (chart.data + chart.update() — no destroy/recreate).
    function updateCharts(data) {
      if (typeof Chart === "undefined") return; // cards/table still render
      if (!state.charts) {
        createCharts(data);
        return;
      }
      setSeries(state.charts.kelas, data.kelas);
      setSeries(state.charts.jurusan, data.jurusan);
      setSeries(state.charts.nilai, data.nilai);
      state.charts.kelas.update();
      state.charts.jurusan.update();
      state.charts.nilai.update();
    }

    // Theme toggle flips <html data-bs-theme> → re-read the CSS variables so
    // grid/ticks/slices stay readable in both themes.
    function refreshChartTheme() {
      if (!state.charts) return;
      var th = chartTheme();
      var pal = palette();
      var c = state.charts;
      c.kelas.data.datasets[0].backgroundColor = pal[0];
      c.jurusan.data.datasets[0].backgroundColor = pal;
      c.nilai.data.datasets[0].backgroundColor = pal[1];
      [c.kelas, c.nilai].forEach(function (ch) {
        ch.options.scales.x.ticks.color = th.tick;
        ch.options.scales.y.ticks.color = th.tick;
        ch.options.scales.x.grid.color = th.grid;
        ch.options.scales.y.grid.color = th.grid;
      });
      c.jurusan.options.plugins.legend.labels.color = th.tick;
      c.kelas.update();
      c.jurusan.update();
      c.nilai.update();
    }

    // --- summary cards + score stats -------------------------------------
    function renderSummary(cells) {
      Object.keys(cells).forEach(function (key) {
        var el = root.querySelector('[data-summary="' + key + '"]');
        if (el) el.textContent = String(cells[key]);
      });
    }
    function renderScoreStats(stats) {
      ["avg", "count", "min", "max"].forEach(function (key) {
        var el = root.querySelector('[data-score-stat="' + key + '"]');
        if (el) el.textContent = stats[key];
      });
    }

    // --- quiz list --------------------------------------------------------
    function cell(text, cls) {
      var td = document.createElement("td");
      td.textContent = text;
      if (cls) td.className = cls;
      return td;
    }
    function renderQuizRows(rows) {
      var body = document.getElementById("dash-quiz-rows");
      if (!body) return;
      body.textContent = "";
      if (!rows.length) {
        var empty = document.createElement("tr");
        var td = cell("Tidak ada kuis yang cocok dengan filter saat ini.", "text-body-secondary");
        td.colSpan = 4;
        empty.appendChild(td);
        body.appendChild(empty);
        return;
      }
      rows.forEach(function (q) {
        var tr = document.createElement("tr");
        tr.appendChild(cell(q.judul));
        var code = document.createElement("code");
        code.textContent = q.code;
        var codeTd = document.createElement("td");
        codeTd.appendChild(code);
        tr.appendChild(codeTd);
        var statusTd = document.createElement("td");
        var chip = document.createElement("span");
        chip.className = q.chip;
        chip.textContent = q.label;
        statusTd.appendChild(chip);
        tr.appendChild(statusTd);
        tr.appendChild(cell(String(q.peserta), "text-end"));
        body.appendChild(tr);
      });
    }

    // --- filter selects ----------------------------------------------------
    function fillSelect(sel, options, value) {
      if (!sel) return;
      sel.textContent = "";
      options.forEach(function (o) {
        var opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        sel.appendChild(opt);
      });
      sel.value = value;
    }
    function selectById(id) { return document.getElementById(id); }
    // Options are reference data (all kelas/majors/statuses) — they only get
    // built once from the SSR blob; later payloads just re-sync the values.
    function renderFilterOptions(options, applied) {
      fillSelect(selectById("dash-filter-kelas"), options.kelas, applied.kelas);
      fillSelect(selectById("dash-filter-jurusan"), options.jurusan, applied.jurusan);
      fillSelect(selectById("dash-filter-status"), options.status, applied.status);
    }
    function syncFilterValues(applied) {
      FILTERS.forEach(function (f) {
        var sel = selectById(f.id);
        if (sel) sel.value = applied[f.key] || "";
      });
    }
    function readFilters() {
      var out = {};
      FILTERS.forEach(function (f) {
        var sel = selectById(f.id);
        out[f.key] = sel ? sel.value : "";
      });
      return out;
    }
    function setBusy(busy) {
      FILTERS.forEach(function (f) {
        var sel = selectById(f.id);
        if (sel) sel.disabled = busy;
      });
      var bar = document.getElementById("dash-filters");
      if (bar) bar.setAttribute("aria-busy", busy ? "true" : "false");
    }

    // --- payload application ----------------------------------------------
    function applyPayload(payload, withOptions) {
      renderSummary(summaryCells(payload));
      renderScoreStats(scoreStats(payload));
      renderQuizRows(quizRows(payload));
      if (withOptions) renderFilterOptions(filterOptions(payload), appliedFilters(payload));
      else syncFilterValues(appliedFilters(payload));
      updateCharts(buildChartData(payload));
    }

    // --- filter change → XHR refetch --------------------------------------
    function onFilterChange() {
      var qs = buildFilterQuery(readFilters());
      var seq = ++state.seq;
      setBusy(true);
      fetch(ENDPOINT + qs, { credentials: "same-origin" })
        .then(function (res) {
          return res.json().then(function (body) {
            return { status: res.status, body: body };
          }, function () {
            return { status: res.status, body: null }; // non-JSON error body
          });
        })
        .then(function (r) {
          if (r.status >= 400 || !r.body || r.body.ok !== true || !r.body.data) {
            throw new Error((r.body && r.body.message) ||
              "Permintaan gagal (" + r.status + "). Silakan coba lagi.");
          }
          if (seq !== state.seq) return; // a newer filter change superseded us
          hideError();
          applyPayload(r.body.data, false);
        })
        .catch(function (err) {
          if (seq !== state.seq) return;
          notifyError((err && err.message) || "Data dasbor tidak dapat disegarkan.");
        })
        .then(function () { // finally — ES5-safe
          if (seq === state.seq) setBusy(false);
        });
    }

    // --- boot --------------------------------------------------------------
    applyPayload(initial, true); // also creates the charts on first pass

    FILTERS.forEach(function (f) {
      var sel = selectById(f.id);
      if (sel) sel.addEventListener("change", onFilterChange);
    });

    if (typeof MutationObserver !== "undefined") {
      new MutationObserver(refreshChartTheme).observe(document.documentElement,
        { attributes: true, attributeFilter: ["data-bs-theme"] });
    }
  }

  boot();
})();
