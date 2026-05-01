import { browser } from '$app/environment';
import { writable } from 'svelte/store';

export type Theme = 'light' | 'dark';

const STORAGE_KEY = 'knot.theme';

function pickInitial(): Theme {
	if (!browser) return 'light';
	const saved = localStorage.getItem(STORAGE_KEY);
	if (saved === 'light' || saved === 'dark') return saved;
	return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function applyToHtml(t: Theme) {
	if (!browser) return;
	const cls = document.documentElement.classList;
	if (t === 'dark') cls.add('dark');
	else cls.remove('dark');
}

const initial = pickInitial();
applyToHtml(initial);

export const theme = writable<Theme>(initial);

theme.subscribe((t) => {
	if (browser) localStorage.setItem(STORAGE_KEY, t);
	applyToHtml(t);
});

export function toggleTheme(): void {
	theme.update((t) => (t === 'dark' ? 'light' : 'dark'));
}
