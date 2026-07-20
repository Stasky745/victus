// Applied synchronously (this file has no defer/async) so the page never
// flashes the wrong theme before first paint.
(function () {
	var stored = localStorage.getItem("victus-theme");
	var dark = stored ? stored === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
	document.documentElement.classList.toggle("dark", dark);
})();

// The toggle button doesn't exist yet when the snippet above runs (it's in
// <head>, before <body>), so wiring up the click handler waits for the DOM —
// a no-op on pages with no toggle, like the login page.
document.addEventListener("DOMContentLoaded", function () {
	var toggle = document.getElementById("theme-toggle");
	if (!toggle) return;
	toggle.addEventListener("click", function () {
		var isDark = document.documentElement.classList.toggle("dark");
		localStorage.setItem("victus-theme", isDark ? "dark" : "light");
	});
});
