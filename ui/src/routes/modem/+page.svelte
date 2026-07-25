<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, apiPost, ApiError } from '$lib/api';
	import Tabs from '$lib/components/Tabs.svelte';
	import type { ModemResponse, ModemStatus, ModemUsage, ModemNetwork } from '$lib/types';

	let status = $state<ModemStatus>({ present: false, signal_percent: 0 });
	let asWAN = $state(false);
	let apn = $state('');
	let username = $state('');
	let hasPin = $state(false);
	let pin = $state('');
	let pinTouched = $state(false);
	let simSlot = $state(0);
	let dataLimitMB = $state(0);
	let cycleResetDay = $state(1);
	let usage = $state<ModemUsage | null>(null);
	let routerMode = $state(true);

	// Network selection (access tech + bands)
	let network = $state<ModemNetwork | null>(null);
	let networkBusy = $state(false);
	// USSD (prepaid balance)
	let ussdCode = $state('');
	let ussdResp = $state<string | null>(null);
	let ussdBusy = $state(false);
	let ctlError = $state<string | null>(null);

	function ctlErr(e: unknown): string {
		if (e instanceof ApiError) {
			const b = e.body as { error?: { message?: string } } | undefined;
			return b?.error?.message ?? e.message;
		}
		return e instanceof Error ? e.message : String(e);
	}

	async function loadNetwork() {
		try {
			network = await apiGet<ModemNetwork>('/modem/network', { timeoutMs: 8000 });
		} catch {
			network = null; // backend without a modem controller / no modem
		}
	}

	async function applyModes(modes: string[]) {
		networkBusy = true;
		ctlError = null;
		try {
			await apiPut('/modem/network', { modes }, { timeoutMs: 30000 });
			await loadNetwork();
		} catch (e) {
			ctlError = ctlErr(e);
		} finally {
			networkBusy = false;
		}
	}

	async function toggleBand(band: string) {
		if (!network) return;
		const cur = new Set(network.current_bands.filter((b) => b !== 'any'));
		if (cur.has(band)) cur.delete(band);
		else cur.add(band);
		networkBusy = true;
		ctlError = null;
		try {
			await apiPut('/modem/network', { bands: [...cur] }, { timeoutMs: 30000 });
			await loadNetwork();
		} catch (e) {
			ctlError = ctlErr(e);
		} finally {
			networkBusy = false;
		}
	}

	async function runUSSD() {
		if (!ussdCode.trim()) return;
		ussdBusy = true;
		ussdResp = null;
		ctlError = null;
		try {
			const r = await apiPost<{ response: string }>(
				'/modem/ussd',
				{ code: ussdCode.trim() },
				{ timeoutMs: 30000 }
			);
			ussdResp = r.response;
		} catch (e) {
			ctlError = ctlErr(e);
		} finally {
			ussdBusy = false;
		}
	}

	let loading = $state(true);
	let saving = $state(false);
	let error = $state<string | null>(null);
	let savedFlash = $state(false);
	let timer: ReturnType<typeof setInterval> | null = null;

	async function refresh(initial = false) {
		try {
			const r = await apiGet<ModemResponse>('/modem');
			status = r.status;
			routerMode = r.router_mode;
			usage = r.usage ?? null;
			if (initial) {
				asWAN = r.as_wan;
				apn = r.apn;
				username = r.username;
				hasPin = r.has_pin;
				simSlot = r.sim_slot;
				dataLimitMB = r.data_limit_mb;
				cycleResetDay = r.cycle_reset_day || 1;
			}
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			if (!(e instanceof ApiError && e.status === 503)) {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			loading = false;
		}
	}

	async function save() {
		saving = true;
		error = null;
		try {
			const body: Record<string, unknown> = {
				as_wan: asWAN,
				apn,
				username,
				data_limit_mb: dataLimitMB,
				cycle_reset_day: cycleResetDay
			};
			if (pinTouched) body.pin = pin;
			if ((status.sim_slots ?? 0) > 1) body.sim_slot = simSlot;
			await apiPut('/modem', body, { timeoutMs: 60000 });
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 2500);
			pin = '';
			pinTouched = false;
			await refresh(true);
		} catch (e) {
			if (e instanceof ApiError) {
				const b = e.body as { error?: { message?: string } } | undefined;
				error = b?.error?.message ?? e.message;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			saving = false;
		}
	}

	function bars(pct: number): number {
		if (pct <= 0) return 0;
		return Math.max(1, Math.min(4, Math.ceil(pct / 25)));
	}

	function fmtBytes(n: number): string {
		if (!n) return '0 B';
		const u = ['B', 'KB', 'MB', 'GB', 'TB'];
		const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
		return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : i < 3 ? 1 : 2)} ${u[i]}`;
	}

	// Usage as a fraction of the cap (0..1), and percent for the bar.
	const limitBytes = $derived(dataLimitMB > 0 ? dataLimitMB * 1024 * 1024 : 0);
	const usedFrac = $derived(
		limitBytes > 0 && usage ? Math.min(1, usage.total_bytes / limitBytes) : 0
	);
	const overLimit = $derived(limitBytes > 0 && !!usage && usage.total_bytes >= limitBytes);

	// In-page tabs — keeps the page short instead of one long card stack.
	let activeTab = $state('status');
	const tabList = $derived([
		{ id: 'status', label: $_('modem.tab_status'), icon: 'bi-broadcast' },
		{ id: 'data', label: $_('modem.tab_data'), icon: 'bi-bar-chart-line' },
		{ id: 'network', label: $_('modem.tab_network'), icon: 'bi-reception-4' },
		{ id: 'settings', label: $_('modem.tab_settings'), icon: 'bi-sliders2' }
	]);
	// The Save button applies config fields, which all live on Settings.
	const showSave = $derived(activeTab === 'settings');

	const stateColor = $derived(
		status.state === 'connected'
			? 'badge-ok'
			: status.lock_required === 'sim-pin'
				? 'badge-warn'
				: status.present
					? 'badge-info'
					: 'badge-neutral'
	);

	onMount(() => {
		refresh(true);
		loadNetwork();
		timer = setInterval(() => refresh(false), 5000);
	});
	onDestroy(() => {
		if (timer) clearInterval(timer);
	});
</script>

<svelte:head>
	<title>{$_('modem.title')} · KnotOS</title>
</svelte:head>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('modem.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('modem.subtitle')}</p>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400"><div class="spinner"></div></div>
{:else}
	<div class="surface border-amber-300 dark:border-amber-500/30 p-3 mb-5 text-xs flex items-start gap-2">
		<i class="bi bi-cone-striped text-amber-500 mt-0.5"></i>
		<span>{$_('modem.experimental')}</span>
	</div>

	{#if !routerMode}
		<div class="surface border-amber-300 dark:border-amber-500/30 p-4 mb-5 text-sm flex items-start gap-3">
			<i class="bi bi-info-circle text-amber-500 text-lg mt-0.5"></i>
			<span>{$_('modem.not_router')}</span>
		</div>
	{/if}

	<Tabs tabs={tabList} bind:active={activeTab} />

	{#if activeTab === 'status'}
	<!-- Live status -->
	<section class="surface p-5 mb-5">
		<div class="flex items-center gap-4">
			<div class="w-12 h-12 shrink-0 rounded-xl bg-brand-100 dark:bg-brand-500/15 flex items-center justify-center text-brand-700 dark:text-brand-300">
				<i class="bi bi-sim text-xl"></i>
			</div>
			<div class="flex-1 min-w-0">
				{#if status.present}
					<div class="flex items-center gap-2 flex-wrap">
						<span class="font-medium truncate">
							{status.manufacturer || ''} {status.model || $_('modem.unknown_model')}
						</span>
						<span class="badge {stateColor}">{status.state || '—'}</span>
					</div>
					<div class="text-sm text-zinc-500 dark:text-zinc-400 mt-1 flex flex-wrap gap-x-4 gap-y-1">
						{#if status.operator}<span><i class="bi bi-broadcast-pin"></i> {status.operator}</span>{/if}
						{#if status.tech}<span class="uppercase">{status.tech}</span>{/if}
						{#if status.interface}<span class="font-mono text-xs">{status.interface}</span>{/if}
					</div>
				{:else}
					<div class="font-medium">{$_('modem.none')}</div>
					<div class="text-sm text-zinc-500 dark:text-zinc-400 mt-0.5">{$_('modem.none_help')}</div>
				{/if}
			</div>
			{#if status.present}
				<!-- Signal bars -->
				<div class="flex items-end gap-0.5 h-8" title="{status.signal_percent}%">
					{#each [1, 2, 3, 4] as b}
						<div
							class="w-1.5 rounded-sm {b <= bars(status.signal_percent)
								? 'bg-brand-500'
								: 'bg-zinc-200 dark:bg-zinc-700'}"
							style="height: {b * 25}%"
						></div>
					{/each}
				</div>
			{/if}
		</div>

		{#if status.lock_required === 'sim-pin'}
			<div class="mt-3 text-sm text-amber-700 dark:text-amber-400 flex items-center gap-2">
				<i class="bi bi-lock"></i>{$_('modem.pin_required')}
			</div>
		{/if}

		{#if status.last_error && status.state !== 'connected'}
			<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
				<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
				<span>
					<span class="font-medium">{$_('modem.connect_failed')}</span>
					<span class="block mt-0.5 font-mono text-xs break-all">{status.last_error}</span>
				</span>
			</div>
		{/if}
	</section>
	{/if}

	{#if activeTab === 'data'}
	<!-- Data usage + signal history -->
	{#if usage}
		<section class="surface p-5 mb-5">
			<div class="flex items-center justify-between gap-3 mb-3">
				<h2 class="font-medium">{$_('modem.usage_title')}</h2>
				<span class="text-xs text-zinc-500 dark:text-zinc-400">
					{$_('modem.usage_since', { values: { date: new Date(usage.cycle_start).toLocaleDateString() } })}
				</span>
			</div>

			<div class="flex items-baseline gap-2">
				<span class="text-2xl font-semibold tabular-nums">{fmtBytes(usage.total_bytes)}</span>
				{#if dataLimitMB > 0}
					<span class="text-sm text-zinc-500 dark:text-zinc-400">/ {fmtBytes(limitBytes)}</span>
				{/if}
			</div>
			<div class="text-xs text-zinc-500 dark:text-zinc-400 mt-1 flex gap-4">
				<span><i class="bi bi-arrow-down text-emerald-500"></i> {fmtBytes(usage.rx_bytes)}</span>
				<span><i class="bi bi-arrow-up text-sky-500"></i> {fmtBytes(usage.tx_bytes)}</span>
			</div>

			{#if dataLimitMB > 0}
				<div class="mt-3 h-2 rounded-full bg-zinc-200 dark:bg-zinc-700 overflow-hidden">
					<div
						class="h-full rounded-full {overLimit ? 'bg-red-500' : usedFrac > 0.8 ? 'bg-amber-500' : 'bg-brand-500'}"
						style="width: {Math.round(usedFrac * 100)}%"
					></div>
				</div>
				{#if overLimit}
					<p class="mt-2 text-xs text-red-600 dark:text-red-400 flex items-center gap-1">
						<i class="bi bi-exclamation-triangle-fill"></i>{$_('modem.usage_over')}
					</p>
				{/if}
			{/if}

			{#if usage.signal.length > 1}
				<div class="mt-4">
					<div class="text-xs text-zinc-500 dark:text-zinc-400 mb-1">{$_('modem.signal_history')}</div>
					<div class="flex items-end gap-px h-10">
						{#each usage.signal.slice(-60) as s (s.at)}
							<div
								class="flex-1 min-w-px rounded-sm bg-brand-400/80 dark:bg-brand-500/60"
								style="height: {Math.max(4, s.percent)}%"
								title="{s.percent}%"
							></div>
						{/each}
					</div>
				</div>
			{/if}
		</section>
	{:else}
		<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('modem.usage_none')}</p>
	{/if}
	{/if}

	{#if activeTab === 'settings'}
	<!-- Settings -->
	<section class="surface p-5 mb-5 space-y-4">
		<label class="flex items-start gap-3 cursor-pointer">
			<input type="checkbox" class="rounded text-brand-600 mt-1" bind:checked={asWAN} />
			<span class="flex-1">
				<span class="font-medium">{$_('modem.as_wan')}</span>
				<span class="text-sm text-zinc-500 dark:text-zinc-400 block mt-0.5">{$_('modem.as_wan_help')}</span>
			</span>
		</label>

		<div>
			<label class="label" for="apn">
				{$_('modem.apn')}
				<span class="font-normal text-zinc-400 dark:text-zinc-500">({$_('modem.optional')})</span>
			</label>
			<input id="apn" class="input font-mono" bind:value={apn} placeholder="internet" />
			<p class="help">{$_('modem.apn_help')}</p>
		</div>

		{#if (status.sim_slots ?? 0) > 1}
			<div>
				<span class="label">{$_('modem.sim_slot')}</span>
				<div class="flex flex-wrap gap-2 mt-1">
					{#each Array(status.sim_slots) as slotEntry, i (i)}
						{@const slot = i + 1}
						<button
							type="button"
							onclick={() => (simSlot = slot)}
							class="px-4 py-2 rounded-md border text-sm
								{simSlot === slot
									? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
									: 'border-zinc-200 dark:border-zinc-700'}"
						>
							{$_('modem.sim_slot_n', { values: { n: slot } })}
							{#if status.primary_slot === slot}
								<span class="badge badge-ok text-[10px] ml-1">{$_('modem.sim_slot_active')}</span>
							{/if}
						</button>
					{/each}
				</div>
				<p class="help">{$_('modem.sim_slot_help')}</p>
			</div>
		{/if}

		<div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
			<div>
				<label class="label" for="pin">{$_('modem.pin')}</label>
				<input
					id="pin"
					class="input font-mono"
					type="password"
					bind:value={pin}
					oninput={() => (pinTouched = true)}
					placeholder={hasPin ? '••••' : $_('modem.pin_placeholder')}
				/>
				<p class="help">{$_('modem.pin_help')}</p>
			</div>
			<div>
				<label class="label" for="user">{$_('modem.username')}</label>
				<input id="user" class="input" bind:value={username} placeholder={$_('modem.optional')} />
				<p class="help">{$_('modem.username_help')}</p>
			</div>
		</div>

		<div class="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-2 border-t border-zinc-100 dark:border-zinc-800">
			<div>
				<label class="label" for="limit">{$_('modem.data_limit')}</label>
				<input
					id="limit"
					class="input font-mono"
					type="number"
					min="0"
					bind:value={dataLimitMB}
					placeholder="0"
				/>
				<p class="help">{$_('modem.data_limit_help')}</p>
			</div>
			<div>
				<label class="label" for="cycle">{$_('modem.cycle_reset_day')}</label>
				<input
					id="cycle"
					class="input font-mono"
					type="number"
					min="1"
					max="28"
					bind:value={cycleResetDay}
				/>
				<p class="help">{$_('modem.cycle_reset_day_help')}</p>
			</div>
		</div>
	</section>
	{/if}

	{#if activeTab === 'network'}
	<!-- Network selection: access tech + band lock -->
	{#if network && network.supported_modes.length > 0}
		<section class="surface p-5 mb-5 space-y-4" class:opacity-60={networkBusy}>
			<div>
				<span class="label">{$_('modem.network_mode')}</span>
				<div class="flex flex-wrap gap-2 mt-1">
					<button
						type="button"
						disabled={networkBusy}
						onclick={() => applyModes([])}
						class="px-4 py-2 rounded-md border text-sm
							{network.current_modes.length === network.supported_modes.length
							? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
							: 'border-zinc-200 dark:border-zinc-700'}"
					>
						{$_('modem.network_auto')}
					</button>
					{#each network.supported_modes as m (m)}
						<button
							type="button"
							disabled={networkBusy}
							onclick={() => applyModes([m])}
							class="px-4 py-2 rounded-md border text-sm uppercase
								{network.current_modes.length === 1 && network.current_modes[0] === m
								? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
								: 'border-zinc-200 dark:border-zinc-700'}"
						>
							{m}
						</button>
					{/each}
				</div>
				<p class="help">{$_('modem.network_mode_help')}</p>
			</div>

			{#if network.supported_bands.length > 0}
				<div>
					<span class="label">{$_('modem.bands')}</span>
					<div class="flex flex-wrap gap-1.5 mt-1">
						{#each network.supported_bands as band (band)}
							<button
								type="button"
								disabled={networkBusy}
								onclick={() => toggleBand(band)}
								class="px-2.5 py-1 rounded-md border text-xs font-mono
									{network.current_bands.includes(band)
									? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
									: 'border-zinc-200 dark:border-zinc-700 text-zinc-500'}"
							>
								{band}
							</button>
						{/each}
					</div>
					<p class="help">{$_('modem.bands_help')}</p>
				</div>
			{/if}
		</section>
	{:else}
		<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('modem.network_none')}</p>
	{/if}
	{/if}

	{#if activeTab === 'settings'}
	<!-- USSD (prepaid balance) -->
	{#if status.present}
		<section class="surface p-5 mb-5 space-y-3">
			<div>
				<span class="label">{$_('modem.ussd')}</span>
				<div class="flex gap-2">
					<input
						class="input font-mono flex-1"
						bind:value={ussdCode}
						placeholder="*100#"
						onkeydown={(e) => e.key === 'Enter' && runUSSD()}
					/>
					<button class="btn-ghost shrink-0" type="button" disabled={ussdBusy} onclick={runUSSD}>
						{#if ussdBusy}<span class="spinner"></span>{:else}<i class="bi bi-send"></i>{/if}
						{$_('modem.ussd_send')}
					</button>
				</div>
				<p class="help">{$_('modem.ussd_help')}</p>
			</div>
			{#if ussdResp}
				<div class="p-3 rounded-lg bg-zinc-50 dark:bg-zinc-800/60 border border-zinc-200 dark:border-zinc-700 text-sm whitespace-pre-wrap">
					{ussdResp}
				</div>
			{/if}
			<a href="/messages" class="text-sm text-brand-600 dark:text-brand-400 hover:underline inline-flex items-center gap-1">
				<i class="bi bi-chat-dots"></i>{$_('modem.open_messages')}
			</a>
		</section>
	{/if}
	{/if}

	{#if ctlError}
		<div class="flex items-start gap-2 p-3 mb-4 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
			<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i><span>{ctlError}</span>
		</div>
	{/if}

	{#if asWAN}
		<div class="surface border-amber-300 dark:border-amber-500/30 p-3 mb-4 text-xs flex items-start gap-2">
			<i class="bi bi-exclamation-triangle text-amber-500 mt-0.5"></i>
			<span>{$_('modem.switch_warning')}</span>
		</div>
	{/if}

	{#if error}
		<div class="flex items-start gap-2 p-3 mb-4 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
			<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i><span>{error}</span>
		</div>
	{/if}

	{#if showSave}
		<div class="flex items-center gap-2">
			<button class="btn-primary" type="button" disabled={saving} onclick={save}>
				{#if saving}<span class="spinner"></span>{$_('common.saving')}{:else}<i class="bi bi-check2"></i>{$_('modem.save')}{/if}
			</button>
			{#if savedFlash}
				<span class="text-sm text-emerald-600 dark:text-emerald-400 flex items-center gap-1 ml-1">
					<i class="bi bi-check-circle-fill"></i>{$_('common.saved')}
				</span>
			{/if}
		</div>
	{/if}
{/if}
