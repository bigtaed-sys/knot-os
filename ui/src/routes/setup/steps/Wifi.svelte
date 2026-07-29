<script lang="ts">
	import { _ } from 'svelte-i18n';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import InlineHelp from '$lib/components/wizard/InlineHelp.svelte';
	import { wizard } from '../wizard.svelte';
	import { API_BASE } from '$lib/api';

	// Live QR — the WIFI: join string is rendered to a PNG by the
	// daemon's /api/setup/qr endpoint (same go-qrcode the WireGuard
	// peer QR uses), so the UI bundle carries no QR dependency. The
	// <img> src is reactive: as the user edits the SSID/PSK the
	// browser re-fetches the encoded code.
	function wifiURI(ssid: string, psk: string): string {
		// WIFI:T:WPA;S:<ssid>;P:<psk>;H:false;
		const esc = (s: string) =>
			s.replace(/\\/g, '\\\\').replace(/;/g, '\\;').replace(/,/g, '\\,').replace(/:/g, '\\:').replace(/"/g, '\\"');
		const sec = psk.length >= 8 ? 'WPA' : 'nopass';
		return `WIFI:T:${sec};S:${esc(ssid)};P:${esc(psk)};H:false;`;
	}

	const uri = $derived(wifiURI(wizard.apSSID, wizard.apPSK));
	const qrSrc = $derived(`${API_BASE}/setup/qr?text=${encodeURIComponent(uri)}`);

	const canNext = $derived.by(() => {
		if (wizard.apSSID.length === 0 || wizard.apSSID.length > 32) return false;
		if (wizard.apPSK.length < 8 || wizard.apPSK.length > 63) return false;
		return true;
	});

	function strength(p: string): { label: string; cls: string; pct: number } {
		const len = p.length;
		const hasUpper = /[A-Z]/.test(p);
		const hasDigit = /\d/.test(p);
		const hasSym = /[^a-zA-Z0-9]/.test(p);
		const score = (len >= 8 ? 1 : 0) + (len >= 12 ? 1 : 0) + (len >= 16 ? 1 : 0)
			+ (hasUpper ? 1 : 0) + (hasDigit ? 1 : 0) + (hasSym ? 1 : 0);
		if (len === 0) return { label: '', cls: '', pct: 0 };
		if (score < 2) return { label: $_('setup.wifi.pw_weak'), cls: 'bg-red-500', pct: 25 };
		if (score < 4) return { label: $_('setup.wifi.pw_ok'), cls: 'bg-amber-500', pct: 55 };
		if (score < 5) return { label: $_('setup.wifi.pw_strong'), cls: 'bg-emerald-500', pct: 80 };
		return { label: $_('setup.wifi.pw_excellent'), cls: 'bg-emerald-600', pct: 100 };
	}

	const pwState = $derived(strength(wizard.apPSK));

	// 5 GHz is offered only on boards whose radio supports it (Pi 4/5).
	const fiveGhz = $derived(wizard.cap?.five_ghz_capable ?? false);
	const channelOptions = $derived(
		wizard.apBand === '5'
			? [
					{ v: 0, l: $_('setup.wifi.channel_auto') },
					{ v: 36, l: '36' },
					{ v: 40, l: '40' },
					{ v: 44, l: '44' },
					{ v: 48, l: '48' }
				]
			: [
					{ v: 0, l: $_('setup.wifi.channel_auto') },
					{ v: 1, l: '1' },
					{ v: 6, l: '6' },
					{ v: 11, l: '11' }
				]
	);
	$effect(() => {
		if (!fiveGhz && wizard.apBand === '5') wizard.apBand = '2.4';
	});
	$effect(() => {
		if (!channelOptions.some((o) => o.v === wizard.apChannel)) wizard.apChannel = 0;
	});

	let showAdvanced = $state(false);
</script>

<StepCard
	title={$_('setup.wifi.title')}
	subtitle={$_('setup.wifi.subtitle')}
	{canNext}
>
	<div class="grid sm:grid-cols-[1fr_auto] gap-5 items-start">
		<!-- Left: form -->
		<div class="space-y-4 min-w-0">
			<div>
				<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
					{$_('setup.wifi.ssid_label')}
					<InlineHelp text={$_('setup.wifi.ssid_help')} />
				</label>
				<input
					type="text"
					bind:value={wizard.apSSID}
					placeholder="MyHome"
					maxlength={32}
					class="input mt-1"
				/>
				<p class="text-xs text-zinc-500 mt-1">
					{wizard.apSSID.length}/32 {$_('setup.wifi.ssid_chars')}
				</p>
			</div>

			<div>
				<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
					{$_('setup.wifi.psk_label')}
					<InlineHelp text={$_('setup.wifi.psk_help')} />
				</label>
				<input
					type="text"
					bind:value={wizard.apPSK}
					placeholder={$_('setup.wifi.psk_ph')}
					minlength={8}
					maxlength={63}
					class="input mt-1 font-mono"
				/>
				{#if wizard.apPSK.length > 0}
					<div class="mt-2">
						<div class="h-1.5 rounded-full bg-zinc-200 dark:bg-zinc-700 overflow-hidden">
							<div
								class="h-full transition-all {pwState.cls}"
								style="width: {pwState.pct}%"
							></div>
						</div>
						<p class="text-xs mt-1 text-zinc-600 dark:text-zinc-400">{pwState.label}</p>
					</div>
				{:else}
					<p class="text-xs text-zinc-500 mt-1">{$_('setup.wifi.psk_min')}</p>
				{/if}
			</div>

			<button
				type="button"
				onclick={() => (showAdvanced = !showAdvanced)}
				class="text-xs text-brand-600 dark:text-brand-400 hover:underline"
			>
				<i class="bi {showAdvanced ? 'bi-chevron-up' : 'bi-chevron-down'} mr-1"></i>
				{$_('setup.wifi.advanced')}
			</button>
			{#if showAdvanced}
				<div class="space-y-3 p-3 rounded-md bg-zinc-50 dark:bg-zinc-900/50 border border-zinc-200 dark:border-zinc-800">
					{#if fiveGhz}
						<div>
							<div class="text-xs font-medium text-zinc-700 dark:text-zinc-300">{$_('setup.wifi.band_label')}</div>
							<div class="flex gap-2 mt-1.5">
								{#each ['2.4', '5'] as band}
									<button
										type="button"
										onclick={() => (wizard.apBand = band as '2.4' | '5')}
										class="px-4 py-1.5 rounded-md border text-xs flex-1
											{wizard.apBand === band
												? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10 text-brand-700 dark:text-brand-300'
												: 'border-zinc-200 dark:border-zinc-700'}"
									>
										{band} GHz
									</button>
								{/each}
							</div>
							<p class="text-[11px] text-zinc-500 mt-1">{$_('setup.wifi.band_note')}</p>
						</div>
					{/if}
					<div>
						<label class="text-xs font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
							{$_('setup.wifi.channel_label')}
							<InlineHelp text={$_('setup.wifi.channel_help')} />
						</label>
						<div class="flex gap-2 mt-1.5">
							{#each channelOptions as opt}
								<button
									type="button"
									onclick={() => (wizard.apChannel = opt.v)}
									class="
										px-3 py-1.5 rounded-md border text-xs flex-1
										{wizard.apChannel === opt.v
											? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10 text-brand-700 dark:text-brand-300'
											: 'border-zinc-200 dark:border-zinc-700'}
									"
								>
									{opt.l}
								</button>
							{/each}
						</div>
					</div>
				</div>
			{/if}
		</div>

		<!-- Right: live QR preview -->
		<div class="hidden sm:block w-44 shrink-0 sticky top-4">
			<div class="text-xs font-medium text-zinc-500 dark:text-zinc-400 mb-2 text-center">
				{$_('setup.wifi.qr_preview')}
			</div>
			{#if canNext}
				<div class="p-3 bg-white border border-zinc-200 rounded-lg flex flex-col items-center gap-2">
					<img
						src={qrSrc}
						alt={$_('setup.wifi.qr_preview')}
						width="128"
						height="128"
						class="w-32 h-32 rounded"
					/>
					<p class="text-[10px] text-center text-zinc-500 leading-tight">
						{$_('setup.wifi.qr_help')}
					</p>
				</div>
			{:else}
				<div class="p-4 bg-zinc-50 dark:bg-zinc-900 border border-dashed border-zinc-300 dark:border-zinc-700 rounded-lg text-center">
					<i class="bi bi-qr-code text-3xl text-zinc-300 dark:text-zinc-600"></i>
					<p class="text-xs text-zinc-500 mt-2">{$_('setup.wifi.qr_pending')}</p>
				</div>
			{/if}
		</div>
	</div>
</StepCard>
