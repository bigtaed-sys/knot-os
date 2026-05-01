<script lang="ts">
	import { _, locale } from 'svelte-i18n';
	import { theme, toggleTheme, type Theme } from '$lib/theme';
	import { setLocale, SUPPORTED_LOCALES, type Locale } from '$lib/i18n';
	import { goto } from '$app/navigation';
	import { apiPost } from '$lib/api';

	let { onMenuClick = () => {} }: { onMenuClick?: () => void } = $props();

	let langOpen = $state(false);
	let currentTheme = $state<Theme>('light');
	theme.subscribe((t) => (currentTheme = t));

	const labels: Record<Locale, string> = { ru: 'Русский', en: 'English' };
	const flags: Record<Locale, string> = { ru: '🇷🇺', en: '🇬🇧' };

	async function logout() {
		try {
			await apiPost('/auth/logout');
		} catch {
			// ignore
		}
		goto('/login', { replaceState: true });
	}

	function pickLocale(loc: Locale) {
		setLocale(loc);
		langOpen = false;
	}

	function onClickOutside(e: MouseEvent) {
		const t = e.target as HTMLElement;
		if (!t.closest('[data-lang-menu]')) langOpen = false;
	}
</script>

<svelte:window onclick={onClickOutside} />

<header
	class="
		sticky top-0 z-20
		h-16 px-4 lg:px-6
		flex items-center gap-3
		bg-white/80 dark:bg-zinc-900/80 backdrop-blur
		border-b border-zinc-200 dark:border-zinc-800
	"
>
	<button
		type="button"
		class="lg:hidden p-2 -ml-2 rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800"
		aria-label="Open menu"
		onclick={onMenuClick}
	>
		<i class="bi bi-list text-xl"></i>
	</button>

	<div class="flex-1"></div>

	<!-- Language picker -->
	<div class="relative" data-lang-menu>
		<button
			type="button"
			class="flex items-center gap-2 px-3 py-1.5 rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800 text-sm"
			onclick={() => (langOpen = !langOpen)}
			title={$_('nav.language')}
		>
			<span>{flags[($locale as Locale) ?? 'ru']}</span>
			<span class="hidden sm:inline">{labels[($locale as Locale) ?? 'ru']}</span>
			<i class="bi bi-chevron-down text-xs opacity-60"></i>
		</button>
		{#if langOpen}
			<div
				class="absolute right-0 mt-2 w-44 surface py-1 shadow-lg"
				role="menu"
			>
				{#each SUPPORTED_LOCALES as loc}
					<button
						type="button"
						class="
							w-full flex items-center gap-3 px-3 py-2 text-sm
							hover:bg-zinc-100 dark:hover:bg-zinc-800
							{$locale === loc ? 'text-brand-600 dark:text-brand-400 font-medium' : ''}
						"
						onclick={() => pickLocale(loc)}
					>
						<span>{flags[loc]}</span>
						<span>{labels[loc]}</span>
						{#if $locale === loc}
							<i class="bi bi-check-lg ml-auto"></i>
						{/if}
					</button>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Theme toggle -->
	<button
		type="button"
		class="p-2 rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800"
		onclick={toggleTheme}
		aria-label={currentTheme === 'dark' ? $_('nav.theme_light') : $_('nav.theme_dark')}
		title={currentTheme === 'dark' ? $_('nav.theme_light') : $_('nav.theme_dark')}
	>
		{#if currentTheme === 'dark'}
			<i class="bi bi-sun text-lg"></i>
		{:else}
			<i class="bi bi-moon-stars text-lg"></i>
		{/if}
	</button>

	<!-- Logout -->
	<button
		type="button"
		class="p-2 rounded-lg hover:bg-zinc-100 dark:hover:bg-zinc-800"
		onclick={logout}
		title={$_('nav.logout')}
		aria-label={$_('nav.logout')}
	>
		<i class="bi bi-box-arrow-right text-lg"></i>
	</button>
</header>
