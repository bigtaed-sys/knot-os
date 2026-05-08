<script lang="ts">
	import { _ } from 'svelte-i18n';
	import StepCard from '$lib/components/wizard/StepCard.svelte';
	import InlineHelp from '$lib/components/wizard/InlineHelp.svelte';
	import { wizard } from '../wizard.svelte';

	const canNext = $derived.by(() => {
		if (wizard.password.length < 8) return false;
		return wizard.password === wizard.passwordConfirm;
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
	const pwState = $derived(strength(wizard.password));
	const mismatch = $derived(
		wizard.passwordConfirm.length > 0 && wizard.password !== wizard.passwordConfirm
	);
</script>

<StepCard title={$_('setup.admin.title')} subtitle={$_('setup.admin.subtitle')} {canNext}>
	<!-- Device name -->
	<div>
		<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
			{$_('setup.admin.device_name_label')}
			<InlineHelp text={$_('setup.admin.device_name_help')} />
		</label>
		<input
			type="text"
			bind:value={wizard.deviceName}
			placeholder="knot"
			class="input mt-1"
		/>
	</div>

	<!-- Country -->
	<div>
		<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
			{$_('setup.admin.country_label')}
			<InlineHelp text={$_('setup.admin.country_help')} />
		</label>
		<input
			type="text"
			bind:value={wizard.country}
			maxlength={2}
			placeholder="RU"
			class="input mt-1 font-mono uppercase"
		/>
	</div>

	<!-- Admin password -->
	<div class="pt-3 border-t border-zinc-200 dark:border-zinc-800">
		<label class="text-sm font-medium text-zinc-700 dark:text-zinc-300 flex items-center gap-2">
			{$_('setup.admin.password_label')}
			<InlineHelp text={$_('setup.admin.password_help')} />
		</label>
		<input
			type="password"
			bind:value={wizard.password}
			minlength={8}
			class="input mt-1"
		/>
		{#if wizard.password.length > 0}
			<div class="mt-2">
				<div class="h-1.5 rounded-full bg-zinc-200 dark:bg-zinc-700 overflow-hidden">
					<div class="h-full transition-all {pwState.cls}" style="width: {pwState.pct}%"></div>
				</div>
				<p class="text-xs mt-1 text-zinc-600 dark:text-zinc-400">{pwState.label}</p>
			</div>
		{/if}
	</div>

	<div>
		<label for="pwconfirm" class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
			{$_('setup.admin.password_confirm_label')}
		</label>
		<input
			id="pwconfirm"
			type="password"
			bind:value={wizard.passwordConfirm}
			class="input mt-1"
		/>
		{#if mismatch}
			<p class="text-xs text-red-600 dark:text-red-400 mt-1">
				<i class="bi bi-exclamation-circle mr-1"></i>{$_('setup.admin.password_mismatch')}
			</p>
		{/if}
	</div>

	<!-- Telegram bot opt-in -->
	<div class="pt-3 border-t border-zinc-200 dark:border-zinc-800">
		<label class="flex items-start gap-3 cursor-pointer">
			<input
				type="checkbox"
				bind:checked={wizard.enableTelegram}
				class="mt-1 rounded text-brand-600"
			/>
			<div>
				<div class="text-sm font-medium text-zinc-700 dark:text-zinc-300">
					{$_('setup.admin.tg_label')}
				</div>
				<div class="text-xs text-zinc-500 dark:text-zinc-400 mt-0.5">
					{$_('setup.admin.tg_help')}
				</div>
			</div>
		</label>
	</div>
</StepCard>
