<script lang="ts">
	import { onMount } from 'svelte';
	import { fade } from 'svelte/transition';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, apiPut, apiPatch, apiDelete, ApiError, ApiTimeoutError, API_BASE } from '$lib/api';
	import Tabs from '$lib/components/Tabs.svelte';
	import type {
		SystemStatus,
		TLSInfo,
		UpdateCheckResult,
		RescueInfo,
		RescueRevealed,
		ChannelReport,
		NotifyState,
		NotifyPIN
	} from '$lib/types';

	let activeTab = $state('overview');
	const tabList = $derived([
		{ id: 'overview', label: $_('system.tab_overview'), icon: 'bi-info-circle' },
		{ id: 'updates', label: $_('system.tab_updates'), icon: 'bi-arrow-repeat' },
		{ id: 'security', label: $_('system.tab_security'), icon: 'bi-shield-lock' },
		{ id: 'more', label: $_('system.tab_more'), icon: 'bi-three-dots' }
	]);

	let status = $state<SystemStatus | null>(null);
	let tls = $state<TLSInfo | null>(null);
	let tlsAvailable = $state(true);
	let regenerating = $state(false);
	let busy = $state<string | null>(null);
	let updateMsg = $state<{ kind: 'ok' | 'err'; text: string } | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);
	let sigInput = $state<HTMLInputElement | null>(null);
	let binFile = $state<File | null>(null);
	let sigFile = $state<File | null>(null);

	// GitHub auto-update state.
	let upd = $state<UpdateCheckResult | null>(null);
	let updAvailable = $state(true);
	let updChecking = $state(false);
	let updApplying = $state(false);
	let updError = $state<string | null>(null);

	// Notifications / Telegram bot state.
	let notifyState = $state<NotifyState | null>(null);
	let notifyTokenInput = $state('');
	let notifySaving = $state(false);
	let notifyError = $state<string | null>(null);
	// MTProto transport (app_id/app_hash) — lets the bot work where the
	// HTTP Bot API is blocked, dialing through the local Telegram proxy.
	let appIDInput = $state('');
	let appHashInput = $state('');
	let appSaving = $state(false);

	async function saveNotifyApp() {
		appSaving = true;
		notifyError = null;
		try {
			notifyState = await apiPut<NotifyState>(
				'/notify/telegram/app',
				{ app_id: parseInt(appIDInput, 10) || 0, app_hash: appHashInput.trim() },
				{ timeoutMs: 30000 }
			);
			appHashInput = '';
		} catch (e) {
			if (e instanceof ApiError) {
				const b = e.body as { error?: { message?: string } } | undefined;
				notifyError = b?.error?.message ?? e.message;
			} else {
				notifyError = e instanceof Error ? e.message : String(e);
			}
		} finally {
			appSaving = false;
		}
	}

	async function clearNotifyApp() {
		appSaving = true;
		notifyError = null;
		try {
			notifyState = await apiPut<NotifyState>(
				'/notify/telegram/app',
				{ app_id: 0, app_hash: '' },
				{ timeoutMs: 30000 }
			);
			appIDInput = '';
			appHashInput = '';
		} catch (e) {
			notifyError = e instanceof Error ? e.message : String(e);
		} finally {
			appSaving = false;
		}
	}
	let notifyPIN = $state<NotifyPIN | null>(null);
	let notifyPinTick = $state(0);

	async function loadNotify() {
		try {
			notifyState = await apiGet<NotifyState>('/notify/telegram', { timeoutMs: 3000 });
		} catch (e) {
			if (e instanceof ApiError && e.status === 503) {
				notifyState = null;
			}
		}
	}

	async function saveNotifyToken() {
		if (!notifyTokenInput.trim()) return;
		notifySaving = true;
		notifyError = null;
		try {
			notifyState = await apiPut<NotifyState>('/notify/telegram/token', {
				token: notifyTokenInput.trim()
			});
			notifyTokenInput = '';
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				notifyError = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				notifyError = e.message;
			}
		} finally {
			notifySaving = false;
		}
	}

	async function issuePIN() {
		notifyError = null;
		try {
			notifyPIN = await apiPost<NotifyPIN>('/notify/telegram/pin');
		} catch (e) {
			notifyError = e instanceof Error ? e.message : String(e);
		}
	}

	async function unlinkChat(chatID: number) {
		if (!confirm($_('notify.unlink_confirm'))) return;
		try {
			await apiDelete(`/notify/telegram/chats/${chatID}`);
			await loadNotify();
		} catch (e) {
			notifyError = e instanceof Error ? e.message : String(e);
		}
	}

	async function setPrimaryLang(lang: 'ru' | 'en') {
		try {
			notifyState = await apiPatch<NotifyState>('/notify/telegram/primary-lang', { lang });
		} catch (e) {
			notifyError = e instanceof Error ? e.message : String(e);
		}
	}

	const pinRemaining = $derived.by(() => {
		if (!notifyPIN) return 0;
		void notifyPinTick; // re-evaluate every tick
		const ms = new Date(notifyPIN.expires_at).getTime() - Date.now();
		return Math.max(0, Math.floor(ms / 1000));
	});

	// 1-second tick for PIN countdown + linked-chats poll while a PIN
	// is in flight (so the UI flips the moment the user enters it).
	let pinPollTimer: ReturnType<typeof setInterval> | null = null;
	$effect(() => {
		if (notifyPIN && pinRemaining > 0) {
			if (pinPollTimer) return;
			pinPollTimer = setInterval(async () => {
				notifyPinTick++;
				if (notifyState) {
					const before = notifyState.chats.length;
					await loadNotify();
					if (notifyState && notifyState.chats.length > before) {
						notifyPIN = null;
						if (pinPollTimer) {
							clearInterval(pinPollTimer);
							pinPollTimer = null;
						}
					}
				}
			}, 2000);
		} else if (pinPollTimer) {
			clearInterval(pinPollTimer);
			pinPollTimer = null;
		}
	});

	// Channel scanner state.
	let chReport = $state<ChannelReport | null>(null);
	let chScanning = $state(false);
	let chApplying = $state(false);
	let chError = $state<string | null>(null);

	async function scanChannels() {
		chScanning = true;
		chError = null;
		try {
			// 30s timeout: iw scan on a busy radio that's also serving
			// the AP can take 10–20s on Zero 2W.
			chReport = await apiGet<ChannelReport>('/network/channels', { timeoutMs: 30000 });
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				chError = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				chError = e.message;
			}
		} finally {
			chScanning = false;
		}
	}

	async function applyChannel(ch: number) {
		if (!chReport || chApplying) return;
		chApplying = true;
		chError = null;
		try {
			await apiPost('/network/channels/apply', { channel: ch });
			// Re-pull report so the "current_channel" marker moves to the
			// freshly-applied channel.
			chReport = await apiGet<ChannelReport>('/network/channels', { timeoutMs: 30000 });
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				chError = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				chError = e.message;
			}
		} finally {
			chApplying = false;
		}
	}

	// Rescue keypair state.
	let rescue = $state<RescueInfo | null>(null);
	let rescueAvailable = $state(true);
	let revealing = $state(false);
	let revealed = $state<RescueRevealed | null>(null);
	let rescueError = $state<string | null>(null);

	async function load() {
		try {
			status = await apiGet<SystemStatus>('/status');
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) goto('/login', { replaceState: true });
		}
		try {
			tls = await apiGet<TLSInfo>('/tls/info');
			tlsAvailable = true;
		} catch (e) {
			if (e instanceof ApiError && e.status === 503) {
				tlsAvailable = false;
			}
		}
		// Don't auto-check for updates on mount: that hits GitHub and
		// we don't want the System page to take 30 seconds to load
		// just because the device's uplink is slow. The user clicks
		// "Проверить" / "Check" explicitly. Endpoint-not-configured
		// (503) gets surfaced from the click handler instead of
		// pre-emptively here.

		// Rescue info IS cheap — just a sysfs+memory read on the
		// device — so we do load it on mount.
		try {
			rescue = await apiGet<RescueInfo>('/system/update/rescue', { timeoutMs: 3000 });
			rescueAvailable = true;
		} catch (e) {
			if (e instanceof ApiError && e.status === 503) rescueAvailable = false;
		}
		await loadNotify();
	}

	async function revealRescue() {
		if (!rescue?.private_available) return;
		if (!confirm($_('rescue.reveal_confirm'))) return;
		revealing = true;
		rescueError = null;
		try {
			revealed = await apiPost<RescueRevealed>('/system/update/rescue/reveal');
			// Also reload the info so the "private_available" flag flips off.
			rescue = await apiGet<RescueInfo>('/system/update/rescue');
		} catch (e) {
			if (e instanceof ApiError && e.status === 410) {
				rescueError = $_('rescue.error_already_revealed');
			} else if (e instanceof Error) {
				rescueError = e.message;
			}
		} finally {
			revealing = false;
		}
	}

	async function copyToClipboard(text: string) {
		try {
			await navigator.clipboard.writeText(text);
		} catch {
			// Clipboard API can fail on insecure origins (HTTP); the
			// user can still select+copy from the visible <code>.
		}
	}

	async function checkUpdate() {
		updChecking = true;
		updError = null;
		try {
			// 30s timeout: GitHub API + the network path through whatever
			// uplink the device has. Plenty of margin without being so
			// long that a wedged DNS server traps the user forever.
			upd = await apiGet<UpdateCheckResult>('/system/update/check', { timeoutMs: 30000 });
			updAvailable = true;
		} catch (e) {
			if (e instanceof ApiError && e.status === 503) {
				updAvailable = false;
			} else if (e instanceof ApiTimeoutError) {
				updError = $_('updates.error_timeout');
			} else if (e instanceof ApiError && e.status === 502) {
				updError = $_('updates.error_github');
			} else if (e instanceof Error) {
				updError = e.message;
			}
		} finally {
			updChecking = false;
		}
	}

	async function applyUpdate() {
		if (!upd?.update_available) return;
		const target = upd.latest_version;
		if (!confirm($_('updates.apply_confirm', { values: { version: target } }))) return;
		updApplying = true;
		updError = null;
		try {
			// 5 min timeout: download + verify + atomic install +
			// service restart. Almost all of that is the binary
			// download itself; sub-second on a fast LAN, long minutes
			// over a phone hotspot.
			await apiPost('/system/update/apply', undefined, { timeoutMs: 5 * 60 * 1000 });
			updateMsg = { kind: 'ok', text: $_('updates.apply_success', { values: { version: target } }) };
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				updError = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				updError = e.message;
			}
		} finally {
			updApplying = false;
		}
	}

	async function regenerateLeaf() {
		if (!confirm($_('security.regenerate_confirm'))) return;
		regenerating = true;
		try {
			tls = await apiPost<TLSInfo>('/tls/regenerate');
		} catch (e) {
			console.error(e);
		} finally {
			regenerating = false;
		}
	}

	function fpShort(fp: string): string {
		if (!fp) return '';
		const compact = fp.replace(/:/g, '').toUpperCase();
		return compact.slice(0, 4) + ' ' + compact.slice(4, 8) + ' ' + compact.slice(8, 12) + '…';
	}

	function fmtDate(s: string): string {
		try {
			return new Date(s).toLocaleDateString();
		} catch {
			return s;
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

	function pickBinary(e: Event) {
		binFile = (e.target as HTMLInputElement).files?.[0] ?? null;
		updateMsg = null;
	}

	function pickSignature(e: Event) {
		sigFile = (e.target as HTMLInputElement).files?.[0] ?? null;
		updateMsg = null;
	}

	// uploadUpdate posts the chosen knotd binary to /system/update.
	// When a detached signature (.sig) is also selected it switches to
	// a multipart/form-data body with `binary` + `signature` parts —
	// the shape the daemon requires on production-keyed builds. Without
	// a signature it falls back to the raw octet-stream upload, which
	// only dev-key-empty builds accept.
	async function uploadUpdate() {
		if (!binFile) return;
		busy = 'update';
		updateMsg = null;
		try {
			let res: Response;
			if (sigFile) {
				const form = new FormData();
				form.append('binary', binFile, 'knotd');
				form.append('signature', sigFile, 'knotd.sig');
				res = await fetch(`${API_BASE}/system/update`, {
					method: 'POST',
					credentials: 'same-origin',
					body: form
				});
			} else {
				res = await fetch(`${API_BASE}/system/update`, {
					method: 'POST',
					credentials: 'same-origin',
					headers: { 'content-type': 'application/octet-stream' },
					body: binFile
				});
			}
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				throw new Error(body?.error?.message ?? `HTTP ${res.status}`);
			}
			updateMsg = { kind: 'ok', text: $_('system.update_success') };
			binFile = null;
			sigFile = null;
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			updateMsg = { kind: 'err', text: $_('system.update_error', { values: { message: msg } }) };
		} finally {
			busy = null;
			if (fileInput) fileInput.value = '';
			if (sigInput) sigInput.value = '';
		}
	}

	onMount(load);
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('system.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('system.subtitle')}</p>
</header>

<div>
	<Tabs tabs={tabList} bind:active={activeTab} />

	{#key activeTab}
	<div in:fade={{ duration: 140 }} class="space-y-5">
	{#if activeTab === 'overview'}
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
	{/if}

	{#if activeTab === 'security'}
	<!-- Security / TLS -->
	{#if tlsAvailable}
		<section class="surface p-5">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-shield-lock text-brand-500"></i>
				{$_('security.section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">{$_('security.subtitle')}</p>

			{#if tls}
				<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm mb-4">
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.root_fingerprint')}</dt>
					<dd class="font-mono break-all" title={tls.root_fingerprint}>{fpShort(tls.root_fingerprint)}</dd>
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.root_expires')}</dt>
					<dd>{fmtDate(tls.root_not_after)}</dd>

					<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.leaf_fingerprint')}</dt>
					<dd class="font-mono break-all" title={tls.leaf_fingerprint}>{fpShort(tls.leaf_fingerprint)}</dd>
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.leaf_expires')}</dt>
					<dd>{fmtDate(tls.leaf_not_after)}</dd>

					{#if tls.leaf_dns_names && tls.leaf_dns_names.length > 0}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.leaf_names')}</dt>
						<dd class="font-mono text-xs">{tls.leaf_dns_names.join(', ')}</dd>
					{/if}
					{#if tls.leaf_ips && tls.leaf_ips.length > 0}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('security.leaf_ips')}</dt>
						<dd class="font-mono text-xs">{tls.leaf_ips.join(', ')}</dd>
					{/if}
				</dl>
			{/if}

			<div class="flex flex-wrap gap-3">
				<a class="btn-primary" href="/setup-ca.crt" download="knot-root-ca.crt">
					<i class="bi bi-download"></i>
					{$_('security.download_root')}
				</a>
				<button class="btn-ghost" disabled={regenerating} onclick={regenerateLeaf}>
					{#if regenerating}
						<span class="spinner"></span>
						{$_('security.regenerating')}
					{:else}
						<i class="bi bi-arrow-clockwise"></i>
						{$_('security.regenerate')}
					{/if}
				</button>
			</div>
			<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-3">{$_('security.install_help')}</p>
		</section>
	{/if}

	{/if}

	{#if activeTab === 'more'}
	<!-- Channel scanner -->
	{#if status && status.role === 'wifi-router'}
		<section class="surface p-5">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-graph-up text-brand-500"></i>
				{$_('channels.section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">{$_('channels.subtitle')}</p>

			<div class="flex flex-wrap gap-3 mb-4">
				<button class="btn-primary" onclick={scanChannels} disabled={chScanning || chApplying}>
					{#if chScanning}
						<span class="spinner"></span>
						{$_('channels.scanning')}
					{:else}
						<i class="bi bi-search"></i>
						{$_('channels.scan')}
					{/if}
				</button>
			</div>

			{#if chReport}
				{@const max = chReport.channels.reduce((m, c) => Math.max(m, c.score), 0.001)}
				<div class="space-y-1">
					{#each chReport.channels as c}
						<div class="flex items-center gap-3">
							<span class="w-6 text-right text-xs font-mono tabular-nums text-zinc-500">
								{c.channel}
							</span>
							<div class="flex-1 h-5 bg-zinc-100 dark:bg-zinc-800 rounded overflow-hidden">
								<div
									class="h-full transition-all
										{c.recommended
											? 'bg-emerald-400'
											: c.channel === chReport?.current_channel
												? 'bg-brand-400'
												: 'bg-rose-400 opacity-60'}"
									style="width: {(c.score / max) * 100}%"
								></div>
							</div>
							<span class="text-xs text-zinc-500 w-16 text-right tabular-nums">
								{c.networks > 0 ? `${c.networks} AP` : '—'}
							</span>
							{#if c.recommended}
								<span class="badge badge-ok">{$_('channels.recommended')}</span>
							{:else if c.channel === chReport.current_channel}
								<span class="badge badge-info">{$_('channels.current')}</span>
							{/if}
						</div>
					{/each}
				</div>

				{#if chReport.recommended !== chReport.current_channel}
					<div class="mt-4 flex flex-wrap items-center gap-3">
						<button
							class="btn-primary"
							disabled={chApplying}
							onclick={() => applyChannel(chReport!.recommended)}
						>
							{#if chApplying}
								<span class="spinner"></span>
								{$_('channels.applying')}
							{:else}
								<i class="bi bi-arrow-right-circle"></i>
								{$_('channels.apply', { values: { channel: chReport.recommended } })}
							{/if}
						</button>
						<p class="text-xs text-zinc-500 dark:text-zinc-400 max-w-md">
							{$_('channels.apply_help')}
						</p>
					</div>
				{:else}
					<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-4">
						{$_('channels.already_optimal')}
					</p>
				{/if}
			{/if}

			{#if chError}
				<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
					<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
					<span>{chError}</span>
				</div>
			{/if}
		</section>
	{/if}

	<!-- Notifications / Telegram bot -->
	{#if notifyState}
		<section class="surface p-5">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-send text-brand-500"></i>
				{$_('notify.section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">{$_('notify.subtitle')}</p>

			{#if !notifyState.bot_configured}
				<!-- First-time configuration: paste a bot token. -->
				<div class="space-y-3">
					<p class="text-sm">
						{$_('notify.step1_title')}
					</p>
					<ol class="text-xs text-zinc-500 dark:text-zinc-400 space-y-1 list-decimal pl-5">
						<li>{$_('notify.step1_botfather')}</li>
						<li>{$_('notify.step1_paste')}</li>
					</ol>
					<input
						class="input font-mono text-xs"
						type="password"
						placeholder="1234567890:ABC..."
						bind:value={notifyTokenInput}
						disabled={notifySaving}
					/>
					<div class="flex flex-wrap gap-2">
						<button
							class="btn-primary"
							disabled={notifySaving || !notifyTokenInput.trim()}
							onclick={saveNotifyToken}
						>
							{#if notifySaving}
								<span class="spinner"></span>
							{:else}
								<i class="bi bi-check2"></i>
							{/if}
							{$_('notify.save_token')}
						</button>
					</div>
				</div>
			{:else}
				<!-- Configured: show status, link button, chat list. -->
				<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm mb-4">
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('notify.bot')}</dt>
					<dd class="font-mono">
						{#if notifyState.bot_username}
							<a
								class="text-brand-600 dark:text-brand-400 hover:underline"
								href={`https://t.me/${notifyState.bot_username}`}
								target="_blank"
								rel="noreferrer"
							>
								@{notifyState.bot_username}
							</a>
						{:else}
							{$_('notify.bot_starting')}
						{/if}
					</dd>
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('notify.primary_lang')}</dt>
					<dd>
						<button
							type="button"
							class="text-xs px-2 py-0.5 rounded {notifyState.primary_lang === 'ru' ? 'bg-brand-500 text-white' : 'bg-zinc-100 dark:bg-zinc-800'}"
							onclick={() => setPrimaryLang('ru')}
						>
							🇷🇺 RU
						</button>
						<button
							type="button"
							class="text-xs px-2 py-0.5 rounded ml-1 {notifyState.primary_lang === 'en' ? 'bg-brand-500 text-white' : 'bg-zinc-100 dark:bg-zinc-800'}"
							onclick={() => setPrimaryLang('en')}
						>
							🇬🇧 EN
						</button>
					</dd>
				</dl>

				{#if notifyPIN && pinRemaining > 0}
					<div class="surface-muted p-4 mb-4">
						<div class="text-xs uppercase tracking-wide text-zinc-500 dark:text-zinc-400 mb-1">
							{$_('notify.pin_title')}
						</div>
						<div class="text-3xl font-mono font-bold tabular-nums">{notifyPIN.pin}</div>
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-2">
							{$_('notify.pin_help', { values: { sec: pinRemaining } })}
						</p>
					</div>
				{/if}

				<button class="btn-ghost mb-4" onclick={issuePIN}>
					<i class="bi bi-link-45deg"></i>
					{$_('notify.link_button')}
				</button>

				<h3 class="font-semibold text-sm mb-2">
					{$_('notify.linked_chats')}
					<span class="text-zinc-400">({notifyState.chats.length})</span>
				</h3>
				{#if notifyState.chats.length === 0}
					<p class="text-xs text-zinc-500 dark:text-zinc-400">{$_('notify.no_chats')}</p>
				{:else}
					<ul class="surface-muted divide-y divide-zinc-200 dark:divide-zinc-700/50">
						{#each notifyState.chats as c (c.chat_id)}
							<li class="flex items-center gap-3 px-3 py-2 text-sm">
								<i class="bi bi-person-circle text-zinc-400 text-lg"></i>
								<div class="flex-1 min-w-0">
									<div class="font-medium truncate">
										{c.username ? '@' + c.username : c.first_name || c.chat_id}
									</div>
									<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono">
										{c.chat_id} · {c.lang}
									</div>
								</div>
								<button
									class="text-zinc-400 hover:text-rose-500"
									title={$_('notify.unlink')}
									onclick={() => unlinkChat(c.chat_id)}
								>
									<i class="bi bi-x-circle"></i>
								</button>
							</li>
						{/each}
					</ul>
				{/if}

				<!-- MTProto transport: app_id/app_hash (works where the Bot
				     API is blocked, dialing via the local Telegram proxy) -->
				<div class="mt-5 pt-4 border-t border-zinc-200 dark:border-zinc-800">
					<div class="flex items-center gap-2 mb-1">
						<span class="font-medium text-sm">{$_('notify.mtproto_title')}</span>
						{#if notifyState.app_configured}
							<span class="badge badge-ok text-[10px]">{$_('notify.mtproto_on')}</span>
						{/if}
					</div>
					<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">
						{$_('notify.mtproto_help')}
						{#if !notifyState.proxy_enabled}
							<span class="text-amber-600 dark:text-amber-400 block mt-1">
								<i class="bi bi-exclamation-triangle"></i> {$_('notify.mtproto_no_proxy')}
							</span>
						{/if}
					</p>
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
						<input
							class="input font-mono"
							type="text"
							inputmode="numeric"
							bind:value={appIDInput}
							placeholder={notifyState.app_configured ? String(notifyState.app_id) : 'app_id'}
						/>
						<input
							class="input font-mono"
							type="password"
							bind:value={appHashInput}
							placeholder={notifyState.app_configured ? '••••••••' : 'app_hash'}
						/>
					</div>
					<div class="flex items-center gap-2 mt-3">
						<button class="btn-ghost text-sm" type="button" disabled={appSaving} onclick={saveNotifyApp}>
							{#if appSaving}<span class="spinner"></span>{/if}{$_('notify.mtproto_save')}
						</button>
						{#if notifyState.app_configured}
							<button class="btn-ghost text-sm text-rose-600 dark:text-rose-400" type="button" disabled={appSaving} onclick={clearNotifyApp}>
								{$_('notify.mtproto_clear')}
							</button>
						{/if}
					</div>
				</div>
			{/if}

			{#if notifyError}
				<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
					<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
					<span>{notifyError}</span>
				</div>
			{/if}
		</section>
	{/if}

	{/if}

	{#if activeTab === 'updates'}
	<!-- Updates: GitHub auto + manual upload -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-1 flex items-center gap-2">
			<i class="bi bi-arrow-up-circle text-brand-500"></i>
			{$_('updates.section')}
		</h2>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">
			{$_('updates.subtitle')}
		</p>

		{#if !updAvailable}
			<p class="text-xs text-zinc-500 dark:text-zinc-400 mb-3">
				<i class="bi bi-info-circle"></i>
				{$_('updates.disabled')}
			</p>
		{:else}
			<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm mb-4">
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('updates.current_version')}</dt>
				<dd class="font-mono">{status?.version ?? ''}</dd>
				{#if upd}
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('updates.latest_version')}</dt>
					<dd class="font-mono">
						{upd.latest_version}
						{#if upd.update_available}
							<span class="badge badge-ok ml-2">
								<i class="bi bi-arrow-up"></i>
								{$_('updates.available')}
							</span>
						{:else}
							<span class="badge badge-neutral ml-2">{$_('updates.up_to_date')}</span>
						{/if}
					</dd>
					{#if upd.latest?.published_at}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('updates.published')}</dt>
						<dd>{fmtDate(upd.latest.published_at)}</dd>
					{/if}
					{#if !upd.signing_enabled}
						<dt class="text-zinc-500 dark:text-zinc-400">{$_('updates.signing')}</dt>
						<dd class="text-amber-600 dark:text-amber-400">
							<i class="bi bi-exclamation-triangle"></i>
							{$_('updates.signing_off')}
						</dd>
					{/if}
				{/if}
			</dl>

			<div class="flex flex-wrap gap-3">
				<button class="btn-ghost" disabled={updChecking || updApplying} onclick={checkUpdate}>
					{#if updChecking}
						<span class="spinner"></span>
						{$_('updates.checking')}
					{:else}
						<i class="bi bi-arrow-repeat"></i>
						{$_('updates.check')}
					{/if}
				</button>
				{#if upd?.update_available}
					<button class="btn-primary" disabled={updApplying} onclick={applyUpdate}>
						{#if updApplying}
							<span class="spinner"></span>
							{$_('updates.applying')}
						{:else}
							<i class="bi bi-cloud-download"></i>
							{$_('updates.install', { values: { version: upd.latest_version } })}
						{/if}
					</button>
				{/if}
			</div>

			{#if updError}
				<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
					<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
					<span>{updError}</span>
				</div>
			{/if}

			{#if upd?.latest?.notes}
				<details class="mt-4 text-sm">
					<summary class="cursor-pointer text-zinc-600 dark:text-zinc-300 hover:text-brand-500">
						{$_('updates.release_notes')}
					</summary>
					<pre class="mt-2 p-3 rounded-lg bg-zinc-100 dark:bg-zinc-800/60 text-xs whitespace-pre-wrap font-mono overflow-x-auto">{upd.latest.notes}</pre>
				</details>
			{/if}
		{/if}

		<!-- Manual upload (developer path) -->
		<details class="mt-5 text-sm">
			<summary class="cursor-pointer text-zinc-500 dark:text-zinc-400 hover:text-brand-500">
				{$_('updates.manual_section')}
			</summary>
			<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-2 mb-3">
				{$_('updates.manual_help')}
			</p>
			<input
				bind:this={fileInput}
				type="file"
				accept=""
				onchange={pickBinary}
				class="hidden"
			/>
			<input
				bind:this={sigInput}
				type="file"
				accept=".sig"
				onchange={pickSignature}
				class="hidden"
			/>
			<div class="flex flex-wrap items-center gap-2">
				<button class="btn-ghost" disabled={busy === 'update'} onclick={() => fileInput?.click()}>
					<i class="bi bi-file-earmark-binary"></i>
					{binFile ? binFile.name : $_('system.update_choose')}
				</button>
				<button class="btn-ghost" disabled={busy === 'update'} onclick={() => sigInput?.click()}>
					<i class="bi bi-file-earmark-lock"></i>
					{sigFile ? sigFile.name : $_('system.update_choose_sig')}
				</button>
				<button
					class="btn-primary"
					disabled={!binFile || busy === 'update'}
					onclick={uploadUpdate}
				>
					{#if busy === 'update'}
						<span class="spinner"></span>
						{$_('system.update_uploading')}
					{:else}
						<i class="bi bi-upload"></i>
						{$_('system.update_install')}
					{/if}
				</button>
			</div>
			<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-2">
				{$_('system.update_sig_hint')}
			</p>
		</details>

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

	{/if}

	{#if activeTab === 'security'}
	<!-- Rescue keypair -->
	{#if rescueAvailable}
		<section class="surface p-5">
			<h2 class="font-semibold mb-1 flex items-center gap-2">
				<i class="bi bi-key text-brand-500"></i>
				{$_('rescue.section')}
			</h2>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-3">
				{$_('rescue.subtitle')}
			</p>

			{#if rescue}
				<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm mb-3">
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('rescue.public_key')}</dt>
					<dd class="font-mono break-all text-xs">{rescue.public_key}</dd>
					<dt class="text-zinc-500 dark:text-zinc-400">{$_('rescue.status')}</dt>
					<dd>
						{#if rescue.private_available}
							<span class="badge badge-warn">
								<i class="bi bi-eye"></i>
								{$_('rescue.status_unrevealed')}
							</span>
						{:else}
							<span class="badge badge-ok">
								<i class="bi bi-shield-check"></i>
								{$_('rescue.status_revealed')}
							</span>
						{/if}
					</dd>
				</dl>
			{/if}

			{#if rescue?.private_available}
				<button class="btn-primary" disabled={revealing} onclick={revealRescue}>
					{#if revealing}
						<span class="spinner"></span>
						{$_('rescue.revealing')}
					{:else}
						<i class="bi bi-eye"></i>
						{$_('rescue.reveal')}
					{/if}
				</button>
				<p class="text-xs text-amber-600 dark:text-amber-400 mt-2">
					<i class="bi bi-exclamation-triangle"></i>
					{$_('rescue.warning_one_shot')}
				</p>
			{:else if rescue}
				<p class="text-xs text-zinc-500 dark:text-zinc-400">
					<i class="bi bi-info-circle"></i>
					{$_('rescue.already_revealed_help')}
				</p>
			{/if}

			{#if revealed}
				<div class="mt-4 p-4 rounded-lg bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/30">
					<p class="text-sm text-amber-900 dark:text-amber-200 mb-2">
						<i class="bi bi-exclamation-triangle-fill"></i>
						{revealed.warning}
					</p>
					<div class="text-xs text-zinc-500 dark:text-zinc-400 mb-1">{$_('rescue.private_key_label')}</div>
					<code class="block p-2 rounded bg-white dark:bg-zinc-900 font-mono text-xs break-all select-all">{revealed.private_key}</code>
					<button
						class="btn-ghost mt-2 text-xs"
						onclick={() => copyToClipboard(revealed!.private_key)}
					>
						<i class="bi bi-clipboard"></i>
						{$_('rescue.copy')}
					</button>
				</div>
			{/if}

			{#if rescueError}
				<div class="mt-3 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
					<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
					<span>{rescueError}</span>
				</div>
			{/if}
		</section>
	{/if}

	{/if}

	{#if activeTab === 'overview'}
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
	{/if}
	</div>
	{/key}
</div>
