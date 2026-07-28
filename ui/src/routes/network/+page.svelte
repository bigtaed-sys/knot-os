<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError } from '$lib/api';
	import Tabs from '$lib/components/Tabs.svelte';
	import type { NetworkResponse, PortView, ScannedNetwork, ModemStatus } from '$lib/types';

	// --- loaded state ---
	let loading = $state(true);
	let error = $state<string | null>(null);
	let savedFlash = $state(false);
	let saving = $state(false);
	let piModel = $state('');
	let ports = $state<PortView[]>([]);
	let modem = $state<ModemStatus>({ present: false, signal_percent: 0 });

	// --- editable settings (initialised from GET /network) ---
	let role = $state<'wifi-router' | 'wifi-extender'>('wifi-router');
	let wanMode = $state<'dhcp' | 'modem'>('dhcp');
	let wanInterface = $state('');
	let mApn = $state('');
	let mPin = $state('');
	let mSimSlot = $state(0);
	let mDataLimit = $state(0);
	let mCycleDay = $state(1);
	let lanPorts = $state<string[]>([]);
	let apSSID = $state('');
	let apPSK = $state('');
	let apBand = $state<'2.4' | '5'>('2.4');
	let apChannel = $state(0);
	let uplinkSSID = $state('');
	let uplinkPSK = $state('');
	let lanCIDR = $state('192.168.42.0/24');
	let poolStart = $state('192.168.42.100');
	let poolEnd = $state('192.168.42.200');

	let activeTab = $state('mode');

	// Uplink scan (extender)
	let networks = $state<ScannedNetwork[]>([]);
	let scanning = $state(false);

	const cabledPorts = $derived(ports.filter((p) => p.link));
	const lanCandidates = $derived(cabledPorts.filter((p) => p.name !== wanInterface));

	async function load() {
		loading = true;
		try {
			const r = await apiGet<NetworkResponse>('/network');
			ports = r.ports ?? [];
			modem = r.modem ?? { present: false, signal_percent: 0 };
			piModel = r.pi_model_string ?? '';
			if (r.role === 'wifi-extender') role = 'wifi-extender';
			else role = 'wifi-router';
			const n = r.network ?? {};
			wanMode = n.wan?.mode === 'modem' ? 'modem' : 'dhcp';
			wanInterface = n.wan?.interface ?? '';
			mApn = n.wan?.modem?.apn ?? '';
			mPin = n.wan?.modem?.pin ?? '';
			mSimSlot = n.wan?.modem?.sim_slot ?? 0;
			mDataLimit = n.wan?.modem?.data_limit_mb ?? 0;
			mCycleDay = n.wan?.modem?.cycle_reset_day ?? 1;
			lanPorts = n.lan_ports ?? [];
			apSSID = n.ap?.ssid ?? '';
			apPSK = n.ap?.psk ?? '';
			apBand = n.ap?.band === '5' ? '5' : '2.4';
			apChannel = n.ap?.channel ?? 0;
			uplinkSSID = n.uplink?.ssid ?? '';
			uplinkPSK = n.uplink?.psk ?? '';
			lanCIDR = n.lan?.cidr ?? '192.168.42.0/24';
			poolStart = n.lan?.dhcp?.pool_start ?? '192.168.42.100';
			poolEnd = n.lan?.dhcp?.pool_end ?? '192.168.42.200';
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

	async function scan() {
		scanning = true;
		try {
			const r = await apiGet<{ networks: ScannedNetwork[] }>('/network/scan', { timeoutMs: 20000 });
			networks = r.networks ?? [];
		} catch {
			networks = [];
		} finally {
			scanning = false;
		}
	}

	function toggleLan(name: string, on: boolean) {
		lanPorts = on ? [...new Set([...lanPorts, name])] : lanPorts.filter((p) => p !== name);
	}

	// A port can't be both WAN and LAN.
	$effect(() => {
		if (wanInterface && lanPorts.includes(wanInterface)) {
			lanPorts = lanPorts.filter((p) => p !== wanInterface);
		}
	});

	const canSave = $derived.by(() => {
		if (apSSID.trim().length === 0 || apSSID.length > 32) return false;
		if (apPSK.length > 0 && (apPSK.length < 8 || apPSK.length > 63)) return false;
		if (role === 'wifi-router') {
			if (wanMode === 'dhcp' && !wanInterface) return false;
		} else {
			if (!uplinkSSID.trim()) return false;
		}
		return true;
	});

	async function save() {
		saving = true;
		error = null;
		try {
			const body: Record<string, unknown> = {
				role,
				ap: { ssid: apSSID, psk: apPSK, band: apBand, channel: Number(apChannel) || 0 },
				lan: {
					cidr: lanCIDR,
					dhcp: { pool_start: poolStart, pool_end: poolEnd }
				}
			};
			if (role === 'wifi-router') {
				if (wanMode === 'modem') {
					body.wan = {
						mode: 'modem',
						modem: {
							apn: mApn,
							pin: mPin,
							sim_slot: Number(mSimSlot) || 0,
							data_limit_mb: Number(mDataLimit) || 0,
							cycle_reset_day: Number(mCycleDay) || 1
						}
					};
				} else {
					body.wan = { mode: 'dhcp', interface: wanInterface };
				}
				body.lan_ports = lanPorts.filter((p) => p !== wanInterface);
			} else {
				body.uplink = { ssid: uplinkSSID, psk: uplinkPSK };
			}

			await apiPut('/network', body, { timeoutMs: 60000 });
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 3000);
			await load();
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

	const tabs = $derived.by(() => {
		const t = [
			{ id: 'mode', label: $_('network.tab_mode'), icon: 'bi-diagram-2' },
			{ id: 'wan', label: $_('network.tab_internet'), icon: 'bi-globe' },
			{ id: 'wifi', label: $_('network.tab_wifi'), icon: 'bi-wifi' }
		];
		if (role === 'wifi-router' && lanCandidates.length > 0) {
			t.push({ id: 'ports', label: $_('network.tab_ports'), icon: 'bi-ethernet' });
		}
		t.push({ id: 'lan', label: $_('network.tab_lan'), icon: 'bi-hdd-network' });
		return t;
	});

	onMount(load);
</script>

<div class="max-w-3xl mx-auto space-y-5">
	<div class="flex items-start justify-between gap-4">
		<div>
			<h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{$_('network.title')}</h1>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('network.subtitle')}</p>
		</div>
		{#if piModel}
			<div class="text-xs text-zinc-400 dark:text-zinc-500 whitespace-nowrap mt-1">
				<i class="bi bi-cpu mr-1"></i>{piModel}
			</div>
		{/if}
	</div>

	{#if loading}
		<div class="py-10 text-center"><div class="spinner mx-auto"></div></div>
	{:else}
		<Tabs {tabs} bind:active={activeTab} />

		<!-- ============ MODE ============ -->
		{#if activeTab === 'mode'}
			<div class="space-y-3">
				<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('network.mode_help')}</p>
				<button
					type="button"
					onclick={() => (role = 'wifi-router')}
					class="w-full text-left p-4 rounded-lg border
						{role === 'wifi-router' ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}"
				>
					<div class="font-medium flex items-center gap-2"><i class="bi bi-router"></i>{$_('network.mode_router')}</div>
					<div class="text-xs text-zinc-500 mt-0.5">{$_('network.mode_router_desc')}</div>
				</button>
				<button
					type="button"
					onclick={() => (role = 'wifi-extender')}
					class="w-full text-left p-4 rounded-lg border
						{role === 'wifi-extender' ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}"
				>
					<div class="font-medium flex items-center gap-2"><i class="bi bi-broadcast-pin"></i>{$_('network.mode_extender')}</div>
					<div class="text-xs text-zinc-500 mt-0.5">{$_('network.mode_extender_desc')}</div>
				</button>
			</div>
		{/if}

		<!-- ============ INTERNET (WAN / UPLINK) ============ -->
		{#if activeTab === 'wan'}
			{#if role === 'wifi-router'}
				<div class="space-y-4">
					<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('network.wan_source')}</div>
					<div class="grid grid-cols-2 gap-2">
						<button
							type="button"
							onclick={() => (wanMode = 'dhcp')}
							class="p-3 rounded-md border text-sm text-left
								{wanMode === 'dhcp' ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}"
						>
							<div class="font-medium flex items-center gap-2"><i class="bi bi-ethernet"></i>{$_('network.wan_ethernet')}</div>
							<div class="text-xs text-zinc-500 mt-0.5">{$_('network.wan_ethernet_desc')}</div>
						</button>
						<button
							type="button"
							onclick={() => (wanMode = 'modem')}
							class="p-3 rounded-md border text-sm text-left
								{wanMode === 'modem' ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}"
						>
							<div class="font-medium flex items-center gap-2">
								<i class="bi bi-sim"></i>{$_('network.wan_modem')}
								{#if modem.present}<span class="badge badge-ok text-[10px]">{$_('network.modem_detected')}</span>{/if}
							</div>
							<div class="text-xs text-zinc-500 mt-0.5">{$_('network.wan_modem_desc')}</div>
						</button>
					</div>

					{#if wanMode === 'dhcp'}
						<div class="space-y-2">
							<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('network.wan_port')}</div>
							{#if cabledPorts.length === 0}
								<div class="p-3 rounded-md bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 text-sm text-amber-800 dark:text-amber-300">
									<i class="bi bi-info-circle mr-1.5"></i>{$_('network.no_cable')}
								</div>
							{:else}
								{#each cabledPorts as p}
									<label class="flex items-center gap-3 p-3 rounded-md border cursor-pointer
										{wanInterface === p.name ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}">
										<input type="radio" name="wan" value={p.name} bind:group={wanInterface} class="text-brand-600" />
										<i class="bi bi-ethernet text-emerald-500"></i>
										<div class="flex-1 min-w-0">
											<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{p.name}</div>
											<div class="text-xs text-zinc-500 truncate">{p.model}</div>
										</div>
										<span class="badge text-[10px] {p.usb ? 'bg-zinc-200 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-300' : 'bg-brand-100 dark:bg-brand-500/20 text-brand-700 dark:text-brand-300'}">
											{p.usb ? $_('network.usb') : $_('network.onboard')}
										</span>
									</label>
								{/each}
							{/if}
						</div>
					{:else}
						<div class="space-y-3">
							{#if modem.present}
								<div class="p-3 rounded-md bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/30 text-sm text-emerald-800 dark:text-emerald-300 flex items-center gap-2">
									<i class="bi bi-sim"></i>
									<span>{modem.manufacturer || ''} {modem.model || $_('modem.unknown_model')}{#if modem.operator} · {modem.operator}{/if}</span>
								</div>
							{:else}
								<div class="p-3 rounded-md bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 text-sm text-amber-800 dark:text-amber-300">
									<i class="bi bi-info-circle mr-1.5"></i>{$_('network.modem_none')}
								</div>
							{/if}
							<div>
								<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-apn">{$_('modem.apn')} <span class="font-normal text-zinc-400">({$_('modem.optional')})</span></label>
								<input id="net-apn" class="input mt-1 font-mono" bind:value={mApn} placeholder="internet" />
							</div>
							<div class="grid grid-cols-2 gap-3">
								<div>
									<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-pin">{$_('modem.pin')} <span class="font-normal text-zinc-400">({$_('modem.optional')})</span></label>
									<input id="net-pin" class="input mt-1 font-mono" type="password" bind:value={mPin} placeholder="----" />
								</div>
								<div>
									<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-limit">{$_('network.data_limit_mb')}</label>
									<input id="net-limit" class="input mt-1" type="number" min="0" bind:value={mDataLimit} />
								</div>
							</div>
						</div>
					{/if}
				</div>
			{:else}
				<!-- Extender uplink -->
				<div class="space-y-3">
					<div class="flex items-center justify-between">
						<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('network.uplink_label')}</div>
						<button type="button" class="text-xs text-brand-600 dark:text-brand-400 hover:underline" onclick={scan} disabled={scanning}>
							<i class="bi bi-arrow-clockwise mr-1 {scanning ? 'animate-spin' : ''}"></i>{$_('network.scan')}
						</button>
					</div>
					{#if networks.length > 0}
						<div class="space-y-1.5 max-h-56 overflow-y-auto">
							{#each networks as n}
								<label class="flex items-center gap-3 p-2.5 rounded-md border cursor-pointer
									{uplinkSSID === n.ssid ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}">
									<input type="radio" name="uplink" value={n.ssid} bind:group={uplinkSSID} class="text-brand-600" />
									<span class="flex-1 text-sm truncate">{n.ssid || '(hidden)'}</span>
									<span class="text-xs text-zinc-400">{n.band} GHz{#if n.secured}<i class="bi bi-lock-fill ml-1"></i>{/if}</span>
								</label>
							{/each}
						</div>
					{/if}
					<div>
						<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-uplink-ssid">{$_('network.uplink_ssid')}</label>
						<input id="net-uplink-ssid" class="input mt-1" bind:value={uplinkSSID} placeholder="Upstream-WiFi" />
					</div>
					<div>
						<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-uplink-psk">{$_('network.uplink_psk')}</label>
						<input id="net-uplink-psk" class="input mt-1 font-mono" type="password" bind:value={uplinkPSK} />
					</div>
				</div>
			{/if}
		{/if}

		<!-- ============ WI-FI (AP) ============ -->
		{#if activeTab === 'wifi'}
			<div class="space-y-4">
				<div>
					<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-ap-ssid">{$_('network.ap_ssid')}</label>
					<input id="net-ap-ssid" class="input mt-1" bind:value={apSSID} maxlength={32} placeholder="MyHome" />
				</div>
				<div>
					<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-ap-psk">{$_('network.ap_psk')}</label>
					<input id="net-ap-psk" class="input mt-1 font-mono" type="text" bind:value={apPSK} minlength={8} maxlength={63} />
					<p class="text-xs text-zinc-500 mt-1">{$_('network.ap_psk_help')}</p>
				</div>
				<div>
					<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('network.ap_band')}</div>
					<div class="flex gap-2 mt-1.5">
						{#each ['2.4', '5'] as b}
							<button type="button" onclick={() => (apBand = b as '2.4' | '5')}
								class="px-4 py-1.5 rounded-md border text-sm {apBand === b ? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10 text-brand-700 dark:text-brand-300' : 'border-zinc-200 dark:border-zinc-700'}">
								{b} GHz
							</button>
						{/each}
					</div>
				</div>
				<div>
					<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('network.ap_channel')}</div>
					<div class="flex gap-2 mt-1.5">
						{#each [{ v: 0, l: $_('network.ap_channel_auto') }, { v: 1, l: '1' }, { v: 6, l: '6' }, { v: 11, l: '11' }] as opt}
							<button type="button" onclick={() => (apChannel = opt.v)}
								class="px-3 py-1.5 rounded-md border text-xs flex-1 {apChannel === opt.v ? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10 text-brand-700 dark:text-brand-300' : 'border-zinc-200 dark:border-zinc-700'}">
								{opt.l}
							</button>
						{/each}
					</div>
				</div>
			</div>
		{/if}

		<!-- ============ WIRED PORTS (LAN) ============ -->
		{#if activeTab === 'ports'}
			<div class="space-y-2">
				<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('network.ports_help')}</p>
				{#each lanCandidates as p}
					<label class="flex items-center gap-3 p-3 rounded-md border cursor-pointer
						{lanPorts.includes(p.name) ? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10' : 'border-zinc-200 dark:border-zinc-700'}">
						<input type="checkbox" checked={lanPorts.includes(p.name)} onchange={(e) => toggleLan(p.name, e.currentTarget.checked)} class="text-brand-600" />
						<i class="bi bi-diagram-3 text-brand-500"></i>
						<div class="flex-1 min-w-0">
							<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{p.name}</div>
							<div class="text-xs text-zinc-500 truncate">{p.model}</div>
						</div>
						{#if lanPorts.includes(p.name)}<span class="badge badge-ok text-[10px]">LAN</span>{/if}
					</label>
				{/each}
			</div>
		{/if}

		<!-- ============ LAN / DHCP ============ -->
		{#if activeTab === 'lan'}
			<div class="space-y-4">
				<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('network.lan_help')}</p>
				<div>
					<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-cidr">{$_('network.lan_cidr')}</label>
					<input id="net-cidr" class="input mt-1 font-mono" bind:value={lanCIDR} placeholder="192.168.42.0/24" />
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div>
						<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-pool-start">{$_('network.pool_start')}</label>
						<input id="net-pool-start" class="input mt-1 font-mono" bind:value={poolStart} />
					</div>
					<div>
						<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300" for="net-pool-end">{$_('network.pool_end')}</label>
						<input id="net-pool-end" class="input mt-1 font-mono" bind:value={poolEnd} />
					</div>
				</div>
			</div>
		{/if}

		<!-- ============ SAVE BAR ============ -->
		{#if error}
			<div class="p-3 rounded-md bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/30 text-sm text-red-700 dark:text-red-300">
				<i class="bi bi-exclamation-circle mr-1.5"></i>{error}
			</div>
		{/if}

		<div class="flex items-center gap-3 pt-2 border-t border-zinc-200 dark:border-zinc-800">
			<button type="button" class="btn btn-primary" disabled={saving || !canSave} onclick={save}>
				{#if saving}<i class="bi bi-arrow-clockwise animate-spin mr-1"></i>{$_('network.applying')}{:else}{$_('network.save')}{/if}
			</button>
			{#if savedFlash}
				<span class="text-sm text-emerald-600 dark:text-emerald-400"><i class="bi bi-check-circle mr-1"></i>{$_('network.saved')}</span>
			{/if}
		</div>
		<p class="text-xs text-zinc-400 dark:text-zinc-500">
			<i class="bi bi-exclamation-triangle mr-1"></i>{$_('network.reapply_warn')}
		</p>
	{/if}
</div>
