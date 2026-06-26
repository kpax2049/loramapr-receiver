/* ------------------------------------------------------------------
   LoRaMapr Portal — optional theme toggle (the ONLY JS in the reskin)
   ------------------------------------------------------------------
   Behavior:
     - No stored choice  -> follow the OS (prefers-color-scheme).
     - User clicks toggle -> set <html data-theme="light|dark"> and
       remember it in localStorage so it survives the server-rendered
       page reloads / auto-refreshes.
   Cost on a Pi Zero 2 W: negligible (runs once per page load, no timers).
   If you want a strictly zero-JS build: delete this file and the
   toggle <button>; the OS preference path in tokens.css still works.

   IMPORTANT: to avoid a flash of the wrong theme, also inline the tiny
   bootstrap snippet shown in README "Theme toggle" into <head> BEFORE
   the stylesheets. This file only wires up the click handler.
------------------------------------------------------------------ */
(function () {
  var KEY = "lmr-theme";
  var root = document.documentElement;

  function stored() {
    try { return localStorage.getItem(KEY); } catch (e) { return null; }
  }
  function apply(theme) {
    // theme is "light", "dark", or null (= follow OS)
    if (theme === "light" || theme === "dark") root.setAttribute("data-theme", theme);
    else root.removeAttribute("data-theme");
  }
  function current() {
    var s = stored();
    if (s === "light" || s === "dark") return s;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  // Ensure attribute matches stored choice on load (head snippet may already have done this).
  apply(stored());

  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-lmr-theme-toggle]");
    if (!btn) return;
    var next = current() === "dark" ? "light" : "dark";
    try { localStorage.setItem(KEY, next); } catch (e) {}
    apply(next);
  });
})();
