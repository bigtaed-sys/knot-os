<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError } from '$lib/api';
	import type { Plugin, PluginsResponse } from '$lib/types';

	let plugins = $state<Plugin[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let toggling = $state<Record<string, boolean>>({});

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

	onMount(refresh);

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
