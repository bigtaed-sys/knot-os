// Dynamic route — the set of MACs is not known at build time, so
// adapter-static cannot prerender this page. Falls back to the SPA
// shell (index.html) at runtime; SvelteKit's client router takes
// over from there.
export const prerender = false;
