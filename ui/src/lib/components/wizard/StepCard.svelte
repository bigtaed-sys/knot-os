<!--
	StepCard — consistent chrome around every wizard step. Card
	centered on the page (max-w-2xl on desktop, full-bleed on
	mobile), title + subtitle header, slot for body, sticky-ish
	footer with Back / Next buttons.

	Each step component slots its body in here so we don't repeat
	the title/footer plumbing eight times.
-->
<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { wizard } from '../../../routes/setup/wizard.svelte';
	import { fly } from 'svelte/transition';

	let {
		title,
		subtitle = '',
		canNext = true,
		nextLabel = '',
		hideBack = false,
		hideNext = false,
		onnext = undefined,
		children
	}: {
		title: string;
		subtitle?: string;
		canNext?: boolean;
		nextLabel?: string;
		hideBack?: boolean;
		hideNext?: boolean;
		onnext?: () => void | Promise<void>;
		children: any;
	} = $props();

	async function handleNext() {
		if (!canNext) return;
		if (onnext) {
			await onnext();
		} else {
			wizard.next();
		}
	}
</script>

<div class="flex-1 flex items-start justify-center w-full" in:fly={{ x: 24, duration: 220 }}>
	<div class="w-full max-w-2xl px-4 sm:px-6 py-6">
		<div class="surface p-6 sm:p-8">
			<header class="mb-6">
				<h1 class="text-2xl font-semibold text-zinc-900 dark:text-zinc-100">{title}</h1>
				{#if subtitle}
					<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1.5 leading-relaxed">{subtitle}</p>
				{/if}
			</header>

			<div class="space-y-5">
				{@render children()}
			</div>

			{#if !hideBack || !hideNext}
				<footer class="flex items-center justify-between gap-3 mt-8 pt-6 border-t border-zinc-200 dark:border-zinc-800">
					{#if !hideBack}
						<button type="button" class="btn-ghost" onclick={() => wizard.prev()}>
							<i class="bi bi-arrow-left"></i>
							{$_('setup.back')}
						</button>
					{:else}
						<div></div>
					{/if}
					{#if !hideNext}
						<button
							type="button"
							class="btn-primary"
							disabled={!canNext}
							onclick={handleNext}
						>
							{nextLabel || $_('setup.next')}
							<i class="bi bi-arrow-right"></i>
						</button>
					{/if}
				</footer>
			{/if}
		</div>
	</div>
</div>
