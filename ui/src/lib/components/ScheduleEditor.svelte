<script lang="ts">
	import { _ } from 'svelte-i18n';
	import type { BlockWindow } from '$lib/types';

	let {
		windows = $bindable([]),
		disabled = false
	}: {
		windows?: BlockWindow[];
		disabled?: boolean;
	} = $props();

	const dayOrder = [1, 2, 3, 4, 5, 6, 0]; // Mon..Sun for visual order

	function toggleDay(idx: number, day: number) {
		const w = windows[idx];
		const set = new Set(w.days);
		if (set.has(day)) set.delete(day);
		else set.add(day);
		w.days = Array.from(set).sort((a, b) => a - b);
		windows = [...windows]; // trigger reactivity
	}

	function setStart(idx: number, v: string) {
		windows[idx].start = v;
		windows = [...windows];
	}
	function setEnd(idx: number, v: string) {
		windows[idx].end = v;
		windows = [...windows];
	}

	function add() {
		windows = [...windows, { days: [1, 2, 3, 4, 5], start: '22:00', end: '07:00' }];
	}

	function remove(idx: number) {
		windows = windows.filter((_, i) => i !== idx);
	}

	function crossesMidnight(w: BlockWindow): boolean {
		return w.start > w.end;
	}
</script>

<div class="space-y-3">
	{#if windows.length === 0}
		<p class="text-sm text-zinc-500 dark:text-zinc-400 italic">{$_('profiles.no_schedule')}</p>
	{:else}
		{#each windows as w, idx}
			<div class="surface-muted p-3 space-y-3">
				<!-- Days -->
				<div>
					<div class="text-xs text-zinc-500 dark:text-zinc-400 mb-1.5">
						{$_('profiles.window_days')}
					</div>
					<div class="flex flex-wrap gap-1.5">
						{#each dayOrder as day}
							<button
								type="button"
								class="
									px-2.5 py-1 rounded-full text-xs font-medium border transition-colors
									{w.days.includes(day)
										? 'bg-brand-600 text-white border-brand-600'
										: 'bg-white dark:bg-zinc-900 text-zinc-600 dark:text-zinc-300 border-zinc-300 dark:border-zinc-700 hover:border-brand-400'}
								"
								disabled={disabled}
								onclick={() => toggleDay(idx, day)}
							>
								{$_(`profiles.day_short_${day}`)}
							</button>
						{/each}
					</div>
				</div>

				<!-- Times -->
				<div class="flex items-end gap-2 flex-wrap">
					<div>
						<div class="text-xs text-zinc-500 dark:text-zinc-400 mb-1">
							{$_('profiles.window_start')}
						</div>
						<input
							type="time"
							class="input w-28"
							value={w.start}
							{disabled}
							oninput={(e) => setStart(idx, (e.currentTarget as HTMLInputElement).value)}
						/>
					</div>
					<i class="bi bi-arrow-right text-zinc-400 mb-2"></i>
					<div>
						<div class="text-xs text-zinc-500 dark:text-zinc-400 mb-1">
							{$_('profiles.window_end')}
						</div>
						<input
							type="time"
							class="input w-28"
							value={w.end}
							{disabled}
							oninput={(e) => setEnd(idx, (e.currentTarget as HTMLInputElement).value)}
						/>
					</div>
					{#if crossesMidnight(w)}
						<span class="badge badge-info mb-2">
							<i class="bi bi-moon"></i>
							{$_('profiles.window_help_cross')}
						</span>
					{/if}
					<div class="ml-auto">
						<button type="button" class="btn-ghost text-sm" disabled={disabled} onclick={() => remove(idx)}>
							<i class="bi bi-trash"></i>
							{$_('profiles.window_remove')}
						</button>
					</div>
				</div>
			</div>
		{/each}
	{/if}

	<button type="button" class="btn-ghost w-full" disabled={disabled} onclick={add}>
		<i class="bi bi-plus-circle"></i>
		{$_('profiles.add_window')}
	</button>
</div>
