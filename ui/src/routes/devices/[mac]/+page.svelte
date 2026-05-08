<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, ApiError, API_BASE } from '$lib/api';
	import { relativeTime, deviceIcon } from '$lib/format';
	import type { Device, Profile, ProfilesResponse, BandwidthStats } from '$lib/types';
	import Sparkline from '$lib/components/Sparkline.svelte';

	// SvelteKit types $page.params as Record<string, string | undefined>;
	// fall back to '' so encodeURIComponent never sees undefined.
	const mac = $derived($page.params.mac ?? '');

	let device = $state<Device | null>(null);
	let profiles = $state<Profile[]>([]);
	let bandwidth = $state<BandwidthStats | null>(null);
	let bwTimer: ReturnType<typeof setInterval> | null = null;
	let loading = $state(true);
	let notFound = $state(false);
	let error = $state<string | null>(null);

	let displayName = $state('');
	let selectedProfile = $state('');
	let saving = $state(false);
	let savingProfile = $state(false);
	let savedFlash = $state(false);
	let deleting = $state(false);

	async function loadDevice() {
		try {
			device = await apiGet<Device>(`/devices/${encodeURIComponent(mac)}`);
			displayName = device.display_name ?? '';
			selectedProfile = device.profile_id ?? '';
			notFound = false;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			if (e instanceof ApiError && e.status === 404) {
				notFound = true;
				return;
			}
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function loadProfiles() {
		try {
			const r = await apiGet<ProfilesResponse>('/profiles');
			profiles = r.profiles;
		} catch {
			// non-fatal
		}
	}

	async function loadBandwidth() {
		try {
			bandwidth = await apiGet<BandwidthStats>(`/bandwidth/${encodeURIComponent(mac)}`);
		} catch {
			// non-fatal — fresh device might not have samples yet
		}
	}

	async function load() {
		loading = true;
		await Promise.all([loadDevice(), loadProfiles(), loadBandwidth()]);
		loading = false;
	}

	async function setProfile(id: string) {
		savingProfile = true;
		try {
			const res = await fetch(`${API_BASE}/devices/${encodeURIComponent(mac)}`, {
				method: 'PATCH',
				credentials: 'same-origin',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ profile_id: id })
			});
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			device = await res.json();
			selectedProfile = device?.profile_id ?? '';
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			savingProfile = false;
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			const res = await fetch(`${API_BASE}/devices/${encodeURIComponent(mac)}`, {
				method: 'PATCH',
				credentials: 'same-origin',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ display_name: displayName })
			});
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			device = await res.json();
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 2000);
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			saving = false;
		}
	}

	function reset() {
		displayName = device?.hostname ?? '';
	}

	let waking = $state(false);
	let wakeMsg = $state<string | null>(null);
	async function wakeDevice() {
		if (!device || waking) return;
		waking = true;
		wakeMsg = null;
		try {
			const res = await fetch(`${API_BASE}/devices/${encodeURIComponent(mac)}/wake`, {
				method: 'POST',
				credentials: 'same-origin'
			});
			if (!res.ok) {
				const body = (await res.json().catch(() => null)) as
					| { error?: { message?: string } }
					| null;
				throw new Error(body?.error?.message ?? `HTTP ${res.status}`);
			}
			wakeMsg = $_('devices.wake_sent');
			setTimeout(() => (wakeMsg = null), 4000);
		} catch (err) {
			wakeMsg = err instanceof Error ? err.message : String(err);
		} finally {
			waking = false;
		}
	}

	async function forgetDevice() {
		if (!device) return;
		const label = device.label;
		if (!confirm($_('devices.forget_confirm', { values: { name: label } }))) return;
		deleting = true;
		try {
			const res = await fetch(`${API_BASE}/devices/${encodeURIComponent(mac)}`, {
				method: 'DELETE',
				credentials: 'same-origin'
			});
			if (res.status === 409) {
				const body = (await res.json()) as { error?: { message?: string } };
				error = body.error?.message ?? $_('devices.forget_online_error');
				return;
			}
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			goto('/devices');
		} catch (err) {
			error = err instanceof Error ? err.message : String(err);
		} finally {
			deleting = false;
		}
	}

	onMount(() => {
		load();
		// Refresh bandwidth every 5s for a live graph.
		bwTimer = setInterval(loadBandwidth, 5000);
	});
	onDestroy(() => {
		if (bwTimer !== null) clearInterval(bwTimer);
	});

	function fmtBytes(b: number): string {
		if (b < 1024) return b + ' B';
		if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
		if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB';
		return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB';
	}
</script>

<a href="/devices" class="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-brand-600 dark:hover:text-brand-400 mb-4">
	{$_('devices.back_to_list')}
</a>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else if notFound}
	<div class="surface p-10 text-center">
		<div class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4">
			<i class="bi bi-question-circle text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<p class="text-zinc-500 dark:text-zinc-400">{$_('devices.unknown_device')}</p>
	</div>
{:else if device}
	<!-- Hero -->
	<section class="surface p-5 mb-5 bg-gradient-to-br from-white to-brand-50/30 dark:from-zinc-900 dark:to-brand-500/5">
		<div class="flex items-start gap-4">
			<div
				class="
					w-14 h-14 shrink-0 rounded-2xl flex items-center justify-center text-2xl
					{device.online
						? 'bg-emerald-100 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
						: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-400 dark:text-zinc-500'}
				"
			>
				<i class="bi {deviceIcon(device)}"></i>
			</div>
			<div class="flex-1 min-w-0">
				<h1 class="text-xl font-semibold truncate">{device.label}</h1>
				<div class="flex items-center gap-2 mt-1.5 flex-wrap">
					{#if device.online}
						<span class="badge badge-ok">
							<span class="dot-live"></span>
							{$_('devices.online')}
						</span>
					{:else}
						<span class="badge badge-neutral">
							{$_('devices.last_seen', { values: { ago: relativeTime(device.last_seen) } })}
						</span>
					{/if}
					{#if device.stale}
						<span class="badge badge-warn" title={$_('devices.stale_help')}>
							<i class="bi bi-clock-history"></i>
							{$_('devices.stale')}
						</span>
					{/if}
					{#if device.profile_id}
						<span class="badge badge-info">
							<i class="bi bi-shield-check"></i>
							{device.profile_id}
						</span>
					{/if}
				</div>
			</div>
		</div>
	</section>

	<!-- Bandwidth (M32) — only when we have samples -->
	{#if bandwidth && bandwidth.sparkline.length > 0}
		<section class="surface p-5 mb-5">
			<h2 class="font-semibold mb-4 flex items-center gap-2">
				<i class="bi bi-graph-up text-brand-500"></i>
				{$_('devices.bandwidth_title')}
			</h2>
			<div class="grid grid-cols-2 gap-4 mb-4 text-sm">
				<div>
					<div class="text-xs text-zinc-500 dark:text-zinc-400">{$_('devices.bw_in_now')}</div>
					<div class="text-xl font-semibold tabular-nums text-brand-600 dark:text-brand-400">
						{bandwidth.last_sample.kbps_in < 1
							? '—'
							: bandwidth.last_sample.kbps_in < 1000
								? Math.round(bandwidth.last_sample.kbps_in) + ' Kbps'
								: (bandwidth.last_sample.kbps_in / 1000).toFixed(1) + ' Mbps'}
					</div>
				</div>
				<div>
					<div class="text-xs text-zinc-500 dark:text-zinc-400">{$_('devices.bw_out_now')}</div>
					<div class="text-xl font-semibold tabular-nums text-zinc-700 dark:text-zinc-300">
						{bandwidth.last_sample.kbps_out < 1
							? '—'
							: bandwidth.last_sample.kbps_out < 1000
								? Math.round(bandwidth.last_sample.kbps_out) + ' Kbps'
								: (bandwidth.last_sample.kbps_out / 1000).toFixed(1) + ' Mbps'}
					</div>
				</div>
			</div>
			<!-- Bigger sparkline. -->
			<div class="bg-zinc-50 dark:bg-zinc-900 rounded-lg p-3">
				<Sparkline values={bandwidth.sparkline} width={520} height={64} />
			</div>
			<div class="mt-3 grid grid-cols-2 gap-4 text-xs">
				<div>
					<span class="text-zinc-500 dark:text-zinc-400">{$_('devices.bw_total_in')}:</span>
					<span class="font-semibold tabular-nums">{fmtBytes(bandwidth.cum_in)}</span>
				</div>
				<div>
					<span class="text-zinc-500 dark:text-zinc-400">{$_('devices.bw_total_out')}:</span>
					<span class="font-semibold tabular-nums">{fmtBytes(bandwidth.cum_out)}</span>
				</div>
			</div>
			<div class="text-xs text-zinc-400 mt-2">{$_('devices.bw_help')}</div>
		</section>
	{/if}

	<!-- Identity -->
	<section class="surface p-5 mb-5">
		<h2 class="font-semibold mb-4 flex items-center gap-2">
			<i class="bi bi-info-circle text-brand-500"></i>
			{$_('devices.title')}
		</h2>
		<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
			<dt class="text-zinc-500 dark:text-zinc-400">{$_('devices.mac')}</dt>
			<dd class="font-mono">{device.mac}</dd>

			{#if device.hostname}
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('devices.hostname')}</dt>
				<dd class="font-medium">{device.hostname}</dd>
			{/if}

			{#if device.ip}
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('devices.ip')}</dt>
				<dd class="font-mono">{device.ip}</dd>
			{/if}

			<dt class="text-zinc-500 dark:text-zinc-400">{$_('devices.first_seen')}</dt>
			<dd>{relativeTime(device.first_seen)}</dd>
		</dl>
	</section>

	<!-- Profile picker -->
	<section class="surface p-5 mb-5">
		<h2 class="font-semibold mb-1 flex items-center gap-2">
			<i class="bi bi-shield-check text-brand-500"></i>
			{$_('device.profile_section')}
		</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">{$_('device.profile_help')}</p>

		<div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
			<!-- "no profile" tile -->
			<button
				type="button"
				disabled={savingProfile}
				class="
					text-left p-3 rounded-lg border transition-colors
					{selectedProfile === ''
						? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10'
						: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}
				"
				onclick={() => setProfile('')}
			>
				<div class="flex items-center gap-2">
					<i class="bi bi-circle text-lg text-zinc-400"></i>
					<span class="font-medium">{$_('device.profile_no_assignment')}</span>
				</div>
			</button>

			{#each profiles as p}
				<button
					type="button"
					disabled={savingProfile}
					class="
						text-left p-3 rounded-lg border transition-colors
						{selectedProfile === p.id
							? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10'
							: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}
					"
					onclick={() => setProfile(p.id)}
				>
					<div class="flex items-center gap-2 flex-wrap">
						<i class="bi bi-shield-check text-lg text-brand-500"></i>
						<span class="font-medium">{p.name}</span>
						{#if p.builtin}
							<span class="badge badge-info text-[10px]">{$_('profiles.builtin_badge')}</span>
						{/if}
					</div>
					{#if p.description}
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1.5 line-clamp-2">{p.description}</p>
					{/if}
				</button>
			{/each}
		</div>
	</section>

	<!-- Rename -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-4 flex items-center gap-2">
			<i class="bi bi-pencil-square text-brand-500"></i>
			{$_('devices.display_name_label')}
		</h2>
		<form onsubmit={save} class="space-y-3">
			<div>
				<input
					class="input"
					bind:value={displayName}
					placeholder={$_('devices.display_name_placeholder')}
				/>
				<p class="help">{$_('devices.display_name_help')}</p>
			</div>
			<div class="flex items-center gap-2">
				<button type="submit" class="btn-primary" disabled={saving}>
					{#if saving}
						<span class="spinner"></span>
						{$_('devices.saving')}
					{:else}
						<i class="bi bi-check2"></i>
						{$_('devices.save')}
					{/if}
				</button>
				<button type="button" class="btn-ghost" onclick={reset} disabled={saving}>
					<i class="bi bi-arrow-counterclockwise"></i>
					{$_('devices.reset_name')}
				</button>
				{#if savedFlash}
					<span class="text-sm text-emerald-600 dark:text-emerald-400 flex items-center gap-1">
						<i class="bi bi-check-circle-fill"></i>
						{$_('devices.saved')}
					</span>
				{/if}
			</div>
		</form>
		{#if error}
			<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
				<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
				<span>{error}</span>
			</div>
		{/if}
	</section>

	<!-- Wake-on-LAN: only when offline (no point waking an already-up box). -->
	{#if !device.online}
		<section class="surface p-5 mt-5">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-power text-emerald-500"></i>
				{$_('devices.wake_section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-3">
				{$_('devices.wake_help')}
			</p>
			<button
				type="button"
				class="btn-ghost"
				disabled={waking}
				onclick={wakeDevice}
			>
				{#if waking}
					<span class="spinner"></span>
					{$_('devices.waking')}
				{:else}
					<i class="bi bi-broadcast"></i>
					{$_('devices.wake')}
				{/if}
			</button>
			{#if wakeMsg}
				<div class="mt-3 text-sm text-zinc-600 dark:text-zinc-300">{wakeMsg}</div>
			{/if}
		</section>
	{/if}

	<!-- Forget / remove device -->
	{#if !device.online}
		<section class="surface p-5 mt-5 border-rose-200/60 dark:border-rose-900/40">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-trash3 text-rose-500"></i>
				{$_('devices.forget_section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-3">
				{$_('devices.forget_help')}
			</p>
			<button
				type="button"
				class="btn-ghost text-rose-600 hover:text-rose-700 dark:text-rose-400"
				disabled={deleting}
				onclick={forgetDevice}
			>
				{#if deleting}
					<span class="spinner"></span>
					{$_('devices.forgetting')}
				{:else}
					<i class="bi bi-trash3"></i>
					{$_('devices.forget')}
				{/if}
			</button>
		</section>
	{/if}
{/if}
