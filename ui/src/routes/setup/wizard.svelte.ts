// Shared state for the M36 setup wizard. One central object lives
// for the duration of the user's onboarding session; every step
// reads + writes the fields it cares about. Saved to sessionStorage
// so a page reload mid-wizard doesn't lose progress.
//
// We use Svelte 5 runes ($state) inside a class so multiple
// components can import the same singleton.

import type { CapabilityReport, ScannedNetwork, Role } from '$lib/types';

const STORAGE_KEY = 'knot-setup-wizard-v2';

export type WizardStep =
	| 'welcome'
	| 'hardware'
	| 'role'
	| 'connection'
	| 'wifi'
	| 'admin'
	| 'review'
	| 'apply';

export const STEPS_ORDER: WizardStep[] = [
	'welcome',
	'hardware',
	'role',
	'connection',
	'wifi',
	'admin',
	'review',
	'apply'
];

// Per-step icon (Bootstrap Icons class).
export const STEP_ICONS: Record<WizardStep, string> = {
	welcome: 'bi-stars',
	hardware: 'bi-cpu',
	role: 'bi-router',
	connection: 'bi-globe2',
	wifi: 'bi-wifi',
	admin: 'bi-shield-lock',
	review: 'bi-check2-square',
	apply: 'bi-rocket-takeoff'
};

class WizardStore {
	step = $state<WizardStep>('welcome');

	// Hardware detection results, populated by the Hardware step.
	cap = $state<CapabilityReport | null>(null);
	capLoading = $state(true);
	capError = $state<string | null>(null);

	// Step-1 admin / device.
	deviceName = $state('knot');
	country = $state('RU');
	password = $state('');
	passwordConfirm = $state('');
	enableTelegram = $state(false);

	// Step-2 role.
	role = $state<Role>('wifi-router');

	// Step-3 connection — extender uplink.
	networks = $state<ScannedNetwork[]>([]);
	scanning = $state(false);
	uplinkSSID = $state('');
	uplinkPSK = $state('');

	// Step-3 connection — router WAN.
	wanInterface = $state('');
	wanMode = $state<'dhcp' | 'pppoe'>('dhcp');
	wanPppoeUser = $state('');
	wanPppoePass = $state('');

	// Step-4 AP.
	apSSID = $state('KnotNet');
	apPSK = $state('');
	apBand = $state<'2.4' | '5'>('2.4');
	apChannel = $state(0); // 0 = auto

	// Step-7/8 — apply tracking.
	applyID = $state<string | null>(null);
	applyError = $state<string | null>(null);

	get index(): number {
		return STEPS_ORDER.indexOf(this.step);
	}

	get progress(): number {
		// 0..1 fraction of completion.
		return this.index / (STEPS_ORDER.length - 1);
	}

	next() {
		const i = this.index;
		if (i < STEPS_ORDER.length - 1) {
			this.step = STEPS_ORDER[i + 1];
			this.persist();
		}
	}

	prev() {
		const i = this.index;
		if (i > 0) {
			this.step = STEPS_ORDER[i - 1];
			this.persist();
		}
	}

	go(s: WizardStep) {
		this.step = s;
		this.persist();
	}

	// Persist a JSON snapshot of the editable fields. Capability and
	// scan results are NOT persisted (they're transient).
	persist() {
		if (typeof sessionStorage === 'undefined') return;
		try {
			sessionStorage.setItem(
				STORAGE_KEY,
				JSON.stringify({
					step: this.step,
					deviceName: this.deviceName,
					country: this.country,
					role: this.role,
					uplinkSSID: this.uplinkSSID,
					wanInterface: this.wanInterface,
					wanMode: this.wanMode,
					apSSID: this.apSSID,
					apBand: this.apBand,
					apChannel: this.apChannel,
					enableTelegram: this.enableTelegram
				})
			);
		} catch {
			// Quota exceeded etc. — fail open.
		}
	}

	restore() {
		if (typeof sessionStorage === 'undefined') return;
		try {
			const raw = sessionStorage.getItem(STORAGE_KEY);
			if (!raw) return;
			const j = JSON.parse(raw);
			if (j.step) this.step = j.step;
			if (typeof j.deviceName === 'string') this.deviceName = j.deviceName;
			if (typeof j.country === 'string') this.country = j.country;
			if (j.role) this.role = j.role;
			if (typeof j.uplinkSSID === 'string') this.uplinkSSID = j.uplinkSSID;
			if (typeof j.wanInterface === 'string') this.wanInterface = j.wanInterface;
			if (j.wanMode) this.wanMode = j.wanMode;
			if (typeof j.apSSID === 'string') this.apSSID = j.apSSID;
			if (j.apBand) this.apBand = j.apBand;
			if (typeof j.apChannel === 'number') this.apChannel = j.apChannel;
			if (typeof j.enableTelegram === 'boolean') this.enableTelegram = j.enableTelegram;
		} catch {
			// Bad JSON — ignore.
		}
	}

	clear() {
		if (typeof sessionStorage === 'undefined') return;
		sessionStorage.removeItem(STORAGE_KEY);
	}
}

// Singleton.
export const wizard = new WizardStore();
