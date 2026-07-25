<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { fade } from 'svelte/transition';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, apiPatch, apiDelete, ApiError } from '$lib/api';
	import { relativeTime } from '$lib/format';
	import Tabs from '$lib/components/Tabs.svelte';
	import type {
		VPNServer,
		VPNPeer,
		VPNPeersResponse,
		VPNAddPeerResponse,
		Profile,
		ProfilesResponse
	} from '$lib/types';

	let activeTab = $state('server');
	const tabList = $derived([
		{ id: 'server', label: $_('vpn.tab_server'), icon: 'bi-router' },
		{ id: 'peers', label: $_('vpn.tab_peers'), icon: 'bi-people' }
	]);

	let server = $state<VPNServer | null>(null);
	let peers = $state<VPNPeer[]>([]);
	let profiles = $state<Profile[]>([]);
	let available = $state(true);
	let error = $state<string | null>(null);
	let timer: ReturnType<typeof setInterval> | null = null;

	// Add-peer modal state.
	let showAdd = $state(false);
	let newName = $state('');
	let newProfile = $state('');
	let newFullTunnel = $state(true);
	let creating = $state(false);
	let addError = $state<string | null>(null);

	// One-time delivery modal: shown after a successful AddPeer.
	// Once user closes, we can't show the private key again.
	let delivered = $state<VPNAddPeerResponse | null>(null);
	let copyFlash = $state(false);

	// Server-edit form state.
	let editingServer = $state(false);
	let formEnabled = $state(false);
	let formPort = $state(51820);
	let formEndpoint = $state('');
	let formCIDR = $state('10.20.30.1/24');
	let savingServer = $state(false);

	async function load(initial = false) {
		try {
			const s = await apiGet<VPNServer>('/vpn/server');
			server = s;
			available = true;
			if (!editingServer) {
				formEnabled = s.enabled;
				formPort = s.listen_port;
				formEndpoint = s.endpoint_host;
				formCIDR = s.interface_cidr;
			}
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			if (e instanceof ApiError && e.status === 503) {
				available = false;
				return;
			}
			error = e instanceof Error ? e.message : String(e);
			return;
		}
		try {
			const r = await apiGet<VPNPeersResponse>('/vpn/peers');
			peers = r.peers;
		} catch {
			// non-fatal
		}
		if (initial) {
			try {
				const r = await apiGet<ProfilesResponse>('/profiles');
				profiles = r.profiles;
			} catch {
				// non-fatal
			}
		}
	}

	onMount(() => {
		load(true);
		// Refresh handshakes every 10s. Cheap, but enough for the
		// "is anyone connected right now?" indicator.
		timer = setInterval(() => load(false), 10000);
	});
	onDestroy(() => {
		if (timer !== null) clearInterval(timer);
	});

	async function saveServer() {
		savingServer = true;
		error = null;
		try {
			await apiPatch<VPNServer>('/vpn/server', {
				enabled: formEnabled,
				listen_port: formPort,
				endpoint_host: formEndpoint.trim(),
				interface_cidr: formCIDR.trim()
			});
			editingServer = false;
			await load();
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				error = e.message;
			}
		} finally {
			savingServer = false;
		}
	}

	function startEdit() {
		if (!server) return;
		formEnabled = server.enabled;
		formPort = server.listen_port;
		formEndpoint = server.endpoint_host;
		formCIDR = server.interface_cidr;
		editingServer = true;
	}

	async function addPeer() {
		const name = newName.trim();
		if (!name) {
			addError = $_('vpn.err_name_required');
			return;
		}
		creating = true;
		addError = null;
		try {
			const res = await apiPost<VPNAddPeerResponse>('/vpn/peers', {
				name,
				profile_id: newProfile,
				full_tunnel: newFullTunnel
			});
			delivered = res;
			newName = '';
			newProfile = '';
			newFullTunnel = true;
			showAdd = false;
			await load();
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				addError = body?.error?.message ?? e.message;
			} else if (e instanceof Error) {
				addError = e.message;
			}
		} finally {
			creating = false;
		}
	}

	async function removePeer(p: VPNPeer) {
		if (!confirm($_('vpn.remove_confirm', { values: { name: p.name } }))) return;
		try {
			await apiDelete(`/vpn/peers/${encodeURIComponent(p.id)}`);
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function setPeerProfile(p: VPNPeer, profileID: string) {
		try {
			await apiPatch(`/vpn/peers/${encodeURIComponent(p.id)}`, { profile_id: profileID });
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function copyConfig() {
		if (!delivered) return;
		try {
			await navigator.clipboard.writeText(delivered.client_config);
			copyFlash = true;
			setTimeout(() => (copyFlash = false), 2000);
		} catch {
			// Clipboard API is HTTP-restricted on insecure origins.
			// User can still select+copy from the visible <pre>.
		}
	}

	function fpShort(pub: string): string {
		// First 8 chars of base64 are enough to disambiguate visually.
		return pub.slice(0, 11) + '…';
	}

	function peerOnline(p: VPNPeer): boolean {
		if (!p.last_handshake) return false;
		const ts = new Date(p.last_handshake).getTime();
		return Date.now() - ts < 3 * 60 * 1000; // 3 min
	}
</script>

<header class="mb-6 flex items-start justify-between gap-3 flex-wrap">
	<div>
		<h1 class="text-2xl font-semibold">{$_('vpn.title')}</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('vpn.subtitle')}</p>
	</div>
	{#if server?.enabled}
		<button class="btn-primary" onclick={() => (showAdd = true)}>
			<i class="bi bi-plus-lg"></i>
			{$_('vpn.add_peer')}
		</button>
	{/if}
</header>

{#if !available}
	<div class="surface p-10 text-center">
		<div class="w-16 h-16 mx-auto rounded-2xl bg-zinc-100 dark:bg-zinc-800 flex items-center justify-center mb-4">
			<i class="bi bi-shield-lock text-zinc-400 dark:text-zinc-500 text-2xl"></i>
		</div>
		<p class="text-zinc-500 dark:text-zinc-400">{$_('vpn.disabled')}</p>
	</div>
{:else if !server}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else}
	<Tabs tabs={tabList} bind:active={activeTab} />

	{#key activeTab}
		<div in:fade={{ duration: 140 }}>
			{#if activeTab === 'server'}
	<!-- Server card -->
	<section class="surface p-5 mb-5">
		<div class="flex items-start justify-between gap-3 mb-4">
			<h2 class="font-semibold flex items-center gap-2">
				<i class="bi bi-router text-brand-500"></i>
				{$_('vpn.server_section')}
			</h2>
			{#if server.enabled}
				<span class="badge badge-ok">
					<span class="dot-live"></span>
					{$_('vpn.up')}
				</span>
			{:else}
				<span class="badge badge-neutral">{$_('vpn.down')}</span>
			{/if}
		</div>

		{#if !editingServer}
			<dl class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-2 text-sm mb-4">
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('vpn.endpoint')}</dt>
				<dd class="font-mono">
					{server.endpoint_host || $_('vpn.endpoint_unset')}:{server.listen_port}
				</dd>
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('vpn.subnet')}</dt>
				<dd class="font-mono">{server.interface_cidr}</dd>
				<dt class="text-zinc-500 dark:text-zinc-400">{$_('vpn.public_key')}</dt>
				<dd class="font-mono break-all text-xs" title={server.public_key}>
					{server.public_key}
				</dd>
			</dl>
			<button class="btn-ghost" onclick={startEdit}>
				<i class="bi bi-pencil"></i>
				{$_('vpn.configure')}
			</button>
			{#if !server.endpoint_host}
				<p class="mt-3 text-xs text-amber-600 dark:text-amber-400">
					<i class="bi bi-exclamation-triangle"></i>
					{$_('vpn.endpoint_help')}
				</p>
			{/if}
		{:else}
			<div class="space-y-3">
				<label class="flex items-center gap-2 text-sm">
					<input type="checkbox" bind:checked={formEnabled} class="rounded text-brand-600" />
					{$_('vpn.enabled_label')}
				</label>
				<div>
					<label class="label" for="ep">{$_('vpn.endpoint_label')}</label>
					<input id="ep" class="input" bind:value={formEndpoint} placeholder="home.example.com" />
					<p class="help">{$_('vpn.endpoint_help')}</p>
				</div>
				<div class="grid grid-cols-2 gap-3">
					<div>
						<label class="label" for="port">{$_('vpn.port_label')}</label>
						<input id="port" type="number" min="1" max="65535" class="input" bind:value={formPort} />
					</div>
					<div>
						<label class="label" for="cidr">{$_('vpn.subnet_label')}</label>
						<input id="cidr" class="input" bind:value={formCIDR} />
					</div>
				</div>
				<div class="flex items-center gap-2">
					<button class="btn-primary" disabled={savingServer} onclick={saveServer}>
						{#if savingServer}
							<span class="spinner"></span>
							{$_('vpn.saving')}
						{:else}
							<i class="bi bi-check2"></i>
							{$_('vpn.save')}
						{/if}
					</button>
					<button class="btn-ghost" disabled={savingServer} onclick={() => (editingServer = false)}>
						{$_('vpn.cancel')}
					</button>
				</div>
			</div>
		{/if}
	</section>
			{/if}

			{#if activeTab === 'peers'}
	<!-- Peers list -->
	<section class="surface p-5">
		<h2 class="font-semibold mb-4 flex items-center gap-2">
			<i class="bi bi-people text-brand-500"></i>
			{$_('vpn.peers_section')}
			<span class="text-xs text-zinc-500 dark:text-zinc-400 font-normal">
				({peers.length})
			</span>
		</h2>

		{#if peers.length === 0}
			<p class="text-sm text-zinc-500 dark:text-zinc-400">
				{$_('vpn.peers_empty')}
			</p>
		{:else}
			<div class="space-y-2">
				{#each peers as p (p.id)}
					{@const online = peerOnline(p)}
					<div class="surface-muted p-3 flex items-center gap-4 flex-wrap">
						<div
							class="
								w-10 h-10 shrink-0 rounded-xl flex items-center justify-center
								{online
									? 'bg-emerald-100 dark:bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'
									: 'bg-zinc-100 dark:bg-zinc-800 text-zinc-400 dark:text-zinc-500'}
							"
						>
							<i class="bi bi-phone text-lg"></i>
						</div>
						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2 flex-wrap">
								<span class="font-semibold truncate">{p.name}</span>
								{#if online}
									<span class="badge badge-ok">
										<span class="dot-live"></span>
										{$_('vpn.peer_online')}
									</span>
								{:else if p.last_handshake}
									<span class="badge badge-neutral">
										{relativeTime(p.last_handshake)}
									</span>
								{:else}
									<span class="badge badge-neutral">{$_('vpn.peer_never')}</span>
								{/if}
							</div>
							<div class="text-xs text-zinc-500 dark:text-zinc-400 font-mono mt-0.5">
								{p.allowed_ip} · {fpShort(p.public_key)}
							</div>
						</div>
						<select
							class="input py-1 text-xs max-w-[12ch]"
							value={p.profile_id ?? ''}
							onchange={(e) => setPeerProfile(p, (e.target as HTMLSelectElement).value)}
						>
							<option value="">{$_('vpn.profile_none')}</option>
							{#each profiles as prof}
								<option value={prof.id}>{prof.name}</option>
							{/each}
						</select>
						<button
							class="btn-ghost text-rose-600 dark:text-rose-400"
							onclick={() => removePeer(p)}
							aria-label={$_('vpn.remove_confirm', { values: { name: p.name } })}
							title={$_('vpn.remove_confirm', { values: { name: p.name } })}
						>
							<i class="bi bi-trash3"></i>
						</button>
					</div>
				{/each}
			</div>
		{/if}
	</section>
			{/if}
		</div>
	{/key}

	{#if error}
		<div class="mt-4 flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
			<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
			<span>{error}</span>
		</div>
	{/if}
{/if}

<!-- Add peer modal -->
{#if showAdd}
	<div
		class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
		role="presentation"
		onclick={(e) => {
			if (e.target === e.currentTarget) showAdd = false;
		}}
	>
		<div class="surface p-5 max-w-md w-full">
			<h3 class="font-semibold text-lg mb-4">{$_('vpn.add_peer_title')}</h3>
			<div class="space-y-3">
				<div>
					<label class="label" for="np">{$_('vpn.peer_name_label')}</label>
					<input id="np" class="input" bind:value={newName} placeholder="Phone" />
				</div>
				<div>
					<label class="label" for="npr">{$_('vpn.peer_profile_label')}</label>
					<select id="npr" class="input" bind:value={newProfile}>
						<option value="">{$_('vpn.profile_none')}</option>
						{#each profiles as prof}
							<option value={prof.id}>{prof.name}</option>
						{/each}
					</select>
				</div>
				<label class="flex items-start gap-2 text-sm">
					<input type="checkbox" bind:checked={newFullTunnel} class="rounded text-brand-600 mt-0.5" />
					<span>
						<span class="font-medium">{$_('vpn.full_tunnel_label')}</span>
						<p class="text-xs text-zinc-500 dark:text-zinc-400">
							{$_('vpn.full_tunnel_help')}
						</p>
					</span>
				</label>
			</div>
			{#if addError}
				<div class="mt-3 text-sm text-red-600 dark:text-red-400">{addError}</div>
			{/if}
			<div class="flex justify-end gap-2 mt-5">
				<button class="btn-ghost" disabled={creating} onclick={() => (showAdd = false)}>
					{$_('vpn.cancel')}
				</button>
				<button class="btn-primary" disabled={creating} onclick={addPeer}>
					{#if creating}
						<span class="spinner"></span>
					{/if}
					{$_('vpn.create')}
				</button>
			</div>
		</div>
	</div>
{/if}

<!-- One-time delivery modal: QR + config + private key -->
{#if delivered}
	<div
		class="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4"
		role="presentation"
	>
		<div class="surface p-5 max-w-lg w-full max-h-[95vh] overflow-y-auto">
			<h3 class="font-semibold text-lg mb-1">
				{$_('vpn.delivery_title', { values: { name: delivered.peer.name } })}
			</h3>
			<p class="text-sm text-zinc-500 dark:text-zinc-400 mb-4">
				{$_('vpn.delivery_help')}
			</p>

			<!-- QR -->
			<div class="bg-white p-3 rounded-xl mb-4 flex justify-center">
				<img
					src={`data:image/png;base64,${delivered.qr_png_base64}`}
					alt="WireGuard QR"
					class="w-64 h-64"
				/>
			</div>

			<details class="mb-4">
				<summary class="cursor-pointer text-sm text-zinc-600 dark:text-zinc-300">
					{$_('vpn.delivery_text_config')}
				</summary>
				<pre class="mt-2 p-3 rounded-lg bg-zinc-100 dark:bg-zinc-800/60 text-[11px] whitespace-pre-wrap font-mono overflow-x-auto">{delivered.client_config}</pre>
				<button class="btn-ghost mt-2" onclick={copyConfig}>
					<i class="bi {copyFlash ? 'bi-check2' : 'bi-clipboard'}"></i>
					{copyFlash ? $_('vpn.copied') : $_('vpn.copy')}
				</button>
			</details>

			<div class="flex items-start gap-2 p-3 rounded-lg bg-amber-50 dark:bg-amber-500/10 text-amber-900 dark:text-amber-300 text-xs mb-4">
				<i class="bi bi-exclamation-triangle mt-0.5 shrink-0"></i>
				<p>{$_('vpn.delivery_warning')}</p>
			</div>

			<div class="flex justify-end">
				<button class="btn-primary" onclick={() => (delivered = null)}>
					<i class="bi bi-check2"></i>
					{$_('vpn.delivery_done')}
				</button>
			</div>
		</div>
	</div>
{/if}
