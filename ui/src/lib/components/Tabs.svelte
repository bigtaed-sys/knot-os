<script lang="ts">
	// Segmented in-page sub-navigation. Breaks a long page into short
	// tabbed sections instead of one endless stack of cards. Scrolls
	// horizontally on narrow screens so the tab row never overflows the
	// viewport.
	interface Tab {
		id: string;
		label: string;
		icon?: string;
	}

	let { tabs, active = $bindable() }: { tabs: Tab[]; active: string } = $props();
</script>

<div class="tabs-row flex gap-1 mb-5 border-b border-zinc-200 dark:border-zinc-800">
	{#each tabs as t (t.id)}
		<button
			type="button"
			onclick={() => (active = t.id)}
			class="shrink-0 whitespace-nowrap px-3.5 py-2 text-sm font-medium border-b-2 -mb-px transition-colors
				{active === t.id
				? 'border-brand-500 text-brand-600 dark:text-brand-400'
				: 'border-transparent text-zinc-500 dark:text-zinc-400 hover:text-zinc-800 dark:hover:text-zinc-200'}"
		>
			{#if t.icon}<i class="bi {t.icon} mr-1.5"></i>{/if}{t.label}
		</button>
	{/each}
</div>

<style>
	/* Scroll sideways only when the tabs genuinely overflow, with no
	   visible scrollbar (which otherwise stole a sliver of vertical
	   space and nudged the page). */
	.tabs-row {
		overflow-x: auto;
		scrollbar-width: none; /* Firefox */
	}
	.tabs-row::-webkit-scrollbar {
		display: none; /* WebKit */
	}
</style>
