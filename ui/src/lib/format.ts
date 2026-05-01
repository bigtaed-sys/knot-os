// Small formatters used across the UI. All i18n-aware via svelte-i18n's
// $t function passed in by the caller — that way these helpers stay
// pure and testable.

import { _ } from 'svelte-i18n';
import { get } from 'svelte/store';

/**
 * relativeTime renders an ISO timestamp as a localized "5 minutes ago"
 * string, falling back to "just now" / "never" for the boundary cases.
 *
 * Range buckets:
 *   < 1 min          → just_now
 *   < 1 hour         → minutes_ago
 *   < 24 hours       → hours_ago
 *   < 30 days        → days_ago
 *   ≥ 30 days        → falls back to a locale date string
 */
export function relativeTime(iso: string | undefined | null, now: Date = new Date()): string {
	const t = get(_);
	if (!iso) return t('devices.never');
	const d = new Date(iso);
	if (isNaN(d.getTime())) return t('devices.never');

	const diffMs = now.getTime() - d.getTime();
	if (diffMs < 0) return t('devices.just_now');
	const minutes = Math.floor(diffMs / 60_000);
	if (minutes < 1) return t('devices.just_now');
	if (minutes < 60) return t('devices.minutes_ago', { values: { n: minutes } });
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return t('devices.hours_ago', { values: { n: hours } });
	const days = Math.floor(hours / 24);
	if (days < 30) return t('devices.days_ago', { values: { n: days } });
	return d.toLocaleDateString();
}

/**
 * humanDays renders a sorted array of weekday numbers (0=Sun..6=Sat)
 * as a localized short string. Common groupings (every day,
 * Mon-Fri, weekend) are collapsed; otherwise comma-separated short
 * day names in week order.
 */
export function humanDays(days: number[]): string {
	const t = get(_);
	const set = new Set(days);
	const weekdays = [1, 2, 3, 4, 5];
	const weekends = [0, 6];
	const all = [0, 1, 2, 3, 4, 5, 6];

	const isAll = all.every((d) => set.has(d));
	const isWeekdays = set.size === 5 && weekdays.every((d) => set.has(d));
	const isWeekends = set.size === 2 && weekends.every((d) => set.has(d));

	if (isAll) return t('profiles.days_everyday');
	if (isWeekdays) return t('profiles.days_weekdays');
	if (isWeekends) return t('profiles.days_weekends');

	return Array.from(set)
		.sort((a, b) => a - b)
		.map((d) => t(`profiles.day_short_${d}`))
		.join(', ');
}

/**
 * deviceIcon returns a Bootstrap-icons class name based on a device
 * label heuristic. Falls back to a generic device glyph.
 */
export function deviceIcon(d: { label?: string; hostname?: string; display_name?: string }): string {
	const text = ((d.display_name || d.hostname || d.label) ?? '').toLowerCase();
	if (/iphone|ipad|android|phone|samsung|xiaomi|huawei|mi-/i.test(text)) return 'bi-phone';
	if (/ipad|tablet/i.test(text)) return 'bi-tablet';
	if (/macbook|laptop|notebook|thinkpad/i.test(text)) return 'bi-laptop';
	if (/imac|desktop|pc|workstation/i.test(text)) return 'bi-pc-display';
	if (/tv|chromecast|firestick|roku/i.test(text)) return 'bi-tv';
	if (/printer/i.test(text)) return 'bi-printer';
	if (/camera|cam|nest|wyze/i.test(text)) return 'bi-camera-video';
	if (/echo|alexa|google\s*home/i.test(text)) return 'bi-speaker';
	if (/raspberry|rpi|esp\d|arduino/i.test(text)) return 'bi-cpu';
	if (/router|switch|ap|access[\s-]point/i.test(text)) return 'bi-router';
	return 'bi-hdd-network';
}
