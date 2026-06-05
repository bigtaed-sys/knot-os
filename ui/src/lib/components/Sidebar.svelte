<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { page } from '$app/stores';
	import type { Plugin } from '$lib/types';

	interface NavItem {
		path: string;
		label: string;
		icon: string;
		external?: boolean;
	}

	let {
		plugins = [],
		version = '',
		open = $bindable(false)
	}: {
		plugins?: Plugin[];
		version?: string;
		open?: boolean;
	} = $props();

	const coreItems: NavItem[] = [
		{ path: '/', icon: 'bi-speedometer2', label: 'nav.dashboard' },
		{ path: '/devices', icon: 'bi-hdd-network', label: 'nav.devices' },
		{ path: '/profiles', icon: 'bi-shield-check', label: 'nav.profiles' },
		{ path: '/protection', icon: 'bi-shield-fill-check', label: 'nav.protection' },
		{ path: '/routing', icon: 'bi-globe-americas', label: 'nav.routing' },
		{ path: '/vpn', icon: 'bi-key', label: 'nav.vpn' },
		{ path: '/portforwards', icon: 'bi-box-arrow-in-right', label: 'nav.portforward' },
		{ path: '/zapret', icon: 'bi-magic', label: 'nav.zapret' },
		{ path: '/modem', icon: 'bi-sim', label: 'nav.modem' },
		{ path: '/guests', icon: 'bi-people', label: 'nav.guests' },
		{ path: '/plugins', icon: 'bi-puzzle', label: 'nav.plugins' },
		{ path: '/system', icon: 'bi-gear', label: 'nav.system' }
	];

	function isActive(itemPath: string, currentPath: string): boolean {
		if (itemPath === '/') return currentPath === '/';
		return currentPath === itemPath || currentPath.startsWith(itemPath + '/');
	}

	function pluginMenuItems(p: Plugin): NavItem[] {
		if (!p.enabled || !p.menu || p.menu.length === 0) return [];
		return p.menu.map((m) => ({
			path: m.path,
			icon: m.icon || 'bi-plugin',
			label: m.label
		}));
	}

	const enabledPluginsWithMenu = $derived.by(() =>
		plugins.filter((p) => p.enabled && p.menu && p.menu.length > 0)
	);
</script>

<!-- Mobile backdrop -->
{#if open}
	<button
		type="button"
		class="lg:hidden fixed inset-0 z-30 bg-black/40"
		aria-label="Close menu"
		onclick={() => (open = false)}
	></button>
{/if}

<aside
	class="
		fixed lg:sticky top-0 left-0 z-40
		h-screen w-64 shrink-0
		bg-white dark:bg-zinc-900
		border-r border-zinc-200 dark:border-zinc-800
		transition-transform
		{open ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'}
	"
>
	<div class="flex flex-col h-full">
		<!-- Logo / brand -->
		<div class="flex items-center gap-2 h-16 px-5 border-b border-zinc-200 dark:border-zinc-800">
			<div
				class="w-8 h-8 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center shadow-sm"
			>
				<i class="bi bi-diagram-3-fill text-white text-base"></i>
			</div>
			<div class="flex-1 min-w-0">
				<div class="font-semibold text-zinc-900 dark:text-zinc-100 truncate">
					{$_('app.name')}
				</div>
				<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate">
					{$_('app.tagline')}
				</div>
			</div>
		</div>

		<!-- Nav -->
		<nav class="flex-1 px-3 py-4 space-y-1 overflow-y-auto">
			{#each coreItems as item}
				<a
					href={item.path}
					class="nav-item"
					class:active={isActive(item.path, $page.url.pathname)}
					onclick={() => (open = false)}
				>
					<i class="bi {item.icon} text-base w-5 text-center"></i>
					<span>{$_(item.label)}</span>
				</a>
			{/each}

			{#if enabledPluginsWithMenu.length > 0}
				<div
					class="pt-4 pb-1.5 px-3 text-xs font-semibold uppercase tracking-wider text-zinc-400 dark:text-zinc-500"
				>
					{$_('nav.plugins')}
				</div>
				{#each enabledPluginsWithMenu as plugin}
					{#each pluginMenuItems(plugin) as item}
						<a
							href={item.path}
							class="nav-item"
							class:active={isActive(item.path, $page.url.pathname)}
							onclick={() => (open = false)}
						>
							<i class="bi {item.icon} text-base w-5 text-center"></i>
							<span>{item.label}</span>
						</a>
					{/each}
				{/each}
			{/if}
		</nav>

		<!-- Footer: version + project link -->
		<div class="p-3 border-t border-zinc-200 dark:border-zinc-800 text-xs text-zinc-500 dark:text-zinc-400">
			<div>{version ? 'v' + version : ''}</div>
		</div>
	</div>
</aside>
