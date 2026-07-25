<script lang="ts">
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, apiDelete, API_BASE, ApiError } from '$lib/api';
	import Tabs from '$lib/components/Tabs.svelte';
	import type { Plugin, PluginsResponse } from '$lib/types';

	let activeTab = $state('installed');
	const tabList = $derived([
		{ id: 'installed', label: $_('plugins.tab_installed'), icon: 'bi-puzzle' },
		{ id: 'store', label: $_('plugins.tab_store'), icon: 'bi-bag' }
	]);

	let plugins = $state<Plugin[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let toggling = $state<Record<string, boolean>>({});

	// --- store ---------------------------------------------------------------
	type StoreEntry = {
		id: string;
		name: string;
		description?: string;
		version?: string;
		author?: string;
		official?: boolean;
		url: string;
		sig_url?: string;
		permissions?: string[];
		installed: boolean;
	};
	let store = $state<StoreEntry[]>([]);
	let storeError = $state<string | null>(null);
	let storeLoading = $state(true);
	let installing = $state<Record<string, boolean>>({});
	let confirmEntry = $state<StoreEntry | null>(null); // third-party confirm modal

	async function loadStore() {
		storeLoading = true;
		try {
			const r = await apiGet<{ plugins: StoreEntry[] }>('/plugins/store');
			store = r.plugins ?? [];
			storeError = null;
		} catch (e) {
			storeError = e instanceof Error ? e.message : String(e);
		} finally {
			storeLoading = false;
		}
	}

	async function install(entry: StoreEntry, confirm = false) {
		installing = { ...installing, [entry.id]: true };
		confirmEntry = null;
		try {
			const res = await fetch(`${API_BASE}/plugins/install`, {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ url: entry.url, sig_url: entry.sig_url, confirm })
			});
			if (res.status === 409) {
				// Untrusted package — ask the operator to confirm.
				confirmEntry = entry;
				return;
			}
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				throw new Error(body?.error?.message ?? `HTTP ${res.status}`);
			}
			await Promise.all([refresh(), loadStore()]);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			installing = { ...installing, [entry.id]: false };
		}
	}

	async function uninstall(p: Plugin) {
		if (!confirm($_('plugins.uninstall_confirm', { values: { name: p.name } }))) return;
		try {
			await apiDelete(`/plugins/${encodeURIComponent(p.id)}`);
			await Promise.all([refresh(), loadStore()]);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function refresh() {
		loading = true;
		try {
			const r = await apiGet<PluginsResponse>('/plugins');
			plugins = r.plugins;
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

	async function toggle(p: Plugin) {
		toggling = { ...toggling, [p.id]: true };
		try {
			const updated = await apiPut<Plugin>(`/plugins/${encodeURIComponent(p.id)}`, {
				enabled: !p.enabled
			});
			plugins = plugins.map((x) => (x.id === updated.id ? updated : x));
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			toggling = { ...toggling, [p.id]: false };
		}
	}

	onMount(() => {
		refresh();
		loadStore();
	});

	function runtimeBadge(p: Plugin): { label: string; cls: string; icon: string } | null {
		if (!p.runtime) return null;
		switch (p.runtime.state) {
			case 'running':
				return { label: $_('plugins.rt_running'), cls: 'badge-ok', icon: 'bi-play-circle-fill' };
			case 'crashed':
				return {
					label: $_('plugins.rt_crashed'),
					cls: 'badge bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-400',
					icon: 'bi-exclamation-triangle-fill'
				};
			default:
				return { label: $_('plugins.rt_stopped'), cls: 'badge-neutral', icon: 'bi-stop-circle' };
		}
	}

	function hasUI(p: Plugin): boolean {
		return !!p.menu && p.menu.length > 0;
	}
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('plugins.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('plugins.subtitle')}</p>
</header>

<Tabs tabs={tabList} bind:active={activeTab} />

{#key activeTab}
<div in:fade={{ duration: 140 }}>
{#if activeTab === 'installed'}
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
{:else if plugins.length === 0}
	<div class="surface p-10 text-center">
		<div class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4">
			<i class="bi bi-puzzle text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<h2 class="font-medium text-lg mb-2">{$_('plugins.empty_title')}</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
			{$_('plugins.empty_help')}
		</p>
	</div>
{:else}
	<div class="space-y-3">
		{#each plugins as p (p.id)}
			<article class="surface p-5">
				<div class="flex items-start gap-4">
					<div class="w-12 h-12 shrink-0 rounded-xl bg-brand-100 dark:bg-brand-500/15 flex items-center justify-center text-brand-700 dark:text-brand-300">
						<i class="bi {p.menu?.[0]?.icon || 'bi-puzzle'} text-xl"></i>
					</div>
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 flex-wrap">
							<h2 class="font-semibold">{p.name}</h2>
							<span class="badge badge-neutral font-mono">v{p.version}</span>
							{#if p.enabled}
								<span class="badge badge-ok">
									<i class="bi bi-check-circle-fill text-xs"></i>
									{$_('plugins.enabled')}
								</span>
							{/if}
							{#if runtimeBadge(p)}
								{@const rb = runtimeBadge(p)}
								<span class="badge {rb!.cls}" title={p.runtime?.last_error ?? ''}>
									<i class="bi {rb!.icon} text-xs"></i>
									{rb!.label}{#if (p.runtime?.restarts ?? 0) > 0} · ×{p.runtime?.restarts}{/if}
								</span>
							{/if}
						</div>
						<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono mt-0.5">{p.id}</div>
						{#if p.description}
							<p class="text-sm text-zinc-600 dark:text-zinc-300 mt-2">{p.description}</p>
						{/if}
					</div>
					<div class="flex items-center gap-2 shrink-0">
						{#if p.enabled && hasUI(p)}
							<a class="btn-ghost" href={`/plugins/${encodeURIComponent(p.id)}`}>
								<i class="bi bi-box-arrow-up-right"></i>
								{$_('plugins.open')}
							</a>
						{/if}
						<button
							type="button"
							class={p.enabled ? 'btn-ghost' : 'btn-primary'}
							disabled={toggling[p.id]}
							onclick={() => toggle(p)}
						>
							{#if toggling[p.id]}
								<span class="spinner"></span>
							{:else if p.enabled}
								<i class="bi bi-toggle-on"></i>
								{$_('plugins.disable')}
							{:else}
								<i class="bi bi-toggle-off"></i>
								{$_('plugins.enable')}
							{/if}
						</button>
						<button
							type="button"
							class="btn-ghost text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10"
							onclick={() => uninstall(p)}
							title={$_('plugins.uninstall')}
							aria-label={$_('plugins.uninstall')}
						>
							<i class="bi bi-trash"></i>
						</button>
					</div>
				</div>
			</article>
		{/each}
	</div>

	<p class="mt-5 text-xs text-zinc-500 dark:text-zinc-400">
		<i class="bi bi-info-circle"></i>
		{$_('plugins.v01_note')}
	</p>
{/if}
{/if}

{#if activeTab === 'store'}
<!-- Store -->
<section class="mt-8">
	<h2 class="text-lg font-semibold flex items-center gap-2">
		<i class="bi bi-bag text-brand-500"></i>{$_('plugins.store_title')}
	</h2>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1 mb-4">{$_('plugins.store_subtitle')}</p>

	{#if storeLoading}
		<div class="flex items-center gap-2 text-zinc-400 text-sm"><div class="spinner"></div>{$_('common.loading')}</div>
	{:else if storeError}
		<div class="surface p-4 text-sm text-zinc-500">
			<i class="bi bi-cloud-slash mr-1.5"></i>{$_('plugins.store_unreachable')}
			<span class="font-mono text-xs block mt-1 text-zinc-400">{storeError}</span>
		</div>
	{:else}
		{@const available = store.filter((e) => !e.installed)}
		{#if available.length === 0}
			<div class="surface p-6 text-center text-sm text-zinc-500">{$_('plugins.store_empty')}</div>
		{:else}
			<div class="space-y-3">
				{#each available as e (e.id)}
					<article class="surface p-5">
						<div class="flex items-start gap-4">
							<div class="w-12 h-12 shrink-0 rounded-xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center text-zinc-500">
								<i class="bi bi-puzzle text-xl"></i>
							</div>
							<div class="flex-1 min-w-0">
								<div class="flex items-center gap-2 flex-wrap">
									<h3 class="font-semibold">{e.name}</h3>
									{#if e.version}<span class="badge badge-neutral font-mono">v{e.version}</span>{/if}
									{#if e.official}
										<span class="badge badge-ok"><i class="bi bi-patch-check-fill text-xs"></i>{$_('plugins.official')}</span>
									{:else}
										<span class="badge bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-400">
											<i class="bi bi-exclamation-triangle-fill text-xs"></i>{$_('plugins.third_party')}
										</span>
									{/if}
								</div>
								{#if e.author}<div class="text-xs text-zinc-500 mt-0.5">{$_('plugins.by', { values: { author: e.author } })}</div>{/if}
								{#if e.description}<p class="text-sm text-zinc-600 dark:text-zinc-300 mt-2">{e.description}</p>{/if}
								{#if e.permissions && e.permissions.length}
									<div class="flex flex-wrap gap-1 mt-2">
										{#each e.permissions as perm}
											<span class="badge badge-neutral font-mono text-[10px]">{perm}</span>
										{/each}
									</div>
								{/if}
							</div>
							<button class="btn-primary shrink-0" disabled={installing[e.id]} onclick={() => install(e)}>
								{#if installing[e.id]}
									<span class="spinner"></span>
								{:else}
									<i class="bi bi-download"></i>{$_('plugins.install')}
								{/if}
							</button>
						</div>
					</article>
				{/each}
			</div>
		{/if}
	{/if}
</section>
{/if}
</div>
{/key}

<!-- Third-party install confirmation -->
{#if confirmEntry}
	<div
		class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
		role="presentation"
		onclick={(ev) => {
			if (ev.target === ev.currentTarget) confirmEntry = null;
		}}
	>
		<div class="surface p-5 max-w-md w-full">
			<h2 class="text-lg font-semibold flex items-center gap-2 text-amber-600 dark:text-amber-400">
				<i class="bi bi-exclamation-triangle-fill"></i>{$_('plugins.confirm_title')}
			</h2>
			<p class="text-sm text-zinc-600 dark:text-zinc-300 mt-3">
				{$_('plugins.confirm_body', { values: { name: confirmEntry.name } })}
			</p>
			<div class="flex justify-end gap-2 mt-5">
				<button class="btn-ghost" onclick={() => (confirmEntry = null)}>{$_('common.cancel')}</button>
				<button
					class="btn-primary bg-amber-600 hover:bg-amber-700"
					onclick={() => confirmEntry && install(confirmEntry, true)}
				>
					{$_('plugins.confirm_install')}
				</button>
			</div>
		</div>
	</div>
{/if}
