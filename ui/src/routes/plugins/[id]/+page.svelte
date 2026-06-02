<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, API_BASE, ApiError } from '$lib/api';
	import type { Plugin, PluginsResponse } from '$lib/types';

	const id = $derived($page.params.id);

	let plugin = $state<Plugin | null>(null);
	let loading = $state(true);
	let notFound = $state(false);
	let frameKey = $state(0); // bump to force-reload the iframe

	async function load() {
		loading = true;
		try {
			const r = await apiGet<PluginsResponse>('/plugins');
			plugin = r.plugins.find((p) => p.id === id) ?? null;
			notFound = plugin === null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			notFound = true;
		} finally {
			loading = false;
		}
	}

	onMount(load);
	// Reload when navigating between plugin pages without a full mount.
	$effect(() => {
		void id;
		load();
	});

	const src = $derived(`${API_BASE}/plugins/${id}/proxy/`);
	const running = $derived(plugin?.runtime?.state === 'running');
</script>

<svelte:head>
	<title>{plugin?.name ?? id} · KnotOS</title>
</svelte:head>

<div class="flex flex-col h-[calc(100vh-4rem)]">
	<header class="flex items-center gap-3 px-1 pb-3">
		<i class="bi {plugin?.menu?.[0]?.icon || 'bi-puzzle'} text-brand-500 text-lg"></i>
		<h1 class="text-lg font-semibold flex-1 truncate">{plugin?.name ?? id}</h1>
		{#if running}
			<button class="btn-ghost text-sm" onclick={() => (frameKey += 1)}>
				<i class="bi bi-arrow-clockwise"></i>{$_('plugins.reload')}
			</button>
		{/if}
		<a class="btn-ghost text-sm" href="/plugins"><i class="bi bi-gear"></i>{$_('plugins.manage')}</a>
	</header>

	{#if loading}
		<div class="flex-1 grid place-items-center text-zinc-400"><div class="spinner"></div></div>
	{:else if notFound}
		<div class="surface flex-1 grid place-items-center text-center p-8">
			<div>
				<i class="bi bi-question-circle text-3xl text-zinc-300"></i>
				<p class="mt-2 text-zinc-500">{$_('plugins.not_found')}</p>
			</div>
		</div>
	{:else if !running}
		<div class="surface flex-1 grid place-items-center text-center p-8">
			<div class="max-w-sm">
				<i class="bi bi-plug text-3xl text-zinc-300"></i>
				<p class="mt-2 font-medium">{$_('plugins.runtime_stopped_title')}</p>
				<p class="text-sm text-zinc-500 mt-1">{$_('plugins.runtime_stopped_help')}</p>
				{#if plugin?.runtime?.last_error}
					<p class="text-xs text-red-600 dark:text-red-400 font-mono mt-3 break-words">
						{plugin.runtime.last_error}
					</p>
				{/if}
				<a class="btn-primary mt-4 inline-block" href="/plugins">{$_('plugins.manage')}</a>
			</div>
		</div>
	{:else}
		{#key frameKey}
			<iframe
				title={plugin?.name ?? id}
				src={src}
				class="flex-1 w-full rounded-xl border border-zinc-200 dark:border-zinc-800 bg-white"
			></iframe>
		{/key}
	{/if}
</div>
