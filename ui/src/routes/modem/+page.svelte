<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError } from '$lib/api';
	import type { ModemResponse, ModemStatus } from '$lib/types';

	let status = $state<ModemStatus>({ present: false, signal_percent: 0 });
	let asWAN = $state(false);
	let apn = $state('');
	let username = $state('');
	let hasPin = $state(false);
	let pin = $state('');
	let pinTouched = $state(false);
	let simSlot = $state(0);
	let routerMode = $state(true);

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
			if (initial) {
				asWAN = r.as_wan;
				apn = r.apn;
				username = r.username;
				hasPin = r.has_pin;
				simSlot = r.sim_slot;
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
			const body: Record<string, unknown> = { as_wan: asWAN, apn, username };
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
	</section>

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
