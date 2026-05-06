<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, ApiError, ApiTimeoutError, API_BASE } from '$lib/api';
	import type {
		SystemStatus,
		TLSInfo,
		UpdateCheckResult,
		RescueInfo,
		RescueRevealed,
		ChannelReport
	} from '$lib/types';

	let status = $state<SystemStatus | null>(null);
	let tls = $state<TLSInfo | null>(null);
	let tlsAvailable = $state(true);
	let regenerating = $state(false);
	let busy = $state<string | null>(null);
	let updateMsg = $state<{ kind: 'ok' | 'err'; text: string } | null>(null);
	let fileInput = $state<HTMLInputElement | null>(null);

	// GitHub auto-update state.
	let upd = $state<UpdateCheckResult | null>(null);
	let updAvailable = $state(true);
	let updChecking = $state(false);
	let updApplying = $state(false);
	let updError = $state<string | null>(null);

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
				onchange={uploadUpdate}
				class="hidden"
			/>
			<button
				class="btn-ghost"
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
