<!--
	InlineHelp — small `?` button next to a field that, on click,
	expands an explanation paragraph below. Used liberally in the
	wizard to demystify every choice.

	Stays collapsed by default to keep the form scannable; users who
	already know what they're doing don't have to see the prose.
-->
<script lang="ts">
	let { text }: { text: string } = $props();

	let open = $state(false);
</script>

<div class="inline-flex items-center">
	<button
		type="button"
		onclick={() => (open = !open)}
		class="
			w-5 h-5 rounded-full text-xs flex items-center justify-center
			bg-zinc-200 dark:bg-zinc-700 text-zinc-600 dark:text-zinc-300
			hover:bg-brand-100 hover:text-brand-700 dark:hover:bg-brand-500/20 dark:hover:text-brand-300
			transition-colors
		"
		aria-label="Toggle help"
		aria-expanded={open}
	>
		?
	</button>
</div>

{#if open}
	<div
		class="
			mt-2 p-3 rounded-md
			bg-brand-50 dark:bg-brand-500/10 border border-brand-100 dark:border-brand-500/20
			text-xs text-zinc-700 dark:text-zinc-300 leading-relaxed
		"
	>
		<i class="bi bi-info-circle text-brand-500 mr-1.5"></i>
		{text}
	</div>
{/if}
