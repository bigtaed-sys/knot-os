<script lang="ts">
	import { _ } from 'svelte-i18n';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { wizard } from '../wizard.svelte';
</script>

<StepCard
	title={$_('setup.review.title')}
	subtitle={$_('setup.review.subtitle')}
	nextLabel={$_('setup.review.confirm')}
	onnext={() => wizard.next()}
>
	<!-- Final-state visual -->
	<div class="font-mono text-[11px] sm:text-xs bg-zinc-50 dark:bg-zinc-900 rounded-lg p-4 text-zinc-600 dark:text-zinc-400 overflow-x-auto">
		{#if wizard.role === 'wifi-router'}
			<div class="flex items-center gap-2 whitespace-nowrap">
				<span>🌐 {$_('setup.role.internet')}</span>
				<span class="text-emerald-500">
					{wizard.wanMode === 'modem' ? '📶 SIM' : '━━ ' + (wizard.wanInterface || 'eth0') + ' ━━'}
				</span>
				<span class="text-brand-500">📡 {wizard.deviceName}</span>
				<span class="text-emerald-500">⋯ «{wizard.apSSID}» ⋯</span>
				<span>📱💻 {$_('setup.role.your_devices')}</span>
			</div>
		{:else}
			<div class="flex items-center gap-2 whitespace-nowrap">
				<span>📡 «{wizard.uplinkSSID}»</span>
				<span class="text-emerald-500">⋯ Wi-Fi ⋯</span>
				<span class="text-brand-500">📡 {wizard.deviceName}</span>
				<span class="text-emerald-500">⋯ «{wizard.apSSID}» ⋯</span>
				<span>📱💻 {$_('setup.role.your_devices')}</span>
			</div>
		{/if}
	</div>

	<!-- Plain-language summary -->
	<ul class="space-y-2 text-sm text-zinc-700 dark:text-zinc-300">
		<li class="flex items-start gap-2">
			<i class="bi bi-check2-circle text-emerald-500 mt-0.5"></i>
			<span>
				{wizard.role !== 'wifi-router'
					? $_('setup.review.line_extender', { values: { ssid: wizard.uplinkSSID } })
					: wizard.wanMode === 'modem'
						? $_('setup.review.line_modem', { values: { apn: wizard.wanApn || 'auto' } })
						: $_('setup.review.line_router', { values: { iface: wizard.wanInterface, mode: wizard.wanMode } })}
			</span>
		</li>
		<li class="flex items-start gap-2">
			<i class="bi bi-check2-circle text-emerald-500 mt-0.5"></i>
			<span>{$_('setup.review.line_ap', { values: { ssid: wizard.apSSID, channel: wizard.apChannel || $_('setup.wifi.channel_auto') } })}</span>
		</li>
		<li class="flex items-start gap-2">
			<i class="bi bi-check2-circle text-emerald-500 mt-0.5"></i>
			<span>{$_('setup.review.line_admin', { values: { name: wizard.deviceName } })}</span>
		</li>
		{#if wizard.enableTelegram}
			<li class="flex items-start gap-2">
				<i class="bi bi-check2-circle text-emerald-500 mt-0.5"></i>
				<span>{$_('setup.review.line_tg')}</span>
			</li>
		{/if}
	</ul>

	<!-- Rollback assurance (M33 tie-in) -->
	<div class="p-3 rounded-md bg-brand-50 dark:bg-brand-500/10 border border-brand-100 dark:border-brand-500/20 text-sm">
		<i class="bi bi-shield-check text-brand-500 mr-1.5"></i>
		<span class="text-zinc-700 dark:text-zinc-200">
			{$_('setup.review.rollback_promise')}
		</span>
	</div>
</StepCard>
