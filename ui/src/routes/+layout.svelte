<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { apiGet, apiPost, ApiError } from '$lib/api';
	import type { SystemStatus } from '$lib/types';

	let { children } = $props();

	let status = $state<SystemStatus | null>(null);
	let bootError = $state<string | null>(null);

	async function loadStatus() {
		try {
			status = await apiGet<SystemStatus>('/status');
		} catch (e) {
			bootError = e instanceof Error ? e.message : String(e);
		}
	}

	// Route the user to the correct screen on every status change.
	$effect(() => {
		if (!status) return;
		const path = $page.url.pathname;

		if (status.role === 'setup') {
			if (!path.startsWith('/setup')) goto('/setup', { replaceState: true });
			return;
		}

		// role !== 'setup' — needs auth.
		if (path.startsWith('/setup')) {
			goto('/', { replaceState: true });
			return;
		}
	});

	async function logout() {
		try {
			await apiPost('/auth/logout');
		} catch {
			// ignore — we're going to the login screen anyway
		}
		goto('/login', { replaceState: true });
	}

	onMount(loadStatus);

	const showHeader = $derived(status?.role === 'wifi-extender');
</script>

<div class="app">
	{#if showHeader}
		<header>
			<a href="/" class="brand">KnotOS</a>
			<nav>
				<a href="/" class="link">Dashboard</a>
				<a href="/plugins" class="link">Plugins</a>
				<button class="link" onclick={logout}>Logout</button>
			</nav>
		</header>
	{/if}

	<main>
		{#if bootError}
			<div class="error">
				Could not reach knotd: <code>{bootError}</code>
			</div>
		{:else}
			{@render children()}
		{/if}
	</main>

	<footer>
		<small>KnotOS v0.0.0-dev — pre-alpha</small>
	</footer>
</div>

<style>
	.app {
		display: flex;
		flex-direction: column;
		min-height: 100vh;
		font-family: system-ui, -apple-system, sans-serif;
	}
	header {
		display: flex;
		justify-content: space-between;
		align-items: center;
		padding: 1rem 1.5rem;
		border-bottom: 1px solid #e5e7eb;
	}
	.brand {
		font-weight: 700;
		font-size: 1.25rem;
		text-decoration: none;
		color: inherit;
	}
	nav {
		display: flex;
		gap: 1rem;
	}
	.link {
		background: none;
		border: none;
		color: #2563eb;
		cursor: pointer;
		font: inherit;
		padding: 0;
	}
	.link:hover {
		color: #1d4ed8;
		text-decoration: underline;
	}
	main {
		flex: 1;
		padding: 2rem 1.5rem;
		max-width: 720px;
		width: 100%;
		margin: 0 auto;
	}
	footer {
		padding: 1rem 1.5rem;
		border-top: 1px solid #e5e7eb;
		color: #6b7280;
		text-align: center;
	}
	.error {
		padding: 1rem;
		border: 1px solid #fca5a5;
		background: #fef2f2;
		border-radius: 8px;
		color: #991b1b;
	}
</style>
