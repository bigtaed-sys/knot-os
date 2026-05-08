<!--
	Setup wizard orchestrator. Picks the right step component based
	on the shared wizard store. Each step is a self-contained card
	with its own validation; the chrome (progress dots, navigation)
	lives in the StepCard wrapper.

	M36 (v2026.05.2) — full rework. The previous monolithic 663-line
	+page.svelte is gone; logic lives in steps/* and shared state in
	wizard.svelte.ts.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { _ } from 'svelte-i18n';
	import ProgressDots from '$lib/components/wizard/ProgressDots.svelte';
	import { wizard } from './wizard.svelte';

	import Welcome from './steps/Welcome.svelte';
	import Hardware from './steps/Hardware.svelte';
	import Role from './steps/Role.svelte';
	import Connection from './steps/Connection.svelte';
	import Wifi from './steps/Wifi.svelte';
	import Admin from './steps/Admin.svelte';
	import Review from './steps/Review.svelte';
	import Apply from './steps/Apply.svelte';

	onMount(() => {
		wizard.restore();
	});
</script>

<svelte:head>
	<title>{$_('setup.welcome.title')} · KnotOS</title>
</svelte:head>

<div class="min-h-screen flex flex-col bg-gradient-to-br from-zinc-50 via-white to-brand-50/40 dark:from-zinc-950 dark:via-zinc-900 dark:to-brand-500/5">
	<!-- Brand header -->
	<header class="px-4 sm:px-6 py-4 flex items-center gap-2">
		<div class="w-8 h-8 rounded-lg bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center shadow-sm">
			<i class="bi bi-diagram-3-fill text-white"></i>
		</div>
		<div class="font-semibold">{$_('app.name')}</div>
	</header>

	<!-- Progress -->
	<ProgressDots />

	<!-- Active step -->
	<main class="flex-1 flex items-stretch w-full">
		{#if wizard.step === 'welcome'}
			<Welcome />
		{:else if wizard.step === 'hardware'}
			<Hardware />
		{:else if wizard.step === 'role'}
			<Role />
		{:else if wizard.step === 'connection'}
			<Connection />
		{:else if wizard.step === 'wifi'}
			<Wifi />
		{:else if wizard.step === 'admin'}
			<Admin />
		{:else if wizard.step === 'review'}
			<Review />
		{:else if wizard.step === 'apply'}
			<Apply />
		{/if}
	</main>

	<footer class="px-4 sm:px-6 py-4 text-center text-xs text-zinc-400">
		KnotOS · v2026.05.2
	</footer>
</div>
