<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { onMount } from 'svelte';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { apiGet, ApiTimeoutError } from '$lib/api';
	import type { CapabilityReport } from '$lib/types';
	import { wizard } from '../wizard.svelte';

	async function detect() {
		wizard.capLoading = true;
		wizard.capError = null;
		try {
			wizard.cap = await apiGet<CapabilityReport>('/setup/capability', { timeoutMs: 8000 });
		} catch (e) {
			wizard.cap = { pi: '', eth: [], guest_ap_capable: false, router_capable: false };
			if (e instanceof ApiTimeoutError) {
				wizard.capError = $_('setup.detect_timeout');
			} else if (e instanceof Error) {
				wizard.capError = e.message;
			}
		} finally {
			wizard.capLoading = false;
		}
	}

	onMount(() => {
		if (!wizard.cap && !wizard.capLoading) detect();
		if (!wizard.cap && wizard.capLoading) detect();
	});

	const ethCount = $derived(wizard.cap?.eth.filter((e) => e.link).length ?? 0);
	const ethDetected = $derived(wizard.cap?.eth.length ?? 0);
	const piModel = $derived(wizard.cap?.pi_model_string || wizard.cap?.pi || $_('setup.hw.unknown_pi'));
</script>

<StepCard
	title={$_('setup.hw.title')}
	subtitle={$_('setup.hw.subtitle')}
	canNext={!wizard.capLoading}
>
	{#if wizard.capLoading}
		<div class="py-8 text-center">
			<div class="spinner mx-auto"></div>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-3">{$_('setup.hw.detecting')}</p>
		</div>
	{:else}
		<!-- Pi model card -->
		<div class="flex items-center gap-3 p-4 rounded-lg bg-brand-50 dark:bg-brand-500/10 border border-brand-100 dark:border-brand-500/20">
			<div class="w-12 h-12 rounded-full bg-white dark:bg-zinc-800 flex items-center justify-center shrink-0">
				<i class="bi bi-cpu text-brand-600 dark:text-brand-400 text-xl"></i>
			</div>
			<div class="flex-1 min-w-0">
				<div class="text-xs text-zinc-500 dark:text-zinc-400">{$_('setup.hw.pi_label')}</div>
				<div class="font-semibold text-zinc-900 dark:text-zinc-100 truncate">{piModel}</div>
			</div>
		</div>

		<!-- Ethernet status -->
		<div class="space-y-2">
			<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
				{$_('setup.hw.ethernet')}
			</div>
			{#if ethDetected === 0}
				<div class="p-3 rounded-md bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30 text-sm text-amber-800 dark:text-amber-300">
					<i class="bi bi-info-circle mr-1.5"></i>
					{$_('setup.hw.no_eth')}
				</div>
			{:else}
				<ul class="space-y-1.5">
					{#each wizard.cap?.eth ?? [] as e}
						<li class="flex items-center gap-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50 border border-zinc-200 dark:border-zinc-800 text-sm">
							<i
								class="bi {e.link ? 'bi-ethernet text-emerald-500' : 'bi-plug text-zinc-400'} text-lg"
							></i>
							<div class="flex-1 min-w-0">
								<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">{e.name}</div>
								<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate">{e.model}</div>
							</div>
							{#if e.link}
								<span class="badge badge-ok text-[10px]">
									<span class="dot-live"></span>
									{$_('setup.hw.cable_in')}
								</span>
							{:else}
								<span class="text-xs text-zinc-400 italic">{$_('setup.hw.cable_out')}</span>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</div>

		<!-- Wi-Fi -->
		<div class="space-y-2">
			<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">{$_('setup.hw.wifi')}</div>
			<div class="flex items-center gap-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50 border border-zinc-200 dark:border-zinc-800 text-sm">
				<i class="bi bi-wifi text-emerald-500 text-lg"></i>
				<div class="flex-1">
					<div class="font-mono text-xs text-zinc-700 dark:text-zinc-300">wlan0</div>
					<div class="text-xs text-zinc-500 dark:text-zinc-400">{$_('setup.hw.wifi_ok')}</div>
				</div>
				<span class="badge badge-ok text-[10px]">{$_('setup.hw.ready')}</span>
			</div>
		</div>

		<!-- Recommendation -->
		<div class="mt-2 p-4 rounded-lg bg-zinc-50 dark:bg-zinc-900/50 border border-zinc-200 dark:border-zinc-800">
			<div class="text-sm">
				{#if wizard.cap?.router_capable}
					{#if ethCount > 0}
						<i class="bi bi-stars text-brand-500 mr-1"></i>
						<span class="font-medium">{$_('setup.hw.rec_router_ready')}</span>
					{:else}
						<i class="bi bi-info-circle text-amber-500 mr-1"></i>
						<span>{$_('setup.hw.rec_router_no_cable')}</span>
					{/if}
				{:else}
					<i class="bi bi-info-circle text-brand-500 mr-1"></i>
					<span>{$_('setup.hw.rec_extender_only')}</span>
				{/if}
			</div>
		</div>

		{#if wizard.capError}
			<div class="text-xs text-amber-600 dark:text-amber-400">
				<i class="bi bi-exclamation-triangle mr-1"></i>
				{wizard.capError}
				<button type="button" class="underline ml-2" onclick={detect}>
					{$_('setup.hw.retry')}
				</button>
			</div>
		{:else}
			<button type="button" class="text-xs text-brand-600 dark:text-brand-400 underline" onclick={detect}>
				<i class="bi bi-arrow-clockwise mr-1"></i>{$_('setup.hw.rescan')}
			</button>
		{/if}
	{/if}
</StepCard>
