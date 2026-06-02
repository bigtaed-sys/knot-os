<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, ApiError, API_BASE } from '$lib/api';
	import { relativeTime, deviceIcon } from '$lib/format';
	import type {
		Device,
		DevicesResponse,
		AccessSettings,
		BandwidthStats,
		BandwidthResponse
	} from '$lib/types';
	import Sparkline from '$lib/components/Sparkline.svelte';

	let devices = $state<Device[]>([]);
	let bandwidth = $state<Record<string, BandwidthStats>>({});
	let loading = $state(true);
	let error = $state<string | null>(null);
	let timer: ReturnType<typeof setInterval> | null = null;
	let now = $state(new Date());

	async function refresh(initial = false) {
		if (initial) loading = true;
		try {
			const r = await apiGet<DevicesResponse>('/devices');
			// Sort: online first, then by recency.
			devices = r.devices.toSorted((a, b) => {
				if (a.online !== b.online) return a.online ? -1 : 1;
				return new Date(b.last_seen).getTime() - new Date(a.last_seen).getTime();
			});
			error = null;
		} catch (e) {
			// fall through to outer catch
			throw e;
		}
		// Bandwidth is best-effort: never fails the page if it 503s
		// (older backend without M32) or times out.
		try {
			const r = await apiGet<BandwidthResponse>('/bandwidth');
			const m: Record<string, BandwidthStats> = {};
			for (const s of r.devices ?? []) m[s.mac] = s;
			bandwidth = m;
		} catch {
			/* non-fatal */
		}
	}

	async function refreshOuter(initial = false) {
		try {
			await refresh(initial);
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

	let quarantine = $state(false);
	let blockLanding = $state(false);
	let quarBusy = $state(false);

	async function loadAccess() {
		try {
			const a = await apiGet<AccessSettings>('/devices/access');
			quarantine = a.quarantine_new_devices;
			blockLanding = a.block_landing_page;
		} catch {
			/* non-fatal */
		}
	}

	async function setAccess(patch: Partial<AccessSettings>) {
		quarBusy = true;
		try {
			const res = await fetch(`${API_BASE}/devices/access`, {
				method: 'PUT',
				credentials: 'same-origin',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify(patch)
			});
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			const a = (await res.json()) as AccessSettings;
			quarantine = a.quarantine_new_devices;
			blockLanding = a.block_landing_page;
			await refreshOuter();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			quarBusy = false;
		}
	}

	onMount(() => {
		refreshOuter(true);
		loadAccess();
		timer = setInterval(() => {
			now = new Date();
			refreshOuter();
		}, 5000);
	});
	onDestroy(() => {
		if (timer !== null) clearInterval(timer);
	});

	const onlineCount = $derived(devices.filter((d) => d.online).length);
	const pendingCount = $derived(quarantine ? devices.filter((d) => !d.approved).length : 0);
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('devices.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('devices.subtitle')}</p>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else if error}
	<div class="surface p-5 border-red-200 dark:border-red-800/50 bg-red-50 dark:bg-red-500/5">
		<div class="flex items-start gap-3">
			<i class="bi bi-exclamation-triangle text-red-600 dark:text-red-400 text-xl"></i>
			<div class="font-mono text-sm text-red-700 dark:text-red-300">{error}</div>
		</div>
	</div>
{:else if devices.length === 0}
	<div class="surface p-10 text-center">
		<div class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4">
			<i class="bi bi-hdd-network text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<h2 class="font-medium text-lg mb-2">{$_('devices.empty_title')}</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
			{$_('devices.empty_help')}
		</p>
	</div>
{:else}
	<!-- Summary -->
	<div class="surface-muted px-4 py-3 mb-4 flex items-center gap-3 text-sm">
		<i class="bi bi-broadcast text-brand-500"></i>
		<span>
			<span class="font-semibold text-zinc-900 dark:text-zinc-100">{onlineCount}</span>
			<span class="text-zinc-500 dark:text-zinc-400"> / {devices.length}</span>
			<span class="ml-1 text-zinc-500 dark:text-zinc-400">{$_('devices.online')}</span>
		</span>
		<div class="ml-auto flex items-center gap-2 flex-wrap justify-end">
			{#if pendingCount > 0}
				<span class="badge badge-warn">{$_('devices.pending_count', { values: { n: pendingCount } })}</span>
			{/if}
			<button
				class={blockLanding ? 'btn-primary' : 'btn-ghost'}
				disabled={quarBusy}
				onclick={() => setAccess({ block_landing_page: !blockLanding })}
				title={$_('devices.landing_help')}
			>
				<i class="bi {blockLanding ? 'bi-signpost-2-fill' : 'bi-signpost-2'}"></i>
				{$_('devices.landing')}
			</button>
			<button
				class={quarantine ? 'btn-primary' : 'btn-ghost'}
				disabled={quarBusy}
				onclick={() => setAccess({ quarantine_new_devices: !quarantine })}
				title={$_('devices.quarantine_help')}
			>
				<i class="bi {quarantine ? 'bi-shield-fill-check' : 'bi-shield'}"></i>
				{$_('devices.quarantine')}
			</button>
		</div>
	</div>

	<div class="space-y-2">
		{#each devices as d (d.mac)}
			<a
				href={`/devices/${encodeURIComponent(d.mac)}`}
				class="surface p-4 flex items-center gap-4 hover:border-brand-300 dark:hover:border-brand-700 transition-colors"
			>
				<!-- Icon -->
				<div
					class="
						w-12 h-12 shrink-0 rounded-xl flex items-center justify-center text-xl
						{d.online
							? 'bg-emerald-100 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
							: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-400 dark:text-zinc-500'}
					"
				>
					<i class="bi {deviceIcon(d)}"></i>
				</div>

				<!-- Name + meta -->
				<div class="flex-1 min-w-0">
					<div class="flex items-center gap-2 flex-wrap">
						<span class="font-semibold truncate">{d.label}</span>
						{#if d.online}
							<span class="badge badge-ok">
								<span class="dot-live"></span>
								{$_('devices.online')}
							</span>
						{:else}
							<span class="badge badge-neutral">
								{$_('devices.last_seen', { values: { ago: relativeTime(d.last_seen, now) } })}
							</span>
						{/if}
						{#if d.stale}
							<span class="badge badge-warn" title={$_('devices.stale_help')}>
								<i class="bi bi-clock-history"></i>
								{$_('devices.stale')}
							</span>
						{/if}
						{#if d.paused}
							<span class="badge bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-400">
								<i class="bi bi-pause-circle-fill"></i>
								{$_('devices.paused')}
							</span>
						{/if}
						{#if quarantine && !d.approved}
							<span class="badge badge-warn">
								<i class="bi bi-shield-exclamation"></i>
								{$_('devices.pending_approval')}
							</span>
						{/if}
					</div>
					<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono mt-0.5 flex items-center gap-3 flex-wrap">
						<span>{d.mac}</span>
						{#if d.ip}
							<span>· {d.ip}</span>
						{/if}
						{#if d.profile_id}
							<span class="badge badge-info text-[10px]">
								<i class="bi bi-shield-check"></i>
								{d.profile_id}
							</span>
						{/if}
					</div>
				</div>

				<!-- Bandwidth sparkline (M32). Only when we have at least one
					 sample for this device — quiet on first render, then
					 fades in as samples arrive. -->
				{#if bandwidth[d.mac]?.sparkline?.length}
					<div class="hidden sm:block shrink-0">
						<Sparkline
							values={bandwidth[d.mac].sparkline.slice(-60)}
							width={84}
							height={24}
							showLabels
						/>
					</div>
				{/if}

				<i class="bi bi-chevron-right text-zinc-400"></i>
			</a>
		{/each}
	</div>
{/if}
