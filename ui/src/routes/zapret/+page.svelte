<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, apiPost, ApiError } from '$lib/api';
	import type {
		ZapretResponse,
		ZapretPreset,
		ZapretAutoTuneResponse,
		ZapretTuneResult
	} from '$lib/types';

	let enabled = $state(false);
	let strategy = $state('general');
	let customArgs = $state('');
	let presets = $state<ZapretPreset[]>([]);
	let status = $state({ running: false, binary_present: false, router_mode: true });

	let loading = $state(true);
	let saving = $state(false);
	let refreshing = $state(false);
	let error = $state<string | null>(null);
	let savedFlash = $state(false);
	let refreshMsg = $state<string | null>(null);
	let useCustom = $state(false);

	let tuning = $state(false);
	let tuneResults = $state<ZapretTuneResult[]>([]);
	let tuneWinner = $state<string | null>(null);

	async function refresh() {
		loading = true;
		try {
			const r = await apiGet<ZapretResponse>('/zapret');
			enabled = r.enabled;
			strategy = r.strategy || 'general';
			customArgs = r.custom_args || '';
			useCustom = customArgs.trim().length > 0;
			presets = r.presets ?? [];
			status = r.status;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function save() {
		saving = true;
		error = null;
		try {
			await apiPut(
				'/zapret',
				{
					enabled,
					strategy,
					custom_args: useCustom ? customArgs : ''
				},
				{ timeoutMs: 60000 }
			);
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 2000);
			await refresh();
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			saving = false;
		}
	}

	async function refreshLists() {
		refreshing = true;
		refreshMsg = null;
		error = null;
		try {
			const r = await apiPost<{ updated: number; lists: number; strategies: number }>(
				'/zapret/refresh',
				{},
				{ timeoutMs: 60000 }
			);
			refreshMsg = $_('zapret.refresh_done', {
				values: { lists: r.lists, strategies: r.strategies }
			});
			setTimeout(() => (refreshMsg = null), 3000);
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			refreshing = false;
		}
	}

	async function autoTune() {
		tuning = true;
		error = null;
		tuneResults = [];
		tuneWinner = null;
		try {
			const r = await apiPost<ZapretAutoTuneResponse>(
				'/zapret/autotune',
				{},
				{ timeoutMs: 180000 }
			);
			tuneResults = r.results ?? [];
			tuneWinner = r.winner;
			// The server already enabled + saved the winner; reflect it.
			await refresh();
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			tuning = false;
		}
	}

	onMount(refresh);
</script>

<svelte:head>
	<title>{$_('zapret.title')} · KnotOS</title>
</svelte:head>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('zapret.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('zapret.subtitle')}</p>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else}
	{#if !status.router_mode}
		<div class="surface border-amber-300 dark:border-amber-500/30 p-4 mb-5 text-sm flex items-start gap-3">
			<i class="bi bi-info-circle text-amber-500 text-lg mt-0.5"></i>
			<span>{$_('zapret.not_router')}</span>
		</div>
	{/if}

	<!-- Enable + live status -->
	<section class="surface p-5 mb-5">
		<label class="flex items-start gap-3 cursor-pointer">
			<input type="checkbox" class="rounded text-brand-600 mt-1" bind:checked={enabled} />
			<span class="flex-1">
				<span class="font-medium flex items-center gap-2">
					{$_('zapret.enable')}
					{#if enabled && status.running}
						<span class="badge badge-ok"><i class="bi bi-broadcast"></i> {$_('zapret.running')}</span>
					{:else if enabled && !status.running}
						<span class="badge badge-warn"><i class="bi bi-pause-circle"></i> {$_('zapret.not_running')}</span>
					{/if}
				</span>
				<span class="text-sm text-zinc-500 dark:text-zinc-400 block mt-0.5">{$_('zapret.enable_help')}</span>
			</span>
		</label>

		{#if enabled && !status.binary_present}
			<div class="mt-3 text-xs text-zinc-500 dark:text-zinc-400 flex items-start gap-2 pl-7">
				<i class="bi bi-cloud-arrow-down mt-0.5"></i>
				<span>{$_('zapret.will_download')}</span>
			</div>
		{/if}
	</section>

	<!-- Strategy -->
	<section class="surface p-5 mb-5 space-y-4" class:opacity-60={!enabled}>
		<div>
			<div class="flex items-center justify-between gap-3 flex-wrap">
				<div class="label !mb-0">{$_('zapret.strategy')}</div>
				<button
					class="btn-ghost text-sm shrink-0"
					type="button"
					disabled={!enabled || tuning}
					onclick={autoTune}
					title={$_('zapret.autotune_help')}
				>
					{#if tuning}
						<span class="spinner"></span>{$_('zapret.autotuning')}
					{:else}
						<i class="bi bi-magic"></i>{$_('zapret.autotune')}
					{/if}
				</button>
			</div>
			<p class="help mb-3">{$_('zapret.strategy_help')}</p>

			{#if tuning}
				<div class="mb-3 text-xs text-zinc-500 dark:text-zinc-400 flex items-start gap-2">
					<i class="bi bi-hourglass-split mt-0.5"></i>
					<span>{$_('zapret.autotune_running')}</span>
				</div>
			{/if}

			{#if tuneResults.length > 0}
				<div class="mb-3 rounded-lg border border-zinc-200 dark:border-zinc-800 overflow-hidden">
					{#each tuneResults as r (r.strategy)}
						<div
							class="flex items-center gap-3 px-3 py-2 text-sm border-b last:border-0 border-zinc-100 dark:border-zinc-800
							{r.strategy === tuneWinner ? 'bg-emerald-50 dark:bg-emerald-500/10' : ''}"
						>
							<span class="flex-1 min-w-0 truncate">
								{#if r.strategy === tuneWinner}
									<i class="bi bi-trophy-fill text-emerald-500 mr-1"></i>
								{/if}
								{r.name}
							</span>
							<span class="text-xs font-mono tabular-nums {r.ok === r.total ? 'text-emerald-600 dark:text-emerald-400' : r.ok === 0 ? 'text-red-500' : 'text-amber-600 dark:text-amber-400'}">
								{r.ok}/{r.total}
							</span>
						</div>
					{/each}
				</div>
			{/if}

			<div class="space-y-1.5">
				{#each presets as p (p.id)}
					<label
						class="flex items-start gap-3 p-2.5 rounded-lg border cursor-pointer transition
						{!useCustom && strategy === p.id
							? 'border-brand-400 bg-brand-50 dark:bg-brand-500/10'
							: 'border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-800/50'}"
					>
						<input
							type="radio"
							class="text-brand-600 mt-0.5"
							name="strategy"
							value={p.id}
							checked={!useCustom && strategy === p.id}
							disabled={!enabled}
							onchange={() => {
								strategy = p.id;
								useCustom = false;
							}}
						/>
						<span class="min-w-0">
							<span class="font-medium block">{p.name}</span>
							<span class="text-xs text-zinc-500 dark:text-zinc-400 block">{p.desc}</span>
						</span>
					</label>
				{/each}

				<!-- Custom strategy -->
				<label
					class="flex items-start gap-3 p-2.5 rounded-lg border cursor-pointer transition
					{useCustom
						? 'border-brand-400 bg-brand-50 dark:bg-brand-500/10'
						: 'border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-800/50'}"
				>
					<input
						type="radio"
						class="text-brand-600 mt-0.5"
						name="strategy"
						checked={useCustom}
						disabled={!enabled}
						onchange={() => (useCustom = true)}
					/>
					<span class="min-w-0 flex-1">
						<span class="font-medium block">{$_('zapret.custom')}</span>
						<span class="text-xs text-zinc-500 dark:text-zinc-400 block">{$_('zapret.custom_help')}</span>
					</span>
				</label>
			</div>
		</div>

		{#if useCustom}
			<div>
				<textarea
					bind:value={customArgs}
					disabled={!enabled}
					placeholder={'--filter-tcp=443 --hostlist={LISTS}/list-general.txt --dpi-desync=fake,multisplit ...'}
					class="input font-mono text-xs h-32"
				></textarea>
				<p class="help">{$_('zapret.custom_placeholder_help')}</p>
			</div>
		{/if}
	</section>

	<!-- Lists -->
	<section class="surface p-5 mb-5">
		<div class="flex items-center justify-between gap-3 flex-wrap">
			<div class="min-w-0">
				<div class="font-medium">{$_('zapret.lists')}</div>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-0.5">{$_('zapret.lists_help')}</p>
			</div>
			<button class="btn-ghost text-sm shrink-0" type="button" disabled={refreshing} onclick={refreshLists}>
				{#if refreshing}
					<span class="spinner"></span>{$_('zapret.refreshing')}
				{:else}
					<i class="bi bi-arrow-clockwise"></i>{$_('zapret.refresh')}
				{/if}
			</button>
		</div>
		{#if refreshMsg}
			<p class="text-sm text-emerald-600 dark:text-emerald-400 mt-2 flex items-center gap-1">
				<i class="bi bi-check-circle-fill"></i>{refreshMsg}
			</p>
		{/if}
	</section>

	{#if error}
		<div class="flex items-start gap-2 p-3 mb-4 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
			<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
			<span>{error}</span>
		</div>
	{/if}

	<div class="flex items-center gap-2">
		<button class="btn-primary" type="button" disabled={saving} onclick={save}>
			{#if saving}
				<span class="spinner"></span>{$_('common.saving')}
			{:else}
				<i class="bi bi-check2"></i>{$_('zapret.save')}
			{/if}
		</button>
		{#if savedFlash}
			<span class="text-sm text-emerald-600 dark:text-emerald-400 flex items-center gap-1 ml-1">
				<i class="bi bi-check-circle-fill"></i>{$_('common.saved')}
			</span>
		{/if}
	</div>

	<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-4">
		<i class="bi bi-info-circle mr-1"></i>{$_('zapret.tip')}
	</p>
{/if}
