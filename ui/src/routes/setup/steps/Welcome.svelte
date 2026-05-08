<script lang="ts">
	import { _ } from 'svelte-i18n';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { STEPS_ORDER, STEP_ICONS } from '../wizard.svelte';
</script>

<StepCard
	title={$_('setup.welcome.title')}
	subtitle={$_('setup.welcome.subtitle')}
	hideBack
	nextLabel={$_('setup.welcome.start')}
>
	<!-- Friendly hero: animated Pi with glowing connection points -->
	<div class="relative my-2 flex justify-center">
		<svg viewBox="0 0 220 140" class="w-56 sm:w-64" aria-hidden="true">
			<!-- Pi board -->
			<rect x="10" y="20" width="200" height="100" rx="6"
				class="fill-emerald-700 dark:fill-emerald-800" />
			<!-- CPU -->
			<rect x="80" y="55" width="40" height="40" rx="2"
				class="fill-zinc-800" />
			<text x="100" y="80" text-anchor="middle"
				class="fill-zinc-300 text-[8px] font-mono">SoC</text>
			<!-- HDMI / USB blocks -->
			<rect x="10" y="40" width="14" height="20" class="fill-zinc-300" />
			<rect x="10" y="80" width="14" height="20" class="fill-zinc-300" />
			<!-- Ethernet jack on right -->
			<rect x="186" y="60" width="24" height="22" class="fill-zinc-300" />
			<!-- "Power" LED, pulsing -->
			<circle cx="160" cy="110" r="3" class="fill-red-500">
				<animate attributeName="opacity" values="1;0.3;1" dur="1.6s" repeatCount="indefinite" />
			</circle>
			<!-- "Network" LED, pulsing different rhythm -->
			<circle cx="170" cy="110" r="3" class="fill-emerald-300">
				<animate attributeName="opacity" values="0.4;1;0.4" dur="0.9s" repeatCount="indefinite" />
			</circle>
			<!-- Antenna ripples (Wi-Fi) -->
			<g class="stroke-brand-500" stroke-width="1.4" fill="none" opacity="0.7">
				<path d="M 195 22 a 10 10 0 0 1 14 0">
					<animate attributeName="opacity" values="0.2;0.9;0.2" dur="2s" repeatCount="indefinite" />
				</path>
				<path d="M 191 18 a 16 16 0 0 1 22 0">
					<animate attributeName="opacity" values="0.1;0.6;0.1" dur="2s" repeatCount="indefinite" begin="0.3s" />
				</path>
				<path d="M 187 14 a 22 22 0 0 1 30 0">
					<animate attributeName="opacity" values="0.05;0.4;0.05" dur="2s" repeatCount="indefinite" begin="0.6s" />
				</path>
			</g>
		</svg>
	</div>

	<!-- What we'll cover -->
	<div class="bg-zinc-50 dark:bg-zinc-900/50 rounded-lg p-4 sm:p-5 border border-zinc-200 dark:border-zinc-800">
		<div class="text-sm font-medium text-zinc-700 dark:text-zinc-200 mb-3">
			{$_('setup.welcome.agenda_title')}
		</div>
		<ol class="space-y-2 text-sm text-zinc-600 dark:text-zinc-300">
			{#each STEPS_ORDER.slice(1, -1) as s}
				<li class="flex items-center gap-2">
					<span class="w-6 h-6 rounded-full bg-brand-100 dark:bg-brand-500/20 flex items-center justify-center shrink-0">
						<i class="bi {STEP_ICONS[s]} text-xs text-brand-600 dark:text-brand-400"></i>
					</span>
					<span>{$_(`setup.step.${s}.label`)}</span>
				</li>
			{/each}
		</ol>
	</div>

	<p class="text-xs text-zinc-500 dark:text-zinc-400 text-center">
		⏱ {$_('setup.welcome.estimated_time')}
	</p>
</StepCard>
