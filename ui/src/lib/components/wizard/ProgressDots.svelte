<!--
	ProgressDots — top-of-wizard step indicator. Eight dots, one per
	step, with the current one filled in brand color and completed
	ones softly checked. Tapping a completed dot jumps back to that
	step (forward navigation requires Next, so users can't skip
	required fields).

	Mobile: dots shrink, label hides; on >sm the icon name appears
	under the active dot.
-->
<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { wizard, STEPS_ORDER, STEP_ICONS, type WizardStep } from '../../../routes/setup/wizard.svelte';

	function dotClickable(target: WizardStep): boolean {
		// Allow jumping back to any earlier step. Forward jumps not
		// allowed — the user has to satisfy each step's checks.
		return STEPS_ORDER.indexOf(target) < wizard.index;
	}

	function clickDot(target: WizardStep) {
		if (dotClickable(target)) wizard.go(target);
	}
</script>

<div class="w-full max-w-3xl mx-auto px-4 pt-6 pb-2">
	<div class="flex items-center justify-between gap-1">
		{#each STEPS_ORDER as s, i}
			{@const idx = wizard.index}
			{@const done = i < idx}
			{@const active = i === idx}
			<div class="flex items-center flex-1 last:flex-none">
				<button
					type="button"
					disabled={!dotClickable(s)}
					onclick={() => clickDot(s)}
					class="
						relative flex flex-col items-center gap-1.5 group
						{active ? 'cursor-default' : ''}
						{done ? 'cursor-pointer' : ''}
					"
					title={$_(`setup.step.${s}.label`)}
					aria-label={$_(`setup.step.${s}.label`)}
				>
					<div
						class="
							w-8 h-8 sm:w-9 sm:h-9 rounded-full flex items-center justify-center
							transition-all duration-200
							{active
								? 'bg-brand-500 text-white shadow-md ring-2 ring-brand-200 dark:ring-brand-500/30 scale-110'
								: done
									? 'bg-emerald-500 text-white'
									: 'bg-zinc-200 dark:bg-zinc-700 text-zinc-400 dark:text-zinc-500'}
						"
					>
						{#if done}
							<i class="bi bi-check2 text-base"></i>
						{:else}
							<i class="bi {STEP_ICONS[s]} text-sm"></i>
						{/if}
					</div>
					{#if active}
						<span class="hidden sm:block text-xs font-medium text-brand-600 dark:text-brand-400 absolute top-full mt-1 whitespace-nowrap">
							{$_(`setup.step.${s}.label`)}
						</span>
					{/if}
				</button>
				{#if i < STEPS_ORDER.length - 1}
					<div
						class="
							flex-1 h-0.5 mx-1 sm:mx-2 rounded-full transition-colors
							{i < idx ? 'bg-emerald-400 dark:bg-emerald-500' : 'bg-zinc-200 dark:bg-zinc-700'}
						"
					></div>
				{/if}
			</div>
		{/each}
	</div>
</div>
