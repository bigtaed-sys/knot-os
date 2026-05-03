<script lang="ts">
	import { onMount } from 'svelte';
	import { _ } from 'svelte-i18n';
	import { goto } from '$app/navigation';
	import { apiGet, apiPost, ApiError, ApiTimeoutError } from '$lib/api';
	import type { ScanResponse, ScannedNetwork } from '$lib/types';

	type Role = 'wifi-extender' | 'wifi-router';
	type Step = 1 | 2 | 3 | 4 | 5;
	let step = $state<Step>(1);

	// Capability probe — loaded on mount. Drives whether the role
	// step (step 2) is shown and what the connection step (step 3)
	// looks like.
	type EthAdapter = {
		interface: string;
		driver?: string;
		usb_vendor?: string;
		usb_product?: string;
		model: string;
	};
	type CapabilityReport = {
		pi: string;
		pi_model_string?: string;
		eth: EthAdapter[];
		guest_ap_capable: boolean;
		router_capable: boolean;
	};
	let cap = $state<CapabilityReport | null>(null);
	let capLoading = $state(true);
	let capError = $state<string | null>(null);

	async function loadCapability() {
		capLoading = true;
		capError = null;
		try {
			// Tight timeout: capability is a read of /sys/class/net,
			// which is microseconds in real life. 8s gives plenty of
			// margin if the LAN is briefly congested but lets the
			// user re-probe quickly when something is genuinely wedged
			// instead of staring at an indefinite spinner.
			cap = await apiGet<CapabilityReport>('/setup/capability', { timeoutMs: 8000 });
		} catch (e) {
			// Always fall back to "nothing detected" so the wizard can
			// proceed in extender-only mode regardless of the failure.
			cap = { pi: '', eth: [], guest_ap_capable: false, router_capable: false };
			if (e instanceof ApiTimeoutError) {
				capError = $_('setup.detect_timeout');
			} else if (e instanceof Error) {
				capError = e.message;
			}
		} finally {
			capLoading = false;
		}
	}

	// Step 1 — device + admin
	let deviceName = $state('knot');
	let country = $state('RU');
	let password = $state('');
	let passwordConfirm = $state('');
	const step1Error = $derived.by(() => {
		if (!deviceName) return $_('setup.err_device_name');
		if (!/^[a-zA-Z0-9_-]{1,63}$/.test(deviceName)) return $_('setup.err_device_name_format');
		if (!/^[A-Z]{2}$/.test(country)) return $_('setup.err_country');
		if (password.length < 8) return $_('setup.err_password_short');
		if (password !== passwordConfirm) return $_('setup.err_password_mismatch');
		return null;
	});

	// Step 2 — role choice. Only shown when capability says router is
	// feasible; otherwise role is forced to "wifi-extender".
	let role = $state<Role>('wifi-extender');
	const showRoleStep = $derived(cap?.router_capable === true);

	// Step 3a — extender uplink
	let networks = $state<ScannedNetwork[]>([]);
	let scanning = $state(false);
	let uplinkSSID = $state('');
	let uplinkPSK = $state('');
	const uplinkSecured = $derived(networks.find((n) => n.ssid === uplinkSSID)?.secured ?? true);
	const step3ExtenderError = $derived.by(() => {
		if (!uplinkSSID) return $_('setup.err_uplink');
		if (uplinkSecured && !uplinkPSK) return $_('setup.err_uplink_psk');
		return null;
	});

	async function scan() {
		scanning = true;
		try {
			const r = await apiGet<ScanResponse>('/setup/scan');
			networks = r.networks.toSorted((a, b) => b.rssi_dbm - a.rssi_dbm);
		} catch (e) {
			submitError = e instanceof Error ? e.message : String(e);
		} finally {
			scanning = false;
		}
	}

	// Step 3b — router WAN
	let wanInterface = $state('');
	const step3RouterError = $derived.by(() => {
		if (!wanInterface) return $_('setup.err_wan');
		return null;
	});

	const step3Error = $derived(role === 'wifi-router' ? step3RouterError : step3ExtenderError);

	// Step 4 — AP. Channel matters only for router (extender pins to
	// upstream). For 2.4 GHz the useful set is 1..13 (regional caps
	// vary; the validator on the server bounds 0..165).
	let apSSID = $state('KnotNet');
	let apPSK = $state('');
	let apBand = $state<'2.4'>('2.4');
	let apChannel = $state(6);
	const step4Error = $derived.by(() => {
		if (!apSSID) return $_('setup.err_ap_ssid');
		if (apPSK.length > 0 && apPSK.length < 8) return $_('setup.err_ap_psk');
		if (role === 'wifi-router' && (apChannel < 1 || apChannel > 13)) {
			return $_('setup.err_ap_channel');
		}
		return null;
	});

	// Submit
	let submitting = $state(false);
	let submitError = $state<string | null>(null);

	async function submit() {
		submitting = true;
		submitError = null;
		try {
			const body: Record<string, unknown> = {
				device: { name: deviceName, country },
				password,
				role,
				ap: {
					ssid: apSSID,
					psk: apPSK,
					band: apBand,
					channel: role === 'wifi-router' ? apChannel : 0
				}
			};
			if (role === 'wifi-extender') {
				body.uplink = { ssid: uplinkSSID, psk: uplinkSecured ? uplinkPSK : '' };
			} else {
				body.wan = { interface: wanInterface, mode: 'dhcp' };
			}
			await apiPost('/setup/complete', body);
			goto('/', { replaceState: true });
		} catch (e) {
			if (e instanceof ApiError) {
				const ebody = e.body as { error?: { message?: string } } | undefined;
				submitError = ebody?.error?.message ?? e.message;
			} else {
				submitError = e instanceof Error ? e.message : String(e);
			}
		} finally {
			submitting = false;
		}
	}

	function next() {
		if (step === 1 && !step1Error) {
			// Skip the role step when only extender is feasible.
			step = (showRoleStep ? 2 : 3) as Step;
		} else if (step === 2) {
			step = 3;
		} else if (step === 3 && !step3Error) {
			step = 4;
		} else if (step === 4 && !step4Error) {
			step = 5;
		}
	}
	function back() {
		if (step === 5) step = 4;
		else if (step === 4) step = 3;
		else if (step === 3) step = (showRoleStep ? 2 : 1) as Step;
		else if (step === 2) step = 1;
	}

	// On entering step 3 in extender mode, kick off a scan.
	$effect(() => {
		if (step === 3 && role === 'wifi-extender' && networks.length === 0 && !scanning) scan();
	});

	// On entering step 3 in router mode, default the interface to
	// the first detected adapter so the user can just hit "next".
	$effect(() => {
		if (step === 3 && role === 'wifi-router' && !wanInterface && cap?.eth.length) {
			wanInterface = cap.eth[0].interface;
		}
	});

	function rssiBars(dbm: number): number {
		if (dbm >= -50) return 4;
		if (dbm >= -65) return 3;
		if (dbm >= -75) return 2;
		if (dbm >= -85) return 1;
		return 0;
	}

	const steps = $derived([
		{ n: 1, key: 'setup.step_device', icon: 'bi-router' },
		...(showRoleStep ? [{ n: 2, key: 'setup.step_role', icon: 'bi-diagram-2' }] : []),
		{
			n: 3,
			key: role === 'wifi-router' ? 'setup.step_wan' : 'setup.step_uplink',
			icon: role === 'wifi-router' ? 'bi-ethernet' : 'bi-cloud-arrow-up'
		},
		{ n: 4, key: 'setup.step_ap', icon: 'bi-broadcast' },
		{ n: 5, key: 'setup.step_review', icon: 'bi-check2-circle' }
	]);

	onMount(loadCapability);

	const channelOptions = [1, 6, 11, 2, 3, 4, 5, 7, 8, 9, 10, 12, 13];
</script>

<div class="min-h-screen bg-gradient-to-br from-zinc-50 via-zinc-50 to-brand-50/30 dark:from-zinc-950 dark:via-zinc-900 dark:to-brand-950/40">
	<div class="max-w-2xl mx-auto p-4 sm:p-8">
		<!-- Brand -->
		<header class="flex flex-col items-center mb-8">
			<div class="w-14 h-14 rounded-2xl bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center shadow-lg mb-3">
				<i class="bi bi-diagram-3-fill text-white text-2xl"></i>
			</div>
			<h1 class="text-2xl font-semibold">{$_('setup.title')}</h1>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('setup.subtitle')}</p>
		</header>

		<!-- Stepper -->
		<ol class="hidden sm:flex items-center justify-between gap-2 mb-6">
			{#each steps as s, i}
				<li class="flex-1 flex items-center gap-2 min-w-0">
					<div class="flex items-center gap-2.5 flex-1 min-w-0">
						<div
							class="
								shrink-0 w-9 h-9 rounded-full flex items-center justify-center text-sm font-medium transition-colors
								{step >= s.n
									? 'bg-brand-600 text-white'
									: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-400 dark:text-zinc-500'}
							"
						>
							{#if step > s.n}
								<i class="bi bi-check-lg text-base"></i>
							{:else}
								<i class="bi {s.icon} text-base"></i>
							{/if}
						</div>
						<div
							class="text-xs font-medium truncate
							{step >= s.n ? 'text-zinc-900 dark:text-zinc-100' : 'text-zinc-400 dark:text-zinc-500'}"
						>
							{$_(s.key)}
						</div>
					</div>
					{#if i < steps.length - 1}
						<div class="flex-1 h-px bg-zinc-200 dark:bg-zinc-800 mx-1"></div>
					{/if}
				</li>
			{/each}
		</ol>

		<!-- Step content -->
		<div class="surface p-6 mb-4">
			{#if step === 1}
				<h2 class="text-lg font-semibold mb-4">{$_('setup.device_section_title')}</h2>
				<div class="space-y-4">
					<div>
						<label class="label" for="dn">{$_('setup.device_name_label')}</label>
						<input id="dn" class="input" bind:value={deviceName} />
						<p class="help">
							{$_('setup.device_name_help', { values: { name: deviceName || 'knot' } })}
						</p>
					</div>
					<div>
						<label class="label" for="cc">{$_('setup.country_label')}</label>
						<input
							id="cc"
							class="input uppercase"
							maxlength="2"
							bind:value={country}
						/>
						<p class="help">{$_('setup.country_help')}</p>
					</div>
					<div>
						<label class="label" for="pw">{$_('setup.admin_password_label')}</label>
						<input
							id="pw"
							type="password"
							class="input"
							bind:value={password}
							autocomplete="new-password"
						/>
						<p class="help">{$_('setup.admin_password_help')}</p>
					</div>
					<div>
						<label class="label" for="pwc">{$_('setup.admin_password_confirm_label')}</label>
						<input
							id="pwc"
							type="password"
							class="input"
							bind:value={passwordConfirm}
							autocomplete="new-password"
						/>
					</div>
					{#if step1Error && (password || passwordConfirm)}
						<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400">
							<i class="bi bi-exclamation-circle"></i>
							{step1Error}
						</p>
					{/if}

					<!-- Hardware probe summary so the user can see if the
					     dongle was detected before they leave step 1. -->
					<div class="surface-muted p-3 rounded-lg text-sm space-y-2">
						<div class="flex items-center justify-between gap-2 flex-wrap">
							<div class="font-medium flex items-center gap-2">
								<i class="bi bi-cpu text-brand-500"></i>
								{$_('setup.detected_section')}
							</div>
							<button
								type="button"
								class="text-xs text-brand-600 dark:text-brand-400 hover:underline disabled:opacity-50"
								onclick={loadCapability}
								disabled={capLoading}
							>
								{#if capLoading}
									<span class="spinner inline-block align-middle"></span>
									{$_('setup.detect_rescan')}
								{:else}
									<i class="bi bi-arrow-repeat"></i>
									{$_('setup.detect_rescan')}
								{/if}
							</button>
						</div>
						{#if capLoading}
							<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('setup.detect_loading')}</p>
						{:else if capError}
							<p class="text-xs text-rose-600 dark:text-rose-400">
								<i class="bi bi-exclamation-triangle"></i>
								{capError}
							</p>
						{:else if cap}
							{#if cap.pi_model_string}
								<p class="text-xs text-zinc-500 dark:text-zinc-400">
									<span class="font-mono">{cap.pi_model_string}</span>
								</p>
							{/if}
							{#if cap.eth.length > 0}
								<ul class="text-xs space-y-0.5">
									{#each cap.eth as a}
										<li class="flex items-center gap-2">
											<i class="bi bi-ethernet text-emerald-500"></i>
											<span class="font-mono">{a.interface}</span>
											<span class="text-zinc-500 dark:text-zinc-400">— {a.model}</span>
											{#if a.usb_vendor && a.usb_product}
												<span class="text-zinc-400 font-mono">
													({a.usb_vendor}:{a.usb_product})
												</span>
											{/if}
										</li>
									{/each}
								</ul>
								<p class="text-xs text-emerald-700 dark:text-emerald-400">
									{$_('setup.detect_router_available')}
								</p>
							{:else}
								<p class="text-xs text-zinc-500 dark:text-zinc-400">
									<i class="bi bi-info-circle"></i>
									{$_('setup.detect_no_eth')}
								</p>
							{/if}
						{/if}
					</div>
				</div>
			{:else if step === 2}
				<div class="flex items-start justify-between gap-3 mb-1 flex-wrap">
					<h2 class="text-lg font-semibold">{$_('setup.role_section_title')}</h2>
					<button
						type="button"
						class="text-xs text-brand-600 dark:text-brand-400 hover:underline disabled:opacity-50"
						onclick={loadCapability}
						disabled={capLoading}
					>
						{#if capLoading}
							<span class="spinner inline-block align-middle"></span>
						{:else}
							<i class="bi bi-arrow-repeat"></i>
						{/if}
						{$_('setup.detect_rescan')}
					</button>
				</div>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">
					{$_('setup.role_section_subtitle')}
				</p>
				{#if cap}
					{#if cap.eth.length > 0}
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">
							<i class="bi bi-ethernet mr-1"></i>
							{$_('setup.role_detected', {
								values: { adapter: cap.eth[0].model, iface: cap.eth[0].interface }
							})}
						</p>
					{/if}
				{/if}
				<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
					<button
						type="button"
						class="text-left p-4 rounded-xl border-2 transition-colors
							{role === 'wifi-extender'
								? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10'
								: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}"
						onclick={() => (role = 'wifi-extender')}
					>
						<i class="bi bi-cloud-arrow-up text-2xl text-brand-500"></i>
						<div class="font-semibold mt-2">{$_('setup.role_extender_title')}</div>
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
							{$_('setup.role_extender_help')}
						</p>
					</button>
					<button
						type="button"
						class="text-left p-4 rounded-xl border-2 transition-colors
							{role === 'wifi-router'
								? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10'
								: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}"
						onclick={() => (role = 'wifi-router')}
					>
						<i class="bi bi-router text-2xl text-brand-500"></i>
						<div class="font-semibold mt-2">{$_('setup.role_router_title')}</div>
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
							{$_('setup.role_router_help')}
						</p>
					</button>
				</div>
			{:else if step === 3 && role === 'wifi-extender'}
				<div class="flex items-start justify-between gap-4 mb-4">
					<div>
						<h2 class="text-lg font-semibold">{$_('setup.uplink_section_title')}</h2>
						<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">
							{$_('setup.uplink_section_subtitle')}
						</p>
					</div>
					<button type="button" class="btn-ghost shrink-0" onclick={scan} disabled={scanning}>
						{#if scanning}
							<span class="spinner"></span>
							{$_('setup.scanning')}
						{:else}
							<i class="bi bi-arrow-repeat"></i>
							{$_('setup.rescan')}
						{/if}
					</button>
				</div>
				<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-2">
					{$_('setup.networks_visible', { values: { count: networks.length } })}
				</p>

				<ul class="surface-muted divide-y divide-zinc-200 dark:divide-zinc-700/50 max-h-72 overflow-y-auto">
					{#each networks as n}
						{@const bars = rssiBars(n.rssi_dbm)}
						<li>
							<label class="flex items-center gap-3 px-3 py-2.5 cursor-pointer hover:bg-zinc-100 dark:hover:bg-zinc-800/50">
								<input
									type="radio"
									bind:group={uplinkSSID}
									value={n.ssid}
									class="text-brand-600 focus:ring-brand-500"
								/>
								<div class="flex-1 min-w-0">
									<div class="font-medium truncate flex items-center gap-2">
										<span>{n.ssid || $_('setup.hidden_network')}</span>
										{#if !n.secured}
											<span class="badge badge-warn">
												<i class="bi bi-unlock"></i>
												{$_('setup.open_network')}
											</span>
										{/if}
									</div>
									<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono">
										{n.rssi_dbm} dBm · ch {n.channel} · {n.band} GHz
									</div>
								</div>
								<span class="flex items-end gap-0.5 {bars >= 3 ? 'text-emerald-500' : bars >= 2 ? 'text-amber-500' : 'text-red-500'}">
									{#each [1, 2, 3, 4] as b}
										<span
											class="w-1 rounded-sm bg-current"
											class:opacity-25={b > bars}
											style="height: {3 + b * 2}px"
										></span>
									{/each}
								</span>
							</label>
						</li>
					{/each}
				</ul>

				{#if uplinkSSID && uplinkSecured}
					<div class="mt-4">
						<label class="label" for="upsk">
							{$_('setup.uplink_password_label', { values: { ssid: uplinkSSID } })}
						</label>
						<input id="upsk" type="password" class="input" bind:value={uplinkPSK} autocomplete="off" />
					</div>
				{/if}
			{:else if step === 3 && role === 'wifi-router'}
				<h2 class="text-lg font-semibold mb-1">{$_('setup.wan_section_title')}</h2>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">
					{$_('setup.wan_section_subtitle')}
				</p>
				{#if cap?.eth && cap.eth.length > 0}
					<ul class="surface-muted divide-y divide-zinc-200 dark:divide-zinc-700/50">
						{#each cap.eth as a}
							<li>
								<label class="flex items-center gap-3 px-3 py-2.5 cursor-pointer hover:bg-zinc-100 dark:hover:bg-zinc-800/50">
									<input
										type="radio"
										bind:group={wanInterface}
										value={a.interface}
										class="text-brand-600 focus:ring-brand-500"
									/>
									<div class="flex-1 min-w-0">
										<div class="font-medium truncate">{a.model}</div>
										<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono">
											{a.interface}
											{#if a.usb_vendor && a.usb_product}
												· {a.usb_vendor}:{a.usb_product}
											{/if}
											{#if a.driver}· {a.driver}{/if}
										</div>
									</div>
								</label>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('setup.wan_none_detected')}</p>
				{/if}
			{:else if step === 4}
				<h2 class="text-lg font-semibold mb-1">{$_('setup.ap_section_title')}</h2>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">
					{$_('setup.ap_section_subtitle')}
				</p>
				<div class="space-y-4">
					<div>
						<label class="label" for="apssid">{$_('setup.ap_ssid_label')}</label>
						<input id="apssid" class="input" bind:value={apSSID} />
					</div>
					<div>
						<label class="label" for="appsk">{$_('setup.ap_password_label')}</label>
						<input id="appsk" type="password" class="input" bind:value={apPSK} autocomplete="off" />
						<p class="help">{$_('setup.ap_password_help')}</p>
					</div>
					<div>
						<label class="label" for="apband">{$_('setup.band_label')}</label>
						<select id="apband" class="input" bind:value={apBand}>
							<option value="2.4">2.4 GHz</option>
						</select>
						<p class="help">{$_('setup.band_help')}</p>
					</div>
					{#if role === 'wifi-router'}
						<div>
							<label class="label" for="apch">{$_('setup.channel_label')}</label>
							<select id="apch" class="input" bind:value={apChannel}>
								{#each channelOptions as c}
									<option value={c}>{c}</option>
								{/each}
							</select>
							<p class="help">{$_('setup.channel_help')}</p>
						</div>
					{/if}
					{#if step4Error}
						<p class="flex items-center gap-1.5 text-sm text-red-600 dark:text-red-400">
							<i class="bi bi-exclamation-circle"></i>
							{step4Error}
						</p>
					{/if}
				</div>
			{:else if step === 5}
				<h2 class="text-lg font-semibold mb-4">{$_('setup.review_section_title')}</h2>
				<dl class="surface-muted p-4 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('setup.review_device')}</dt>
					<dd class="font-medium">{deviceName} <span class="text-zinc-400">({country})</span></dd>
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('setup.review_role')}</dt>
					<dd class="font-medium">
						{role === 'wifi-router' ? $_('setup.role_router_title') : $_('setup.role_extender_title')}
					</dd>
					{#if role === 'wifi-extender'}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('setup.review_uplink')}</dt>
						<dd class="font-medium truncate">{uplinkSSID}</dd>
					{:else}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('setup.review_wan')}</dt>
						<dd class="font-medium truncate">{wanInterface}</dd>
					{/if}
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('setup.review_ap')}</dt>
					<dd class="font-medium truncate">
						{apSSID}
						<span class="text-zinc-400">
							({apBand} GHz{#if role === 'wifi-router'}, ch {apChannel}{/if})
						</span>
					</dd>
				</dl>

				<div class="mt-4 flex items-start gap-3 p-3 rounded-lg bg-amber-50 dark:bg-amber-500/10 text-amber-900 dark:text-amber-300">
					<i class="bi bi-info-circle text-base mt-0.5 shrink-0"></i>
					<p class="text-sm">
						{#if role === 'wifi-extender'}
							{$_('setup.review_warn', {
								values: { setup: 'KnotOS-setup-XXXX', ap: apSSID, uplink: uplinkSSID }
							})}
						{:else}
							{$_('setup.review_warn_router', {
								values: { setup: 'KnotOS-setup-XXXX', ap: apSSID, wan: wanInterface }
							})}
						{/if}
					</p>
				</div>

				{#if submitError}
					<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
						<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
						<span>{submitError}</span>
					</div>
				{/if}
			{/if}
		</div>

		<!-- Actions -->
		<div class="flex justify-between">
			{#if step > 1}
				<button class="btn-ghost" onclick={back} disabled={submitting}>
					<i class="bi bi-arrow-left"></i>
					{$_('setup.back')}
				</button>
			{:else}
				<span></span>
			{/if}

			{#if step < 5}
				<button
					class="btn-primary"
					onclick={next}
					disabled={(step === 1 && (!!step1Error || capLoading)) ||
						(step === 3 && !!step3Error) ||
						(step === 4 && !!step4Error)}
				>
					{$_('setup.next')}
					<i class="bi bi-arrow-right"></i>
				</button>
			{:else}
				<button class="btn-primary" onclick={submit} disabled={submitting}>
					{#if submitting}
						<span class="spinner"></span>
						{$_('setup.applying')}
					{:else}
						<i class="bi bi-check2"></i>
						{$_('setup.apply')}
					{/if}
				</button>
			{/if}
		</div>
	</div>
</div>
