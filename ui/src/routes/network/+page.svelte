<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError } from '$lib/api';
	import type { NetworkPortsResponse, PortView } from '$lib/types';

	let ports = $state<PortView[]>([]);
	let piModel = $state('');
	let routerMode = $state(true);
	let wanInterface = $state('');
	let lanPorts = $state<string[]>([]);
	let loading = $state(true);
	let saving = $state(false);
	let error = $state<string | null>(null);
	let savedFlash = $state(false);

	// Cabled ports that can be assigned as LAN: everything but the WAN.
	const lanCandidates = $derived(ports.filter((p) => p.name !== wanInterface));
	const dirty = $derived.by(() => {
		const orig = ports
			.filter((p) => p.role === 'lan')
			.map((p) => p.name)
			.sort()
			.join(',');
		const now = [...lanPorts].sort().join(',');
		const origWan = ports.find((p) => p.role === 'wan')?.name ?? '';
		return orig !== now || origWan !== wanInterface;
	});

	async function refresh() {
		loading = true;
		try {
			const r = await apiGet<NetworkPortsResponse>('/network/ports');
			ports = r.ports ?? [];
			piModel = r.pi_model_string ?? '';
			routerMode = r.router_mode;
			wanInterface = r.wan_interface ?? '';
			lanPorts = r.lan_ports ?? [];
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

	function pickWan(name: string) {
		wanInterface = name;
		// A port can't be both WAN and LAN.
		lanPorts = lanPorts.filter((p) => p !== name);
	}

	function toggleLan(name: string, on: boolean) {
		lanPorts = on ? [...new Set([...lanPorts, name])] : lanPorts.filter((p) => p !== name);
	}

	async function save() {
		saving = true;
		error = null;
		try {
			await apiPut(
				'/network/ports',
				{ wan_interface: wanInterface, lan_ports: lanPorts.filter((p) => p !== wanInterface) },
				{ timeoutMs: 45000 }
			);
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 2500);
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

	onMount(refresh);
</script>

<div class="max-w-3xl mx-auto space-y-6">
	<div>
		<h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">
			{$_('network.title')}
		</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('network.subtitle')}</p>
	</div>

	{#if loading}
		<div class="py-10 text-center"><div class="spinner mx-auto"></div></div>
	{:else if !routerMode}
		<div class="p-4 rounded-lg bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 text-sm text-amber-800 dark:text-amber-300">
			<i class="bi bi-info-circle mr-1.5"></i>{$_('network.not_router')}
		</div>
	{:else}
		{#if piModel}
			<div class="text-xs text-zinc-500 dark:text-zinc-400">
				<i class="bi bi-cpu mr-1"></i>{piModel}
			</div>
		{/if}

		{#if ports.length === 0}
			<div class="p-4 rounded-lg bg-zinc-50 dark:bg-zinc-900/50 border border-zinc-200 dark:border-zinc-800 text-sm text-zinc-500">
				<i class="bi bi-info-circle mr-1.5"></i>{$_('network.no_ports')}
			</div>
		{:else}
			<!-- WAN: exactly one port carries the internet -->
			<section class="space-y-2">
				<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
					{$_('network.wan_label')}
				</div>
				<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('network.wan_help')}</p>
				<div class="space-y-2">
					{#each ports as p}
						<label
							class="
								flex items-center gap-3 p-3 rounded-md border cursor-pointer
								{wanInterface === p.name
									? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
									: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300'}
							"
						>
							<input
								type="radio"
								name="wan"
								checked={wanInterface === p.name}
								onchange={() => pickWan(p.name)}
								class="text-brand-600"
							/>
							<i class="bi bi-ethernet {p.link ? 'text-emerald-500' : 'text-zinc-400'}"></i>
							<div class="flex-1 min-w-0">
								<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{p.name}</div>
								<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate">{p.model}</div>
							</div>
							<span
								class="badge text-[10px] {p.usb
									? 'bg-zinc-200 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-300'
									: 'bg-brand-100 dark:bg-brand-500/20 text-brand-700 dark:text-brand-300'}"
							>
								{p.usb ? $_('network.usb') : $_('network.onboard')}
							</span>
							{#if p.link}
								<span class="badge badge-ok text-[10px]"><span class="dot-live"></span>{$_('network.link')}</span>
							{/if}
						</label>
					{/each}
				</div>
			</section>

			<!-- LAN: the remaining ports, bridged with Wi-Fi into one network -->
			{#if lanCandidates.length > 0}
				<section class="space-y-2">
					<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
						{$_('network.lan_label')}
					</div>
					<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('network.lan_help')}</p>
					<div class="space-y-2">
						{#each lanCandidates as p}
							<label
								class="
									flex items-center gap-3 p-3 rounded-md border cursor-pointer
									{lanPorts.includes(p.name)
										? 'border-brand-500 bg-brand-50/40 dark:bg-brand-500/10'
										: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300'}
								"
							>
								<input
									type="checkbox"
									checked={lanPorts.includes(p.name)}
									onchange={(ev) => toggleLan(p.name, ev.currentTarget.checked)}
									class="text-brand-600"
								/>
								<i class="bi bi-diagram-3 text-brand-500"></i>
								<div class="flex-1 min-w-0">
									<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{p.name}</div>
									<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate">{p.model}</div>
								</div>
								{#if lanPorts.includes(p.name)}
									<span class="badge badge-ok text-[10px]">LAN</span>
								{/if}
							</label>
						{/each}
					</div>
				</section>
			{/if}

			{#if error}
				<div class="p-3 rounded-md bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/30 text-sm text-red-700 dark:text-red-300">
					<i class="bi bi-exclamation-circle mr-1.5"></i>{error}
				</div>
			{/if}

			<div class="flex items-center gap-3">
				<button type="button" class="btn btn-primary" disabled={saving || !dirty} onclick={save}>
					{#if saving}
						<i class="bi bi-arrow-clockwise animate-spin mr-1"></i>{$_('network.applying')}
					{:else}
						{$_('network.save')}
					{/if}
				</button>
				{#if savedFlash}
					<span class="text-sm text-emerald-600 dark:text-emerald-400">
						<i class="bi bi-check-circle mr-1"></i>{$_('network.saved')}
					</span>
				{/if}
			</div>
			<p class="text-xs text-zinc-400 dark:text-zinc-500">
				<i class="bi bi-exclamation-triangle mr-1"></i>{$_('network.reapply_warn')}
			</p>
		{/if}
	{/if}
</div>
