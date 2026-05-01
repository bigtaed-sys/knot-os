import { register, init, getLocaleFromNavigator, locale } from 'svelte-i18n';
import { browser } from '$app/environment';

export const SUPPORTED_LOCALES = ['ru', 'en'] as const;
export type Locale = (typeof SUPPORTED_LOCALES)[number];

const STORAGE_KEY = 'knot.locale';

/**
 * pickInitialLocale chooses the locale to load on first paint:
 *   1. Explicit user preference from localStorage.
 *   2. Browser language if it matches one of our supported locales.
 *   3. Russian by default — KnotOS targets RU users primarily.
 */
function pickInitialLocale(): Locale {
	if (browser) {
		const saved = localStorage.getItem(STORAGE_KEY);
		if (saved && (SUPPORTED_LOCALES as readonly string[]).includes(saved)) {
			return saved as Locale;
		}
		const nav = getLocaleFromNavigator();
		if (nav) {
			const short = nav.split('-')[0];
			if ((SUPPORTED_LOCALES as readonly string[]).includes(short)) {
				return short as Locale;
			}
		}
	}
	return 'ru';
}

// Lazy registration — svelte-i18n loads the JSON only when the locale
// is selected, keeping initial bundle small.
register('ru', () => import('./locales/ru.json'));
register('en', () => import('./locales/en.json'));

export function initI18n(): void {
	init({
		fallbackLocale: 'en',
		initialLocale: pickInitialLocale()
	});
}

export function setLocale(loc: Locale): void {
	if (browser) localStorage.setItem(STORAGE_KEY, loc);
	locale.set(loc);
}
