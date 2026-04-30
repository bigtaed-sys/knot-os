<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
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
</script>

<h1>Plugins</h1>
<p class="muted">Optional features that extend KnotOS.</p>

{#if loading}
	<p class="muted">Loading…</p>
{:else if error}
	<div class="error">{error}</div>
{:else if plugins.length === 0}
	<div class="card empty">
		<p>No plugins installed.</p>
		<p class="muted small">
			Plugins are placed under <code>/usr/lib/knot/plugins/</code> on the device.
			Reboot or restart <code>knotd</code> to discover newly installed ones.
		</p>
	</div>
{:else}
	<ul class="list">
		{#each plugins as p (p.id)}
			<li class="card">
				<div class="row">
					<div class="info">
						<h2>{p.name}</h2>
						<div class="meta">
							<code>{p.id}</code>
							<span>v{p.version}</span>
						</div>
						{#if p.description}
							<p class="desc">{p.description}</p>
						{/if}
					</div>
					<button
						class="toggle"
						class:on={p.enabled}
						disabled={toggling[p.id]}
						onclick={() => toggle(p)}
					>
						{p.enabled ? 'Enabled' : 'Disabled'}
					</button>
				</div>
			</li>
		{/each}
	</ul>

	<p class="muted small note">
		v0.1 plugins are metadata only — the runtime that actually executes plugin code arrives in v0.2.
	</p>
{/if}

<style>
	h1 {
		margin: 0;
	}
	.muted {
		color: #6b7280;
	}
	.small {
		font-size: 0.875rem;
	}
	.list {
		list-style: none;
		padding: 0;
		margin: 1.25rem 0 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.card {
		padding: 1rem 1.25rem;
		border: 1px solid #e5e7eb;
		border-radius: 8px;
		background: #f9fafb;
	}
	.card.empty {
		text-align: center;
	}
	.card.empty p:first-child {
		margin-top: 0;
	}
	.row {
		display: flex;
		justify-content: space-between;
		align-items: flex-start;
		gap: 1rem;
	}
	.info h2 {
		font-size: 1rem;
		margin: 0 0 0.25rem;
	}
	.meta {
		display: flex;
		gap: 0.75rem;
		color: #6b7280;
		font-size: 0.875rem;
	}
	.desc {
		margin: 0.5rem 0 0;
		color: #374151;
	}
	.toggle {
		flex-shrink: 0;
		padding: 0.4rem 0.9rem;
		border: 1px solid #d1d5db;
		border-radius: 999px;
		background: white;
		color: #6b7280;
		font-weight: 500;
		font-size: 0.875rem;
		cursor: pointer;
	}
	.toggle.on {
		background: #059669;
		color: white;
		border-color: #047857;
	}
	.toggle:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.toggle:not(:disabled):hover {
		border-color: #9ca3af;
	}
	.toggle.on:not(:disabled):hover {
		background: #047857;
	}
	.error {
		padding: 0.75rem 1rem;
		border: 1px solid #fca5a5;
		background: #fef2f2;
		color: #991b1b;
		border-radius: 8px;
	}
	.note {
		margin-top: 1rem;
	}
</style>
