<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, ApiError } from '$lib/api';
	import type { SystemStatus } from '$lib/types';

	let status = $state<SystemStatus | null>(null);
	let error = $state<string | null>(null);
	let timer: ReturnType<typeof setInterval> | null = null;

	async function refresh() {
		try {
			const [s] = await Promise.all([apiGet<SystemStatus>('/status'), apiGet('/auth/me')]);
			status = s;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			error = e instanceof ApiError ? e.message : String(e);
		}
	}

	onMount(() => {
		refresh();
		timer = setInterval(refresh, 5000);
	});
	onDestroy(() => {
		if (timer !== null) clearInterval(timer);
	});

	function rssiBars(dbm: number | undefined): number {
		if (!dbm) return 0;
		if (dbm >= -50) return 4;
		if (dbm >= -65) return 3;
		if (dbm >= -75) return 2;
		if (dbm >= -85) return 1;
		return 0;
	}

	function rssiColor(bars: number): string {
		if (bars >= 3) return 'text-emerald-500';
		if (bars >= 2) return 'text-amber-500';
		return 'text-red-500';
	}

	const role = $derived(status?.role ?? 'setup');
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('dashboard.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">
		{role === 'wifi-extender' ? $_('dashboard.subtitle_extender') : $_('dashboard.subtitle_setup')}
	</p>
</header>

{#if error}
	<div class="surface p-5 border-red-200 dark:border-red-800/50 bg-red-50 dark:bg-red-500/5">
		<div class="flex items-start gap-3">
			<i class="bi bi-exclamation-triangle text-red-600 dark:text-red-400 text-xl"></i>
			<div>
				<div class="font-medium text-red-900 dark:text-red-200">{$_('dashboard.error')}</div>
				<div class="text-sm font-mono text-red-700 dark:text-red-300 mt-1">{error}</div>
			</div>
		</div>
	</div>
{:else if status === null}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else}
	<!-- Hero card: device + role -->
	<section class="surface p-5 mb-5 bg-gradient-to-br from-white to-brand-50/30 dark:from-zinc-900 dark:to-brand-500/5">
		<div class="flex items-start justify-between gap-4">
			<div class="flex items-start gap-4 min-w-0">
				<div class="w-12 h-12 shrink-0 rounded-xl bg-brand-100 dark:bg-brand-500/20 flex items-center justify-center text-brand-700 dark:text-brand-300">
					<i class="bi bi-router-fill text-2xl"></i>
				</div>
				<div class="min-w-0">
					<div class="text-xs uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
						{$_('dashboard.device')}
					</div>
					<div class="text-xl font-semibold truncate">{status.device}</div>
					<div class="flex items-center gap-2 mt-1.5 text-sm">
						<span class="badge badge-info">
							{role === 'wifi-extender' ? $_('dashboard.role_extender') : $_('dashboard.role_setup')}
						</span>
						<span class="text-zinc-500 dark:text-zinc-400">
							v{status.version}
						</span>
						{#if status.network.backend === 'mock'}
							<span class="badge badge-warn">
								<i class="bi bi-bug"></i>
								{$_('dashboard.backend_dev')}
							</span>
						{/if}
					</div>
				</div>
			</div>
		</div>
	</section>

	<!-- Two-column status grid -->
	<div class="grid grid-cols-1 md:grid-cols-2 gap-5">
		<!-- Uplink card -->
		{#if status.network.uplink}
			{@const bars = rssiBars(status.network.uplink.rssi_dbm)}
			<section class="surface p-5">
				<header class="flex items-center justify-between mb-4">
					<div class="flex items-center gap-2.5">
						<i class="bi bi-cloud-arrow-up text-brand-500 text-lg"></i>
						<h2 class="font-semibold">{$_('dashboard.uplink')}</h2>
					</div>
					{#if status.network.uplink.connected}
						<span class="badge badge-ok">
							<span class="dot-live"></span>
							{$_('dashboard.connected')}
						</span>
					{:else}
						<span class="badge badge-bad">{$_('dashboard.disconnected')}</span>
					{/if}
				</header>
				<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">
					{$_('dashboard.uplink_subtitle')}
				</p>
				<dl class="space-y-2 text-sm">
					<div class="flex items-center justify-between">
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('dashboard.ssid')}</dt>
						<dd class="font-medium truncate ml-3">{status.network.uplink.ssid}</dd>
					</div>
					{#if status.network.uplink.rssi_dbm}
						<div class="flex items-center justify-between">
							<dt class="text-zinc-500 dark:text-zinc-400">{$_('dashboard.signal')}</dt>
							<dd class="flex items-center gap-2 font-medium">
								<span class="font-mono text-xs">{status.network.uplink.rssi_dbm} dBm</span>
								<span class={'flex items-end gap-0.5 ' + rssiColor(bars)}>
									{#each [1, 2, 3, 4] as b}
										<span
											class="w-1 rounded-sm bg-current"
											class:opacity-25={b > bars}
											style="height: {3 + b * 2}px"
										></span>
									{/each}
								</span>
							</dd>
						</div>
					{/if}
				</dl>
			</section>
		{/if}

		<!-- AP card -->
		{#if status.network.ap}
			<section class="surface p-5">
				<header class="flex items-center justify-between mb-4">
					<div class="flex items-center gap-2.5">
						<i class="bi bi-broadcast text-brand-500 text-lg"></i>
						<h2 class="font-semibold">{$_('dashboard.ap')}</h2>
					</div>
					{#if status.network.ap.up}
						<span class="badge badge-ok">
							<span class="dot-live"></span>
							{$_('dashboard.up')}
						</span>
					{:else}
						<span class="badge badge-bad">{$_('dashboard.down')}</span>
					{/if}
				</header>
				<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">
					{$_('dashboard.ap_subtitle')}
				</p>
				<dl class="space-y-2 text-sm">
					<div class="flex items-center justify-between">
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('dashboard.ssid')}</dt>
						<dd class="font-medium truncate ml-3">{status.network.ap.ssid}</dd>
					</div>
					<div class="flex items-center justify-between">
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('dashboard.clients')}</dt>
						<dd class="font-medium">
							{$_('dashboard.clients_count', { values: { count: status.network.ap.clients } })}
						</dd>
					</div>
				</dl>
			</section>
		{/if}
	</div>
{/if}
