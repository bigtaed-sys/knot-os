<script lang="ts">
	import { _ } from 'svelte-i18n';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { wizard } from '../wizard.svelte';
	import type { Role } from '$lib/types';

	const routerCapable = $derived(wizard.cap?.router_capable === true);
	const ethCount = $derived(wizard.cap?.eth.filter((e) => e.link).length ?? 0);

	function pick(role: Role) {
		// Don't let user pick router on a non-capable Pi.
		if (role === 'wifi-router' && !routerCapable) return;
		wizard.role = role;
	}

	let showHelp = $state(false);
</script>

<StepCard
	title={$_('setup.role.title')}
	subtitle={$_('setup.role.subtitle')}
>
	<!-- Router card -->
	<button
		type="button"
		onclick={() => pick('wifi-router')}
		disabled={!routerCapable}
		class="
			block w-full text-left p-5 rounded-xl border-2 transition-all
			{wizard.role === 'wifi-router' && routerCapable
				? 'border-brand-500 bg-brand-50/50 dark:bg-brand-500/10 shadow-md'
				: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}
			disabled:opacity-50 disabled:cursor-not-allowed
		"
	>
		<div class="flex items-start gap-3 mb-3">
			<div class="
				w-12 h-12 rounded-full flex items-center justify-center shrink-0
				{wizard.role === 'wifi-router' && routerCapable
					? 'bg-brand-500 text-white'
					: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-500'}
			">
				<i class="bi bi-router text-xl"></i>
			</div>
			<div class="flex-1 min-w-0">
				<h3 class="font-semibold text-base">{$_('setup.role.router_title')}</h3>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-0.5">
					{$_('setup.role.router_desc')}
				</p>
			</div>
			{#if wizard.role === 'wifi-router' && routerCapable}
				<i class="bi bi-check-circle-fill text-brand-500 text-xl"></i>
			{/if}
		</div>
		<!-- Diagram: ISP cable → Pi → devices -->
		<div class="font-mono text-[10px] sm:text-xs bg-zinc-50 dark:bg-zinc-900 rounded-md p-3 text-zinc-600 dark:text-zinc-400 overflow-x-auto">
			<div class="flex items-center gap-2 whitespace-nowrap">
				<span class="text-zinc-500">🌐 {$_('setup.role.internet')}</span>
				<span class="text-emerald-500">━━ {$_('setup.role.cable')} ━━</span>
				<span class="text-brand-500">📡 Pi</span>
				<span class="text-emerald-500">⋯ Wi-Fi ⋯</span>
				<span class="text-zinc-500">📱💻 {$_('setup.role.your_devices')}</span>
			</div>
		</div>
		{#if !routerCapable}
			<p class="mt-3 text-xs text-amber-600 dark:text-amber-400">
				<i class="bi bi-exclamation-triangle mr-1"></i>
				{$_('setup.role.no_router_reason')}
			</p>
		{:else if ethCount === 0}
			<p class="mt-3 text-xs text-amber-600 dark:text-amber-400">
				<i class="bi bi-info-circle mr-1"></i>
				{$_('setup.role.cable_warning')}
			</p>
		{/if}
	</button>

	<!-- Extender card -->
	<button
		type="button"
		onclick={() => pick('wifi-extender')}
		class="
			block w-full text-left p-5 rounded-xl border-2 transition-all
			{wizard.role === 'wifi-extender'
				? 'border-brand-500 bg-brand-50/50 dark:bg-brand-500/10 shadow-md'
				: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300 dark:hover:border-zinc-600'}
		"
	>
		<div class="flex items-start gap-3 mb-3">
			<div class="
				w-12 h-12 rounded-full flex items-center justify-center shrink-0
				{wizard.role === 'wifi-extender'
					? 'bg-brand-500 text-white'
					: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-500'}
			">
				<i class="bi bi-broadcast text-xl"></i>
			</div>
			<div class="flex-1 min-w-0">
				<h3 class="font-semibold text-base">{$_('setup.role.extender_title')}</h3>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-0.5">
					{$_('setup.role.extender_desc')}
				</p>
			</div>
			{#if wizard.role === 'wifi-extender'}
				<i class="bi bi-check-circle-fill text-brand-500 text-xl"></i>
			{/if}
		</div>
		<div class="font-mono text-[10px] sm:text-xs bg-zinc-50 dark:bg-zinc-900 rounded-md p-3 text-zinc-600 dark:text-zinc-400 overflow-x-auto">
			<div class="flex items-center gap-2 whitespace-nowrap">
				<span class="text-zinc-500">📡 {$_('setup.role.isp_router')}</span>
				<span class="text-emerald-500">⋯ Wi-Fi ⋯</span>
				<span class="text-brand-500">📡 Pi</span>
				<span class="text-emerald-500">⋯ Wi-Fi ⋯</span>
				<span class="text-zinc-500">📱💻 {$_('setup.role.your_devices')}</span>
			</div>
		</div>
	</button>

	<!-- Help expander -->
	<div>
		<button
			type="button"
			onclick={() => (showHelp = !showHelp)}
			class="text-sm text-brand-600 dark:text-brand-400 hover:underline"
		>
			<i class="bi bi-question-circle mr-1"></i>
			{$_('setup.role.help_toggle')}
			<i class="bi {showHelp ? 'bi-chevron-up' : 'bi-chevron-down'} text-xs ml-0.5"></i>
		</button>
		{#if showHelp}
			<div class="mt-3 p-4 rounded-md bg-brand-50/40 dark:bg-brand-500/5 border border-brand-100/50 dark:border-brand-500/20 text-sm text-zinc-700 dark:text-zinc-300 leading-relaxed">
				<p class="mb-2">{$_('setup.role.help_q1')}</p>
				<ul class="list-disc pl-5 space-y-1 text-xs">
					<li>{$_('setup.role.help_a1')}</li>
					<li>{$_('setup.role.help_a2')}</li>
					<li>{$_('setup.role.help_a3')}</li>
				</ul>
			</div>
		{/if}
	</div>
</StepCard>
