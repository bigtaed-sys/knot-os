<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError } from '$lib/api';
	import type { PortForward, PortForwardsResponse, Device, DevicesResponse } from '$lib/types';

	let rules = $state<PortForward[]>([]);
	let routerMode = $state(true);
	let devices = $state<Device[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let error = $state<string | null>(null);
	let savedFlash = $state(false);

	async function refresh() {
		loading = true;
		try {
			const r = await apiGet<PortForwardsResponse>('/portforwards');
			rules = r.port_forwards ?? [];
			routerMode = r.router_mode;
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
		try {
			const d = await apiGet<DevicesResponse>('/devices');
			devices = d.devices ?? [];
		} catch {
			/* non-fatal — the dest-IP datalist just stays empty */
		}
	}

	function addRule() {
		rules = [
			...rules,
			{ id: '', description: '', proto: 'tcp', wan_port: 0, dest_ip: '', dest_port: 0, enabled: true }
		];
	}

	function removeRule(i: number) {
		rules = rules.filter((_, idx) => idx !== i);
	}

	async function save() {
		saving = true;
		error = null;
		try {
			// Coerce numeric inputs (bind:value on number inputs can yield
			// strings when the field was touched) before sending.
			const payload = rules.map((r) => ({
				...r,
				wan_port: Number(r.wan_port) || 0,
				dest_port: Number(r.dest_port) || 0
			}));
			await apiPut('/portforwards', { port_forwards: payload }, { timeoutMs: 30000 });
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

	function deviceName(ip: string): string {
		const d = devices.find((d) => d.ip === ip);
		return d ? d.display_name || d.hostname || '' : '';
	}

	onMount(refresh);
</script>

<svelte:head>
	<title>{$_('portforward.title')} · KnotOS</title>
</svelte:head>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('portforward.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('portforward.subtitle')}</p>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else}
	{#if !routerMode}
		<div class="surface border-amber-300 dark:border-amber-500/30 p-4 mb-5 text-sm flex items-start gap-3">
			<i class="bi bi-info-circle text-amber-500 text-lg mt-0.5"></i>
			<span>{$_('portforward.not_router')}</span>
		</div>
	{/if}

	<!-- Known device IPs for the dest-IP autocomplete -->
	<datalist id="device-ips">
		{#each devices as d (d.mac)}
			{#if d.ip}
				<option value={d.ip}>{d.display_name || d.hostname || d.mac}</option>
			{/if}
		{/each}
	</datalist>

	<section class="surface p-4 mb-5">
		{#if rules.length === 0}
			<div class="text-center py-10 text-sm text-zinc-500 dark:text-zinc-400">
				{$_('portforward.empty')}
			</div>
		{:else}
			<div class="space-y-3">
				{#each rules as rule, i (i)}
					<div class="rounded-lg border border-zinc-200 dark:border-zinc-800 p-3">
						<div class="flex flex-wrap items-end gap-3">
							<label class="flex items-center gap-2 mb-1.5">
								<input type="checkbox" class="rounded text-brand-600" bind:checked={rule.enabled} />
								<span class="text-xs text-zinc-500">{$_('portforward.enabled')}</span>
							</label>
							<div class="flex-1 min-w-[140px]">
								<label class="label" for="desc{i}">{$_('portforward.description')}</label>
								<input
									id="desc{i}"
									class="input"
									bind:value={rule.description}
									placeholder={$_('portforward.description_placeholder')}
								/>
							</div>
							<div class="w-24">
								<label class="label" for="proto{i}">{$_('portforward.proto')}</label>
								<select id="proto{i}" class="input" bind:value={rule.proto}>
									<option value="tcp">TCP</option>
									<option value="udp">UDP</option>
									<option value="tcp/udp">TCP/UDP</option>
								</select>
							</div>
							<button
								type="button"
								class="btn-ghost text-sm text-red-600 dark:text-red-400 mb-0.5"
								onclick={() => removeRule(i)}
								aria-label={$_('portforward.remove')}
							>
								<i class="bi bi-trash"></i>
							</button>
						</div>
						<div class="flex flex-wrap items-end gap-3 mt-3">
							<div class="w-28">
								<label class="label" for="wan{i}">{$_('portforward.wan_port')}</label>
								<input
									id="wan{i}"
									type="number"
									min="1"
									max="65535"
									class="input"
									bind:value={rule.wan_port}
									placeholder="25565"
								/>
							</div>
							<div class="text-zinc-400 mb-2.5"><i class="bi bi-arrow-right"></i></div>
							<div class="flex-1 min-w-[160px]">
								<label class="label" for="dip{i}">{$_('portforward.dest_ip')}</label>
								<input
									id="dip{i}"
									class="input font-mono"
									list="device-ips"
									bind:value={rule.dest_ip}
									placeholder="192.168.42.50"
								/>
								{#if deviceName(rule.dest_ip)}
									<p class="help">{deviceName(rule.dest_ip)}</p>
								{/if}
							</div>
							<div class="w-28">
								<label class="label" for="dport{i}">{$_('portforward.dest_port')}</label>
								<input
									id="dport{i}"
									type="number"
									min="0"
									max="65535"
									class="input"
									bind:value={rule.dest_port}
									placeholder={$_('portforward.dest_port_placeholder')}
								/>
							</div>
						</div>
					</div>
				{/each}
			</div>
		{/if}

		<button class="btn-ghost text-sm mt-3" type="button" onclick={addRule}>
			<i class="bi bi-plus-lg"></i>
			{$_('portforward.add')}
		</button>
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
				<span class="spinner"></span>
				{$_('common.saving')}
			{:else}
				<i class="bi bi-check2"></i>
				{$_('portforward.save')}
			{/if}
		</button>
		{#if savedFlash}
			<span class="text-sm text-emerald-600 dark:text-emerald-400 flex items-center gap-1 ml-1">
				<i class="bi bi-check-circle-fill"></i>
				{$_('common.saved')}
			</span>
		{/if}
	</div>

	<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-4">
		<i class="bi bi-shield-exclamation mr-1"></i>{$_('portforward.security_note')}
	</p>
{/if}
