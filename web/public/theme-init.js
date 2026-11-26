try {
  var raw = localStorage.getItem("fased.ui-preferences.v1");
  var parsed = raw ? JSON.parse(raw) : null;
  var theme =
    parsed && (parsed.theme === "light" || parsed.theme === "dark")
      ? parsed.theme
      : matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light";
  document.documentElement.dataset.theme = theme;
  var meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute("content", theme === "light" ? "#F3F4F7" : "#0A0C10");
  }
} catch {}
