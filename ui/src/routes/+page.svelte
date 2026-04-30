<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { apiGet, ApiError } from '$lib/api';
	import type { SystemStatus } from '$lib/types';

	let status = $state<SystemStatus | null>(null);
	let error = $state<string | null>(null);
	let timer: ReturnType<typeof setInterval> | null = null;

	async function refresh() {
		try {
			// /api/status is public and returns the live network shape.
			// We probe a protected endpoint to detect "session expired"
			// and bounce to /login if we are on the dashboard without
			// a valid session.
			const [s] = await Promise.all([apiGet<SystemStatus>('/status'), apiGet('/auth/me')]);
			status = s;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			error = e instanceof ApiError ? `${e.message}` : String(e);
		}
	}

	onMount(() => {
		refresh();
		timer = setInterval(refresh, 5000);
	});

	onDestroy(() => {
		if (timer !== null) clearInterval(timer);
	});

	function roleLabel(role: string): string {
		if (role === 'setup') return 'Setup mode';
		if (role === 'wifi-extender') return 'Wi-Fi extender';
		return role;
	}
</script>

<h1>Dashboard</h1>

{#if error}
	<div class="error">
		Could not reach knotd: <code>{error}</code>
	</div>
{:else if status === null}
	<p class="muted">Loading…</p>
{:else}
	<section class="card">
		<dl>
			<dt>Device</dt>
			<dd>{status.device}</dd>

			<dt>Version</dt>
			<dd><code>{status.version}</code></dd>

			<dt>Role</dt>
			<dd>{roleLabel(status.role)}</dd>

			<dt>Backend</dt>
			<dd>
				<code>{status.network.backend}</code>
				{#if status.network.backend === 'mock'}
					<span class="badge">dev</span>
				{/if}
			</dd>
		</dl>
	</section>

	{#if status.network.uplink}
		<section class="card">
			<h2>Uplink (wlan0)</h2>
			<dl>
				<dt>SSID</dt>
				<dd>{status.network.uplink.ssid}</dd>

				<dt>State</dt>
				<dd>
					{#if status.network.uplink.connected}
						<span class="ok">connected</span>
					{:else}
						<span class="bad">disconnected</span>
					{/if}
				</dd>

				{#if status.network.uplink.rssi_dbm}
					<dt>RSSI</dt>
					<dd>{status.network.uplink.rssi_dbm} dBm</dd>
				{/if}
			</dl>
		</section>
	{/if}

	{#if status.network.ap}
		<section class="card">
			<h2>Broadcast (ap0)</h2>
			<dl>
				<dt>SSID</dt>
				<dd>{status.network.ap.ssid}</dd>

				<dt>State</dt>
				<dd>
					{#if status.network.ap.up}
						<span class="ok">up</span>
					{:else}
						<span class="bad">down</span>
					{/if}
				</dd>

				<dt>Clients</dt>
				<dd>{status.network.ap.clients}</dd>
			</dl>
		</section>
	{/if}
{/if}

<style>
	h1 {
		margin-top: 0;
	}
	.card {
		margin-top: 1rem;
		padding: 1rem 1.25rem;
		border: 1px solid #e5e7eb;
		border-radius: 8px;
		background: #f9fafb;
	}
	.card h2 {
		margin: 0 0 0.5rem 0;
		font-size: 1rem;
		color: #374151;
	}
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.25rem 1rem;
		margin: 0;
	}
	dt {
		color: #6b7280;
		font-weight: 500;
	}
	dd {
		margin: 0;
	}
	.error {
		padding: 1rem;
		border: 1px solid #fca5a5;
		background: #fef2f2;
		border-radius: 8px;
		color: #991b1b;
	}
	.muted {
		color: #6b7280;
	}
	.ok {
		color: #047857;
		font-weight: 500;
	}
	.bad {
		color: #b91c1c;
		font-weight: 500;
	}
	.badge {
		display: inline-block;
		margin-left: 0.5rem;
		padding: 0.05em 0.5em;
		font-size: 0.75rem;
		background: #fde68a;
		color: #78350f;
		border-radius: 999px;
	}
</style>
