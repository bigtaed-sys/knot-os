<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, ApiError } from '$lib/api';
	import { relativeTime } from '$lib/format';
	import type {
		DNSStats,
		DNSQuery,
		DNSQueriesResponse,
		Device,
		DevicesResponse
	} from '$lib/types';

	let stats = $state<DNSStats | null>(null);
	let queries = $state<DNSQuery[]>([]);
	let devicesByMAC = $state<Record<string, Device>>({});
	let loading = $state(true);
	let error = $state<string | null>(null);
	let refreshing = $state(false);
	let refreshFlash = $state(false);
	let timer: ReturnType<typeof setInterval> | null = null;

	let queryFilter = $state<'all' | 'blocked'>('all');
	let now = $state(new Date());

	async function loadStats() {
		stats = await apiGet<DNSStats>('/dns/stats');
	}
	async function loadQueries() {
		const r = await apiGet<DNSQueriesResponse>('/dns/queries?limit=100');
		queries = r.queries;
	}
	async function loadDevices() {
		try {
			const r = await apiGet<DevicesResponse>('/devices');
			const map: Record<string, Device> = {};
			for (const d of r.devices) map[d.mac.toLowerCase()] = d;
			devicesByMAC = map;
		} catch {
			// non-fatal — we just won't pretty-print MACs
		}
	}

	async function refresh(initial = false) {
		if (initial) loading = true;
		try {
			await Promise.all([loadStats(), loadQueries(), loadDevices()]);
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			if (e instanceof ApiError && e.status === 503) {
				error = 'disabled';
				return;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			if (initial) loading = false;
		}
	}

	async function manualRefresh() {
		refreshing = true;
		try {
			await apiPost('/dns/refresh');
			refreshFlash = true;
			setTimeout(() => (refreshFlash = false), 2500);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			refreshing = false;
		}
	}

	onMount(() => {
		refresh(true);
		timer = setInterval(() => {
			now = new Date();
			refresh();
		}, 5000);
	});
	onDestroy(() => {
		if (timer !== null) clearInterval(timer);
	});

	const filteredQueries = $derived(
		queryFilter === 'blocked' ? queries.filter((q) => q.blocked) : queries
	);

	const blockedPct = $derived(stats ? Math.round(stats.blocked_ratio * 1000) / 10 : 0);

	const sourceList = $derived(
		stats?.sources ? Object.entries(stats.sources) : []
	);
	const blocklistEntries = $derived(
		stats?.blocklists ? Object.entries(stats.blocklists) : []
	);

	function deviceLabel(q: DNSQuery): string {
		if (q.src_mac) {
			const d = devicesByMAC[q.src_mac.toLowerCase()];
			if (d) return d.label;
			return q.src_mac;
		}
		return q.src_ip;
	}

	function fmtTime(iso: string): string {
		try {
			return new Date(iso).toLocaleTimeString();
		} catch {
			return iso;
		}
	}
</script>

<header class="mb-6 flex items-start justify-between gap-3 flex-wrap">
	<div>
		<h1 class="text-2xl font-semibold">{$_('protection.title')}</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('protection.subtitle')}</p>
	</div>
	<button
		type="button"
		class="btn-ghost"
		onclick={manualRefresh}
		disabled={refreshing || error === 'disabled'}
	>
		{#if refreshing}
			<span class="spinner"></span>
		{:else}
			<i class="bi bi-arrow-clockwise"></i>
		{/if}
		{$_('protection.refresh_now')}
	</button>
</header>

{#if refreshFlash}
	<div
		class="mb-4 p-3 rounded-lg bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 text-sm flex items-center gap-2"
	>
		<i class="bi bi-check-circle-fill"></i>
		{$_('protection.refresh_queued')}
	</div>
{/if}

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else if error === 'disabled'}
	<div class="surface p-10 text-center">
		<div class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4">
			<i class="bi bi-shield-slash text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<h2 class="font-medium text-lg mb-2">{$_('protection.disabled_title')}</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
			{$_('protection.disabled_help')}
		</p>
	</div>
{:else if error}
	<div class="surface p-5 border-red-200 dark:border-red-800/50 bg-red-50 dark:bg-red-500/5">
		<div class="flex items-start gap-3">
			<i class="bi bi-exclamation-triangle text-red-600 dark:text-red-400 text-xl"></i>
			<div class="font-mono text-sm text-red-700 dark:text-red-300">{error}</div>
		</div>
	</div>
{:else if stats}
	<!-- Stat tiles -->
	<div class="grid grid-cols-2 md:grid-cols-3 gap-3 mb-5">
		<div class="surface p-4">
			<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
				{$_('protection.queries_total')}
			</div>
			<div class="text-2xl font-semibold mt-1 tabular-nums">{stats.queries.toLocaleString()}</div>
		</div>
		<div class="surface p-4">
			<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
				{$_('protection.queries_blocked')}
			</div>
			<div class="text-2xl font-semibold mt-1 tabular-nums text-rose-600 dark:text-rose-400">
				{stats.blocked.toLocaleString()}
			</div>
		</div>
		<div class="surface p-4">
			<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
				{$_('protection.blocked_pct')}
			</div>
			<div class="text-2xl font-semibold mt-1 tabular-nums">{blockedPct}%</div>
			<!-- Inline meter -->
			<div class="h-1.5 rounded-full bg-zinc-100 dark:bg-zinc-800 mt-2 overflow-hidden">
				<div
					class="h-full bg-gradient-to-r from-rose-500 to-amber-500 transition-all"
					style="width: {Math.min(blockedPct, 100)}%"
				></div>
			</div>
		</div>
	</div>

	<!-- Top blocked + sources -->
	<div class="grid grid-cols-1 lg:grid-cols-2 gap-4 mb-5">
		<section class="surface p-5">
			<h2 class="font-semibold mb-4 flex items-center gap-2">
				<i class="bi bi-bar-chart-fill text-brand-500"></i>
				{$_('protection.top_blocked')}
			</h2>
			{#if stats.top_blocked.length === 0}
				<p class="text-sm text-zinc-500 dark:text-zinc-400">
					{$_('protection.top_blocked_empty')}
				</p>
			{:else}
				{@const max = stats.top_blocked[0]?.count || 1}
				<ol class="space-y-2">
					{#each stats.top_blocked as t, i (t.name)}
						<li class="text-sm">
							<div class="flex items-center justify-between gap-2 mb-0.5">
								<span class="font-mono truncate text-zinc-700 dark:text-zinc-200">
									<span class="text-zinc-400 mr-1.5">{i + 1}.</span>{t.name}
								</span>
								<span class="tabular-nums text-zinc-500 dark:text-zinc-400">{t.count}</span>
							</div>
							<div class="h-1.5 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
								<div
									class="h-full bg-rose-400 dark:bg-rose-500"
									style="width: {(t.count / max) * 100}%"
								></div>
							</div>
						</li>
					{/each}
				</ol>
			{/if}
		</section>

		<section class="surface p-5">
			<h2 class="font-semibold mb-4 flex items-center gap-2">
				<i class="bi bi-list-ul text-brand-500"></i>
				{$_('protection.sources')}
			</h2>
			{#if blocklistEntries.length === 0}
				<p class="text-sm text-zinc-500 dark:text-zinc-400">
					{$_('protection.sources_empty')}
				</p>
			{:else}
				<ul class="space-y-3">
					{#each blocklistEntries as [name, size] (name)}
						{@const s = stats.sources?.[name]}
						<li>
							<div class="flex items-center justify-between gap-2">
								<span class="font-medium">{name}</span>
								<span class="text-xs text-zinc-500 dark:text-zinc-400 tabular-nums">
									{size.toLocaleString()} {$_('protection.entries')}
								</span>
							</div>
							{#if s}
								<div class="text-xs text-zinc-500 dark:text-zinc-400 mt-1 flex flex-wrap gap-x-3">
									{#if s.last_success}
										<span>
											<i class="bi bi-cloud-check"></i>
											{$_('protection.last_updated', {
												values: { ago: relativeTime(s.last_success, now) }
											})}
										</span>
									{/if}
									{#if s.last_error}
										<span class="text-rose-500 dark:text-rose-400">
											<i class="bi bi-exclamation-triangle"></i>
											{s.last_error}
										</span>
									{/if}
								</div>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	</div>

	<!-- Queries -->
	<section class="surface p-5">
		<div class="flex items-center justify-between mb-4 gap-2 flex-wrap">
			<h2 class="font-semibold flex items-center gap-2">
				<i class="bi bi-clock-history text-brand-500"></i>
				{$_('protection.recent_queries')}
			</h2>
			<div class="inline-flex rounded-lg border border-zinc-200 dark:border-zinc-700 p-0.5 text-sm">
				<button
					type="button"
					class="px-3 py-1 rounded-md transition-colors {queryFilter === 'all'
						? 'bg-brand-500 text-white'
						: 'text-zinc-600 dark:text-zinc-300'}"
					onclick={() => (queryFilter = 'all')}
				>
					{$_('protection.filter_all')}
				</button>
				<button
					type="button"
					class="px-3 py-1 rounded-md transition-colors {queryFilter === 'blocked'
						? 'bg-rose-500 text-white'
						: 'text-zinc-600 dark:text-zinc-300'}"
					onclick={() => (queryFilter = 'blocked')}
				>
					{$_('protection.filter_blocked')}
				</button>
			</div>
		</div>

		{#if filteredQueries.length === 0}
			<p class="text-sm text-zinc-500 dark:text-zinc-400 py-4 text-center">
				{$_('protection.queries_empty')}
			</p>
		{:else}
			<div class="overflow-x-auto -mx-5">
				<table class="w-full text-sm">
					<thead>
						<tr class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
							<th class="px-5 py-2 text-left font-medium">{$_('protection.col_time')}</th>
							<th class="px-3 py-2 text-left font-medium">{$_('protection.col_device')}</th>
							<th class="px-3 py-2 text-left font-medium">{$_('protection.col_qname')}</th>
							<th class="px-3 py-2 text-left font-medium">{$_('protection.col_type')}</th>
							<th class="px-5 py-2 text-left font-medium">{$_('protection.col_result')}</th>
						</tr>
					</thead>
					<tbody>
						{#each filteredQueries as q (q.when + q.qname + q.src_ip)}
							<tr class="border-t border-zinc-100 dark:border-zinc-800">
								<td class="px-5 py-1.5 tabular-nums text-zinc-500 dark:text-zinc-400">
									{fmtTime(q.when)}
								</td>
								<td class="px-3 py-1.5 truncate max-w-[14ch]">{deviceLabel(q)}</td>
								<td class="px-3 py-1.5 font-mono truncate max-w-[28ch]">{q.qname}</td>
								<td class="px-3 py-1.5 text-zinc-500 dark:text-zinc-400">{q.qtype}</td>
								<td class="px-5 py-1.5">
									{#if q.blocked}
										<span class="badge" style="background-color: rgb(254 226 226); color: rgb(159 18 57);">
											<i class="bi bi-shield-slash"></i>
											{$_('protection.blocked')}
											{#if q.blocked_by}
												<span class="opacity-70 ml-1">({q.blocked_by})</span>
											{/if}
										</span>
									{:else}
										<span class="badge badge-ok">
											<i class="bi bi-check2"></i>
											{$_('protection.allowed')}
										</span>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
{/if}
