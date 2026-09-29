// Light/dark theme toggle: reads localStorage("theme"), applies it to
// <html data-bs-theme>, toggles on click of [data-theme-toggle]. No framework.
(function () {
  var KEY = "theme";

  function current() {
    return document.documentElement.getAttribute("data-bs-theme") === "dark"
      ? "dark"
      : "light";
  }

  function apply(theme) {
    document.documentElement.setAttribute("data-bs-theme", theme);
    try {
      localStorage.setItem(KEY, theme);
    } catch (e) {
      /* storage unavailable (private mode) — attribute still applied */
    }
  }

  try {
    var saved = localStorage.getItem(KEY);
    if (saved === "dark" || saved === "light") {
      apply(saved);
    }
  } catch (e) {
    /* ignore */
  }

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-theme-toggle]");
    if (!btn) return;
    apply(current() === "dark" ? "light" : "dark");
  });
})();
