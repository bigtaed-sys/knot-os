<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import { apiPost } from '$lib/api';
	import { wizard } from '../wizard.svelte';

	type Phase = 'saving' | 'applying' | 'verifying' | 'done' | 'failed';
	let phase = $state<Phase>('saving');
	let detail = $state<string | null>(null);
	let countdown = $state(0);

	async function run() {
		phase = 'saving';
		detail = null;

		// Build the body the existing /setup/complete endpoint expects.
		// (It internally PUTs config — which now goes through the M33
		// coordinator. We watch the response for apply_id and surface
		// the rollback reason if any.)
		const body: any = {
			device: {
				name: wizard.deviceName,
				country: wizard.country
			},
			password: wizard.password,
			role: wizard.role,
			ap: {
				ssid: wizard.apSSID,
				psk: wizard.apPSK,
				band: wizard.apBand,
				channel: wizard.apChannel
			}
		};
		if (wizard.role === 'wifi-router') {
			body.wan = {
				interface: wizard.wanMode === 'modem' ? '' : wizard.wanInterface,
				mode: wizard.wanMode,
				...(wizard.wanMode === 'pppoe'
					? { pppoe_user: wizard.wanPppoeUser, pppoe_pass: wizard.wanPppoePass }
					: {}),
				...(wizard.wanMode === 'modem'
					? { modem: { apn: wizard.wanApn, pin: wizard.wanPin, username: wizard.wanModemUser } }
					: {})
			};
			// Wired LAN ports only apply to an Ethernet WAN.
			if (wizard.wanMode !== 'modem' && wizard.lanPorts.length > 0) {
				body.lan_ports = wizard.lanPorts.filter((p) => p !== wizard.wanInterface);
			}
		} else {
			body.uplink = { ssid: wizard.uplinkSSID, psk: wizard.uplinkPSK };
		}

		phase = 'applying';
		try {
			const r = await apiPost<any>('/setup/complete', body, { timeoutMs: 90000 });
			// The setup endpoint either returns the new config (legacy
			// path) or the M33 coord shape with apply_id + status.
			if (r?.error) {
				phase = 'failed';
				detail = r.error.message || r.error.reason || $_('setup.apply.unknown_error');
				return;
			}
			phase = 'verifying';
			// Brief pause to let the user see "Verifying" — the
			// coordinator already ran the health check on the server,
			// so this is purely UX padding.
			await new Promise((r) => setTimeout(r, 800));
			phase = 'done';
			startCountdown();
		} catch (e: any) {
			phase = 'failed';
			detail = e?.message || String(e);
		}
	}

	function startCountdown() {
		countdown = 3;
		const t = setInterval(() => {
			countdown -= 1;
			if (countdown <= 0) {
				clearInterval(t);
				wizard.clear();
				goto('/login', { replaceState: true });
			}
		}, 1000);
	}

	onMount(() => {
		run();
	});
</script>

<StepCard title={$_('setup.apply.title')} subtitle={$_('setup.apply.subtitle')} hideBack hideNext>
	<div class="space-y-3">
		<!-- Step 1: saving -->
		<div class="flex items-center gap-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50">
			<div class="w-8 h-8 rounded-full flex items-center justify-center
				{phase === 'saving'
					? 'bg-brand-500 text-white'
					: 'bg-emerald-500 text-white'}">
				{#if phase === 'saving'}
					<i class="bi bi-arrow-repeat animate-spin text-sm"></i>
				{:else}
					<i class="bi bi-check2 text-sm"></i>
				{/if}
			</div>
			<span class="text-sm">{$_('setup.apply.saving')}</span>
		</div>

		<!-- Step 2: applying -->
		<div class="flex items-center gap-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50
			{phase === 'saving' ? 'opacity-50' : ''}">
			<div class="w-8 h-8 rounded-full flex items-center justify-center
				{phase === 'applying'
					? 'bg-brand-500 text-white'
					: phase === 'verifying' || phase === 'done'
						? 'bg-emerald-500 text-white'
						: 'bg-zinc-300 dark:bg-zinc-700 text-zinc-500'}">
				{#if phase === 'applying'}
					<i class="bi bi-arrow-repeat animate-spin text-sm"></i>
				{:else if phase === 'verifying' || phase === 'done'}
					<i class="bi bi-check2 text-sm"></i>
				{:else}
					<i class="bi bi-circle text-sm"></i>
				{/if}
			</div>
			<span class="text-sm">{$_('setup.apply.applying')}</span>
		</div>

		<!-- Step 3: verifying -->
		<div class="flex items-center gap-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50
			{phase === 'saving' || phase === 'applying' ? 'opacity-50' : ''}">
			<div class="w-8 h-8 rounded-full flex items-center justify-center
				{phase === 'verifying'
					? 'bg-brand-500 text-white'
					: phase === 'done'
						? 'bg-emerald-500 text-white'
						: 'bg-zinc-300 dark:bg-zinc-700 text-zinc-500'}">
				{#if phase === 'verifying'}
					<i class="bi bi-arrow-repeat animate-spin text-sm"></i>
				{:else if phase === 'done'}
					<i class="bi bi-check2 text-sm"></i>
				{:else}
					<i class="bi bi-circle text-sm"></i>
				{/if}
			</div>
			<span class="text-sm">{$_('setup.apply.verifying')}</span>
		</div>

		{#if phase === 'done'}
			<div class="mt-4 p-4 rounded-md bg-emerald-50 dark:bg-emerald-500/10 border border-emerald-200 dark:border-emerald-500/30 text-center">
				<i class="bi bi-check-circle-fill text-emerald-500 text-3xl"></i>
				<h3 class="font-semibold mt-2">{$_('setup.apply.done_title')}</h3>
				<p class="text-sm text-zinc-600 dark:text-zinc-400 mt-1">
					{$_('setup.apply.done_redirect', { values: { sec: countdown } })}
				</p>
				<button
					type="button"
					class="btn-primary mt-3"
					onclick={() => {
						wizard.clear();
						goto('/login', { replaceState: true });
					}}
				>
					{$_('setup.apply.go_now')}
				</button>
			</div>
		{:else if phase === 'failed'}
			<div class="mt-4 p-4 rounded-md bg-red-50 dark:bg-red-500/10 border border-red-200 dark:border-red-500/30">
				<div class="flex items-start gap-3">
					<i class="bi bi-x-circle-fill text-red-500 text-2xl shrink-0 mt-0.5"></i>
					<div class="flex-1 min-w-0">
						<h3 class="font-semibold text-red-700 dark:text-red-400">
							{$_('setup.apply.failed_title')}
						</h3>
						<p class="text-sm text-zinc-700 dark:text-zinc-300 mt-1 break-words">
							{detail || $_('setup.apply.unknown_error')}
						</p>
						<p class="text-xs text-zinc-500 mt-2">
							{$_('setup.apply.failed_rolled_back')}
						</p>
						<button
							type="button"
							class="btn-ghost mt-3"
							onclick={() => wizard.go('review')}
						>
							{$_('setup.apply.retry')}
						</button>
					</div>
				</div>
			</div>
		{/if}
	</div>
</StepCard>
