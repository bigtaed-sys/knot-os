<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, apiDelete, ApiError } from '$lib/api';
	import type { GuestSession } from '$lib/types';

	// Single active session at a time. null = nothing active.
	let session = $state<GuestSession | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Create dialog.
	let showCreate = $state(false);
	let createSSID = $state('');
	let createDuration = $state<'1h' | '4h' | '24h' | 'forever'>('4h');
	let creating = $state(false);

	// Visual countdown — re-rendered every second from `remaining`.
	let now = $state(Date.now());
	let tickTimer: ReturnType<typeof setInterval> | null = null;
	let pollTimer: ReturnType<typeof setInterval> | null = null;

	const durationSeconds = $derived.by(() => {
		switch (createDuration) {
			case '1h':
				return 3600;
			case '4h':
				return 4 * 3600;
			case '24h':
				return 24 * 3600;
			case 'forever':
				return 0;
		}
	});

	const remaining = $derived.by(() => {
		if (!session) return 0;
		if (!session.expires_at) return Infinity;
		const expiresMs = new Date(session.expires_at).getTime();
		return Math.max(0, Math.floor((expiresMs - now) / 1000));
	});

	function formatDuration(seconds: number): string {
		if (seconds === Infinity) return $_('guests.forever');
		if (seconds <= 0) return '0:00';
		const h = Math.floor(seconds / 3600);
		const m = Math.floor((seconds % 3600) / 60);
		const s = seconds % 60;
		if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
		return `${m}:${String(s).padStart(2, '0')}`;
	}

	async function load() {
		try {
			const res = await fetch('/api/guest', { credentials: 'same-origin' });
			if (res.status === 204) {
				session = null;
			} else if (res.status === 401) {
				goto('/login', { replaceState: true });
				return;
			} else if (res.status === 503) {
				error = $_('guests.disabled');
			} else if (res.ok) {
				session = (await res.json()) as GuestSession;
			} else {
				const body = (await res.json().catch(() => null)) as
					| { error?: { message?: string } }
					| null;
				error = body?.error?.message ?? `HTTP ${res.status}`;
			}
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	async function create() {
		if (creating) return;
		creating = true;
		error = null;
		try {
			session = await apiPost<GuestSession>('/guest', {
				ssid: createSSID.trim(),
				duration_sec: durationSeconds,
				profile_id: 'guest'
			});
			showCreate = false;
			createSSID = '';
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				error = e.message;
			}
		} finally {
			creating = false;
		}
	}

	async function revoke() {
		if (!confirm($_('guests.revoke_confirm'))) return;
		try {
			await apiDelete('/guest');
			session = null;
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
		} catch {
			// Fall through — clipboard API requires HTTPS or localhost on
			// some browsers. The PSK + SSID are visible on screen anyway.
		}
	}

	onMount(() => {
		load();
		tickTimer = setInterval(() => (now = Date.now()), 1000);
		// Poll less aggressively than the visual tick — server-side
		// expiry sweeper runs every 30s, we just need fresh data
		// periodically to catch revocations from another tab.
		pollTimer = setInterval(load, 15_000);
	});
	onDestroy(() => {
		if (tickTimer) clearInterval(tickTimer);
		if (pollTimer) clearInterval(pollTimer);
	});
</script>

<header class="mb-6 flex items-start justify-between gap-3 flex-wrap">
	<div>
		<h1 class="text-2xl font-semibold">{$_('guests.title')}</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('guests.subtitle')}</p>
	</div>
	{#if session}
		<button class="btn-ghost text-rose-600" onclick={revoke}>
			<i class="bi bi-x-circle"></i>
			{$_('guests.revoke')}
		</button>
	{:else if !loading && error !== $_('guests.disabled')}
		<button class="btn-primary" onclick={() => (showCreate = true)}>
			<i class="bi bi-plus-circle"></i>
			{$_('guests.create')}
		</button>
	{/if}
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else if error}
	<div class="surface p-5 border-amber-200 dark:border-amber-800/50 bg-amber-50 dark:bg-amber-500/5">
		<div class="flex items-start gap-3">
			<i class="bi bi-info-circle text-amber-600 dark:text-amber-400 text-xl"></i>
			<div class="text-sm text-amber-800 dark:text-amber-200">{error}</div>
		</div>
	</div>
{:else if session}
	<!-- Active session — full-screen-ish QR + meta -->
	<section class="surface p-6 mb-5">
		<div class="grid md:grid-cols-[auto_1fr] gap-6 items-start">
			<!-- QR -->
			<div class="flex flex-col items-center gap-2">
				<img
					src={`data:image/png;base64,${session.qr_png_base64}`}
					alt="Wi-Fi QR"
					class="w-64 h-64 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-white p-2"
				/>
				<a
					class="text-xs text-brand-600 dark:text-brand-400 hover:underline"
					href="/api/guest/qr.png"
					target="_blank"
					rel="noreferrer"
				>
					<i class="bi bi-arrows-fullscreen"></i>
					{$_('guests.qr_open_full')}
				</a>
			</div>
			<!-- Meta -->
			<div class="space-y-3">
				<div>
					<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">SSID</div>
					<div class="text-2xl font-semibold font-mono mt-1 flex items-center gap-2 flex-wrap">
						<span>{session.ssid}</span>
						<button
							class="text-zinc-400 hover:text-brand-500 text-base"
							title={$_('guests.copy')}
							onclick={() => copy(session?.ssid ?? '')}
						>
							<i class="bi bi-clipboard"></i>
						</button>
					</div>
				</div>
				<div>
					<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
						{$_('guests.psk')}
					</div>
					<div class="text-2xl font-semibold font-mono mt-1 flex items-center gap-2 flex-wrap">
						<span>{session.psk}</span>
						<button
							class="text-zinc-400 hover:text-brand-500 text-base"
							title={$_('guests.copy')}
							onclick={() => copy(session?.psk ?? '')}
						>
							<i class="bi bi-clipboard"></i>
						</button>
					</div>
				</div>
				<div>
					<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400">
						{$_('guests.remaining')}
					</div>
					<div
						class="text-xl font-semibold mt-1 tabular-nums"
						class:text-rose-600={remaining < 60 && remaining > 0}
					>
						{formatDuration(remaining)}
					</div>
				</div>
				<p class="text-xs text-zinc-500 dark:text-zinc-400 max-w-md">
					{$_('guests.isolation_help')}
				</p>
			</div>
		</div>
	</section>
{:else}
	<!-- Empty state -->
	<div class="surface p-10 text-center">
		<div
			class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4"
		>
			<i class="bi bi-people text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<h2 class="font-medium text-lg mb-2">{$_('guests.empty_title')}</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
			{$_('guests.empty_help')}
		</p>
	</div>
{/if}

<!-- Create dialog -->
{#if showCreate}
	<div
		class="fixed inset-0 z-50 bg-black/40 flex items-center justify-center p-4"
		onclick={() => (showCreate = false)}
		role="presentation"
	>
		<div
			class="surface p-6 max-w-md w-full"
			onclick={(e) => e.stopPropagation()}
			onkeydown={(e) => e.stopPropagation()}
			role="dialog"
			tabindex="-1"
		>
			<h2 class="text-lg font-semibold mb-4">{$_('guests.create_title')}</h2>

			<label class="block text-sm font-medium mb-1" for="g-ssid">{$_('guests.ssid_label')}</label>
			<input
				id="g-ssid"
				class="input mb-1"
				placeholder={$_('guests.ssid_placeholder')}
				bind:value={createSSID}
				maxlength="32"
			/>
			<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-4">
				{$_('guests.ssid_help')}
			</p>

			<div class="text-sm font-medium mb-2">{$_('guests.duration_label')}</div>
			<div class="grid grid-cols-4 gap-2 mb-5">
				{#each [
					{ v: '1h', label: '1h' },
					{ v: '4h', label: '4h' },
					{ v: '24h', label: '24h' },
					{ v: 'forever', label: $_('guests.forever') }
				] as opt}
					<button
						type="button"
						class="px-2 py-2 rounded-lg border text-sm transition-colors
							{createDuration === opt.v
								? 'border-brand-500 bg-brand-50 dark:bg-brand-500/10 text-brand-700 dark:text-brand-300'
								: 'border-zinc-200 dark:border-zinc-700 hover:border-zinc-300'}"
						onclick={() => (createDuration = opt.v as typeof createDuration)}
					>
						{opt.label}
					</button>
				{/each}
			</div>

			<div class="flex justify-end gap-2">
				<button class="btn-ghost" onclick={() => (showCreate = false)} disabled={creating}>
					{$_('common.cancel')}
				</button>
				<button class="btn-primary" onclick={create} disabled={creating}>
					{#if creating}
						<span class="spinner"></span>
						{$_('guests.creating')}
					{:else}
						{$_('guests.create_confirm')}
					{/if}
				</button>
			</div>
		</div>
	</div>
{/if}
