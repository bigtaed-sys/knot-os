<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { onMount, onDestroy } from 'svelte';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { apiGet } from '$lib/api';
	import type { ScanResponse, CapabilityReport } from '$lib/types';
	import { wizard } from '../wizard.svelte';

	const linkedEths = $derived(wizard.cap?.eth.filter((e) => e.link) ?? []);

	let capTimer: ReturnType<typeof setInterval> | null = null;

	async function scan() {
		wizard.scanning = true;
		try {
			const r = await apiGet<ScanResponse>('/setup/scan', { timeoutMs: 20000 });
			wizard.networks = r.networks ?? [];
		} catch {
			wizard.networks = [];
		} finally {
			wizard.scanning = false;
		}
	}

	// Silent cap refresh — same idea as on the Hardware step. Keeps
	// the cable-status / WAN-port list current while the user lingers
	// on this screen, so plugging a cable in here also Just Works.
	async function refreshCap() {
		try {
			wizard.cap = await apiGet<CapabilityReport>('/setup/capability', { timeoutMs: 4000 });
			// Auto-pick the first cabled interface if the user hasn't
			// chosen one yet — usually there's exactly one.
			if (
				wizard.role === 'wifi-router' &&
				!wizard.wanInterface &&
				linkedEths.length > 0
			) {
				wizard.wanInterface = linkedEths[0].name;
			}
		} catch {
			// transient — keep previous cap.
		}
	}

	onMount(() => {
		if (wizard.role === 'wifi-extender' && wizard.networks.length === 0 && !wizard.scanning) {
			scan();
		}
		if (wizard.role === 'wifi-router') {
			refreshCap();
			capTimer = setInterval(refreshCap, 3000);
			if (!wizard.wanInterface && linkedEths.length > 0) {
				wizard.wanInterface = linkedEths[0].name;
			}
		}
	});

	onDestroy(() => {
		if (capTimer !== null) clearInterval(capTimer);
	});

	const uplinkSecured = $derived(
		wizard.networks.find((n) => n.ssid === wizard.uplinkSSID)?.secured ?? true
	);

	const canNext = $derived.by(() => {
		if (wizard.role === 'wifi-extender') {
			if (!wizard.uplinkSSID) return false;
			if (uplinkSecured && wizard.uplinkPSK.length < 8) return false;
			return true;
		}
		if (wizard.role === 'wifi-router') {
			return wizard.wanInterface !== '';
		}
		return false;
	});

	function rssiBars(dbm: number): number {
		// 4-bar scale, very rough.
		if (dbm >= -55) return 4;
		if (dbm >= -65) return 3;
		if (dbm >= -75) return 2;
		if (dbm >= -85) return 1;
		return 0;
	}
</script>

<StepCard
	title={wizard.role === 'wifi-router'
		? $_('setup.conn.router_title')
		: $_('setup.conn.extender_title')}
	subtitle={wizard.role === 'wifi-router'
		? $_('setup.conn.router_subtitle')
		: $_('setup.conn.extender_subtitle')}
	{canNext}
>
	{#if wizard.role === 'wifi-router'}
		<!-- WAN interface picker -->
		<div class="space-y-2">
			<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
				{$_('setup.conn.wan_iface_label')}
			</div>
			{#if linkedEths.length === 0}
				<div class="p-3 rounded-md bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 text-sm text-amber-800 dark:text-amber-300">
					<i class="bi bi-info-circle mr-1.5"></i>
					{$_('setup.conn.no_cable')}
				</div>
			{:else}
				<div class="space-y-2">
					{#each linkedEths as e}
						<label class="
							flex items-center gap-3 p-3 rounded-md border cursor-pointer
							{wizard.wanInterface === e.name
								? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
								: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300'}
						">
							<input
								type="radio"
								bind:group={wizard.wanInterface}
								value={e.name}
								class="text-brand-600"
							/>
							<i class="bi bi-ethernet text-emerald-500"></i>
							<div class="flex-1 min-w-0">
								<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{e.name}</div>
								<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate">{e.model}</div>
							</div>
							<span class="badge badge-ok text-[10px]">{$_('setup.hw.cable_in')}</span>
						</label>
					{/each}
				</div>
			{/if}
		</div>

		<!-- WAN mode (DHCP / PPPoE) -->
		<div class="space-y-2">
			<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
				{$_('setup.conn.wan_mode_label')}
			</div>
			<div class="grid grid-cols-2 gap-2">
				<button
					type="button"
					onclick={() => (wizard.wanMode = 'dhcp')}
					class="
						p-3 rounded-md border text-sm text-left
						{wizard.wanMode === 'dhcp'
							? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
							: 'border-zinc-200 dark:border-zinc-700'}
					"
				>
					<div class="font-medium">{$_('setup.conn.dhcp')}</div>
					<div class="text-xs text-zinc-500 mt-0.5">{$_('setup.conn.dhcp_desc')}</div>
				</button>
				<button
					type="button"
					disabled
					aria-disabled="true"
					title={$_('setup.conn.pppoe_wip')}
					class="
						relative p-3 rounded-md border text-sm text-left
						border-zinc-200 dark:border-zinc-700 opacity-60 cursor-not-allowed
					"
				>
					<div class="font-medium flex items-center gap-2">
						PPPoE
						<span class="badge text-[10px] bg-zinc-200 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-300">
							{$_('setup.conn.pppoe_wip_badge')}
						</span>
					</div>
					<div class="text-xs text-zinc-500 mt-0.5">{$_('setup.conn.pppoe_wip')}</div>
				</button>
			</div>
		</div>
	{:else}
		<!-- Extender uplink scan -->
		<div class="flex items-center justify-between mb-2">
			<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
				{$_('setup.conn.upstream_label')}
			</div>
			<button
				type="button"
				class="text-xs text-brand-600 dark:text-brand-400 hover:underline"
				onclick={scan}
				disabled={wizard.scanning}
			>
				{#if wizard.scanning}
					<i class="bi bi-arrow-clockwise animate-spin mr-1"></i>{$_('setup.conn.scanning')}
				{:else}
					<i class="bi bi-arrow-clockwise mr-1"></i>{$_('setup.conn.rescan')}
				{/if}
			</button>
		</div>
		{#if wizard.networks.length === 0}
			<div class="p-4 text-center text-sm text-zinc-500 bg-zinc-50 dark:bg-zinc-900/50 rounded-md">
				{wizard.scanning ? $_('setup.conn.scanning_for_networks') : $_('setup.conn.no_networks')}
			</div>
		{:else}
			<div class="space-y-1.5 max-h-72 overflow-y-auto">
				{#each wizard.networks as n}
					<label
						class="
							flex items-center gap-3 p-3 rounded-md border cursor-pointer
							{wizard.uplinkSSID === n.ssid
								? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
								: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300'}
						"
					>
						<input type="radio" bind:group={wizard.uplinkSSID} value={n.ssid} class="text-brand-600" />
						<div class="flex-1 min-w-0">
							<div class="font-medium text-sm truncate">{n.ssid || '(hidden)'}</div>
							<div class="text-xs text-zinc-500 dark:text-zinc-400">
								{$_('setup.conn.channel')}: {n.channel} · {n.band} GHz
								{#if n.secured}<i class="bi bi-lock-fill ml-1"></i>{/if}
							</div>
						</div>
						<!-- 4-bar signal -->
						<div class="flex items-end gap-0.5 h-4">
							{#each [1, 2, 3, 4] as i}
								<div
									class="w-1 rounded-sm
										{rssiBars(n.rssi_dbm) >= i
											? 'bg-emerald-500'
											: 'bg-zinc-200 dark:bg-zinc-700'}"
									style="height: {i * 25}%"
								></div>
							{/each}
						</div>
					</label>
				{/each}
			</div>
		{/if}

		{#if wizard.uplinkSSID && uplinkSecured}
			<div class="space-y-2 mt-3">
				<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
					{$_('setup.conn.upstream_psk_label')}
				</div>
				<input
					type="password"
					bind:value={wizard.uplinkPSK}
					placeholder={$_('setup.conn.upstream_psk_ph')}
					class="input"
				/>
			</div>
		{/if}
	{/if}
</StepCard>
