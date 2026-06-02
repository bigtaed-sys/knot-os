<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, API_BASE, ApiError } from '$lib/api';
	import type { Plugin, PluginsResponse } from '$lib/types';

	// Declarative plugin UI: the plugin process returns this JSON from
	// its proxy root; we render it with native KnotOS components, so
	// every plugin page looks like the rest of the app and no plugin
	// HTML/JS ever runs in the admin UI.
	type Tone = 'ok' | 'warn' | 'bad' | 'neutral';
	type Item =
		| { type: 'stat'; label: string; value: string; tone?: Tone }
		| { type: 'text'; text: string }
		| { type: 'badge'; text: string; tone?: Tone }
		| { type: 'table'; columns: string[]; rows: string[][] };
	type Section = { title?: string; items: Item[] };
	type Spec = { title?: string; refresh_sec?: number; sections: Section[] };

	const id = $derived($page.params.id);

	let plugin = $state<Plugin | null>(null);
	let spec = $state<Spec | null>(null);
	let loading = $state(true);
	let notFound = $state(false);
	let running = $state(true);
	let specError = $state<string | null>(null);
	let timer: ReturnType<typeof setInterval> | null = null;

	async function loadMeta() {
		try {
			const r = await apiGet<PluginsResponse>('/plugins');
			plugin = r.plugins.find((p) => p.id === id) ?? null;
			notFound = plugin === null;
			running = plugin?.runtime?.state === 'running';
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			notFound = true;
		}
	}

	async function loadSpec() {
		try {
			const res = await fetch(`${API_BASE}/plugins/${id}/proxy/`, { credentials: 'same-origin' });
			if (res.status === 502) {
				running = false;
				return;
			}
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			spec = (await res.json()) as Spec;
			running = true;
			specError = null;
		} catch (e) {
			specError = e instanceof Error ? e.message : String(e);
		}
	}

	async function refresh() {
		await loadMeta();
		if (!notFound && running) await loadSpec();
		loading = false;
		schedule();
	}

	function schedule() {
		if (timer) clearInterval(timer);
		const sec = spec?.refresh_sec ?? 0;
		if (sec > 0) timer = setInterval(loadSpec, sec * 1000);
	}

	onMount(refresh);
	onDestroy(() => {
		if (timer) clearInterval(timer);
	});
	$effect(() => {
		void id;
		loading = true;
		spec = null;
		refresh();
	});

	function toneClass(t?: Tone): string {
		switch (t) {
			case 'ok':
				return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400';
			case 'warn':
				return 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-400';
			case 'bad':
				return 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-400';
			default:
				return 'badge-neutral';
		}
	}

	const heading = $derived(spec?.title ?? plugin?.name ?? id);
</script>

<svelte:head>
	<title>{heading} · KnotOS</title>
</svelte:head>

<div class="max-w-3xl mx-auto">
	<header class="flex items-center gap-3 mb-5">
		<i class="bi {plugin?.menu?.[0]?.icon || 'bi-puzzle'} text-brand-500 text-lg"></i>
		<h1 class="text-2xl font-semibold flex-1 truncate">{heading}</h1>
		<a class="btn-ghost text-sm" href="/plugins"><i class="bi bi-gear"></i>{$_('plugins.manage')}</a>
	</header>

	{#if loading}
		<div class="surface p-10 grid place-items-center text-zinc-400"><div class="spinner"></div></div>
	{:else if notFound}
		<div class="surface grid place-items-center text-center p-8">
			<div>
				<i class="bi bi-question-circle text-3xl text-zinc-300"></i>
				<p class="mt-2 text-zinc-500">{$_('plugins.not_found')}</p>
			</div>
		</div>
	{:else if !running}
		<div class="surface grid place-items-center text-center p-8">
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
	{:else if specError}
		<div class="surface p-6 text-sm text-zinc-500">
			<i class="bi bi-exclamation-triangle text-amber-500 mr-1.5"></i>{$_('plugins.spec_error')}
			<span class="font-mono text-xs block mt-1 text-zinc-400">{specError}</span>
		</div>
	{:else if spec}
		<div class="space-y-4">
			{#each spec.sections as section}
				<section class="surface p-5">
					{#if section.title}
						<h2 class="font-semibold mb-3">{section.title}</h2>
					{/if}
					{#each section.items as item}
						{#if item.type === 'stat'}
							<div class="flex items-center justify-between py-2 border-b border-zinc-100 dark:border-zinc-800 last:border-0">
								<span class="text-zinc-500 dark:text-zinc-400 text-sm">{item.label}</span>
								{#if item.tone}
									<span class="badge {toneClass(item.tone)}">{item.value}</span>
								{:else}
									<span class="font-medium">{item.value}</span>
								{/if}
							</div>
						{:else if item.type === 'badge'}
							<span class="badge {toneClass(item.tone)} mr-1">{item.text}</span>
						{:else if item.type === 'text'}
							<p class="text-sm text-zinc-600 dark:text-zinc-300 py-1">{item.text}</p>
						{:else if item.type === 'table'}
							<div class="overflow-x-auto -mx-1">
								<table class="w-full text-sm">
									<thead>
										<tr class="text-left text-xs uppercase tracking-wide text-zinc-400">
											{#each item.columns as c}<th class="font-medium py-1 px-1">{c}</th>{/each}
										</tr>
									</thead>
									<tbody>
										{#each item.rows as row}
											<tr class="border-t border-zinc-100 dark:border-zinc-800">
												{#each row as cell}<td class="py-1.5 px-1 font-mono text-xs">{cell}</td>{/each}
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/if}
					{/each}
				</section>
			{/each}
		</div>
	{/if}
</div>
