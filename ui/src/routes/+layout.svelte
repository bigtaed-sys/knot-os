<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { _, isLoading, waitLocale } from 'svelte-i18n';
	import { initI18n } from '$lib/i18n';
	import { apiGet, ApiError } from '$lib/api';
	import type { SystemStatus, Plugin, PluginsResponse } from '$lib/types';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import Topbar from '$lib/components/Topbar.svelte';

	initI18n();

	let { children } = $props();

	let status = $state<SystemStatus | null>(null);
	let plugins = $state<Plugin[]>([]);
	let bootError = $state<string | null>(null);
	let i18nReady = $state(false);
	let sidebarOpen = $state(false);

	async function loadStatus() {
		try {
			status = await apiGet<SystemStatus>('/status');
		} catch (e) {
			bootError = e instanceof Error ? e.message : String(e);
		}
	}

	async function loadPlugins() {
		try {
			const r = await apiGet<PluginsResponse>('/plugins');
			plugins = r.plugins;
		} catch {
			// 401 expected on /login & /setup; ignore — sidebar just won't show plugin items.
			plugins = [];
		}
	}

	const isSetup = $derived(status?.role === 'setup');
	const isLoginRoute = $derived($page.url.pathname.startsWith('/login'));
	const isSetupRoute = $derived($page.url.pathname.startsWith('/setup'));
	const isAuthRoute = $derived(isLoginRoute || isSetupRoute);

	// Route guard:
	//  - role=setup  → push everyone into /setup
	//  - role=*      → /login if unauth (handled by 401 redirects in pages)
	$effect(() => {
		if (!status) return;
		const path = $page.url.pathname;
		if (status.role === 'setup' && !path.startsWith('/setup')) {
			goto('/setup', { replaceState: true });
		} else if (status.role !== 'setup' && path.startsWith('/setup')) {
			goto('/', { replaceState: true });
		}
	});

	// Refresh plugins when the layout-level pages change (login -> /).
	$effect(() => {
		const path = $page.url.pathname;
		if (path !== '/login' && path !== '/setup') {
			loadPlugins();
		}
	});

	onMount(async () => {
		await waitLocale();
		i18nReady = true;
		await loadStatus();
	});
</script>

<svelte:head>
	<title>KnotOS</title>
</svelte:head>

{#if !i18nReady || $isLoading}
	<!-- Brief flash before i18n loads. Keep it minimal so it doesn't strobe. -->
	<div class="min-h-screen flex items-center justify-center text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else if bootError}
	<div class="min-h-screen flex items-center justify-center p-6">
		<div class="surface max-w-md w-full p-6 text-center">
			<div class="w-12 h-12 mx-auto rounded-full bg-red-100 dark:bg-red-500/15 flex items-center justify-center mb-3">
				<i class="bi bi-exclamation-triangle text-red-600 dark:text-red-400 text-xl"></i>
			</div>
			<h1 class="font-semibold mb-1">{$_('dashboard.error')}</h1>
			<p class="text-sm text-zinc-500 font-mono break-all">{bootError}</p>
		</div>
	</div>
{:else if isAuthRoute}
	<!-- Login & setup pages: full-bleed, no sidebar / topbar. -->
	{@render children()}
{:else}
	<div class="min-h-screen flex">
		<Sidebar {plugins} version={status?.version ?? ''} bind:open={sidebarOpen} />
		<div class="flex-1 flex flex-col min-w-0">
			<Topbar onMenuClick={() => (sidebarOpen = true)} />
			<main class="flex-1 p-4 lg:p-8">
				<div class="max-w-5xl mx-auto w-full">
					{@render children()}
				</div>
			</main>
		</div>
	</div>
{/if}
