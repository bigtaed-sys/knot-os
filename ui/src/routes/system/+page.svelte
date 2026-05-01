<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, ApiError, API_BASE } from '$lib/api';
	import type { SystemStatus } from '$lib/types';

	let status = $state<SystemStatus | null>(null);
	let busy = $state<string | null>(null);
	let updateMsg = $state<{ kind: 'ok' | 'err'; text: string } | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);

	async function load() {
		try {
			status = await apiGet<SystemStatus>('/status');
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) goto('/login', { replaceState: true });
		}
	}

	async function reboot() {
		if (!confirm($_('system.reboot_confirm'))) return;
		busy = 'reboot';
		try {
			await apiPost('/system/reboot');
		} catch (e) {
			console.error(e);
		} finally {
			busy = null;
		}
	}

	async function shutdown() {
		if (!confirm($_('system.shutdown_confirm'))) return;
		busy = 'shutdown';
		try {
			await apiPost('/system/shutdown');
		} catch (e) {
			console.error(e);
		} finally {
			busy = null;
		}
	}

	async function uploadUpdate(e: Event) {
		const file = (e.target as HTMLInputElement).files?.[0];
		if (!file) return;
		busy = 'update';
		updateMsg = null;
		try {
			const res = await fetch(`${API_BASE}/system/update`, {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'content-type': 'application/octet-stream' },
				body: file
			});
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				throw new Error(body?.error?.message ?? `HTTP ${res.status}`);
			}
			updateMsg = { kind: 'ok', text: $_('system.update_success') };
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			updateMsg = { kind: 'err', text: $_('system.update_error', { values: { message: msg } }) };
		} finally {
			busy = null;
			if (fileInput) fileInput.value = '';
		}
	}

	onMount(load);
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('system.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('system.subtitle')}</p>
</header>

<div class="space-y-5">
	<!-- Info -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-3 flex items-center gap-2">
			<i class="bi bi-info-circle text-brand-500"></i>
			{$_('system.info_section')}
		</h2>
		{#if status}
			<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm">
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('system.hostname')}</dt>
				<dd class="font-medium">{status.device}</dd>
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('system.version')}</dt>
				<dd class="font-mono text-sm">{status.version}</dd>
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('system.role')}</dt>
				<dd>
					<span class="badge badge-info">{status.role}</span>
				</dd>
			</dl>
		{:else}
			<div class="spinner text-zinc-400"></div>
		{/if}
	</section>

	<!-- Update -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-1 flex items-center gap-2">
			<i class="bi bi-arrow-up-circle text-brand-500"></i>
			{$_('system.update_section')}
		</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-3">
			{$_('system.update_subtitle')}
		</p>
		<input
			bind:this={fileInput}
			type="file"
			accept=""
			onchange={uploadUpdate}
			class="hidden"
		/>
		<button
			class="btn-primary"
			disabled={busy === 'update'}
			onclick={() => fileInput?.click()}
		>
			{#if busy === 'update'}
				<span class="spinner"></span>
				{$_('system.update_uploading')}
			{:else}
				<i class="bi bi-upload"></i>
				{$_('system.update_choose')}
			{/if}
		</button>
		{#if updateMsg}
			<div
				class="mt-3 flex items-start gap-2 p-3 rounded-lg text-sm
					{updateMsg.kind === 'ok'
						? 'bg-emerald-50 dark:bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
						: 'bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300'}"
			>
				<i class="bi {updateMsg.kind === 'ok' ? 'bi-check-circle' : 'bi-exclamation-circle'} mt-0.5 shrink-0"></i>
				<span>{updateMsg.text}</span>
			</div>
		{/if}
	</section>

	<!-- Power -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-3 flex items-center gap-2">
			<i class="bi bi-power text-brand-500"></i>
			{$_('system.actions_section')}
		</h2>
		<div class="flex flex-wrap gap-3">
			<button class="btn-ghost" disabled={busy !== null} onclick={reboot}>
				<i class="bi bi-arrow-clockwise"></i>
				{$_('system.reboot')}
			</button>
			<button class="btn-danger" disabled={busy !== null} onclick={shutdown}>
				<i class="bi bi-power"></i>
				{$_('system.shutdown')}
			</button>
		</div>
	</section>
</div>
