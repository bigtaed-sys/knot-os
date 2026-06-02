// Dynamic route — the set of plugin ids isn't known at build time, so
// adapter-static can't prerender this page. Falls back to the SPA
// shell (index.html) at runtime; the client router takes over.
export const prerender = false;
