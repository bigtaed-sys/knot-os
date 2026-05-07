<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, apiPut, apiDelete, ApiError } from '$lib/api';
	import { relativeTime } from '$lib/format';
	import type {
		Subscription,
		SubscriptionsResponse,
		SubscriptionRefreshResponse,
		RoutingResponse,
		RoutingDevice,
		Profile,
		ProfilesResponse,
		Device,
		DevicesResponse
	} from '$lib/types';

	// --- state -----------------------------------------------------------------

	let subs = $state<Subscription[]>([]);
	let routing = $state<RoutingResponse>({ devices: [], missing_outbounds: [] });
	let profiles = $state<Profile[]>([]);
	let devices = $state<Device[]>([]);

	let available = $state(true);
	let loading = $state(true);
	let error = $state<string | null>(null);

	// Per-subscription refresh state — avoids one button spinning out the
	// whole page.
	let refreshing = $state<Record<string, boolean>>({});

	// Modal state.
	let showAddSub = $state(false);
	let addSubURL = $state('');
	let addSubName = $state('');
	let addSubUA = $state('');
	let addSubBusy = $state(false);
	let addSubError = $state<string | null>(null);

	let showAddURI = $state(false);
	let addURIText = $state('');
	let addURIBusy = $state(false);
	let addURIError = $state<string | null>(null);

	let pickingFor = $state<Profile | null>(null);

	let timer: ReturnType<typeof setInterval> | null = null;

	// --- loaders ---------------------------------------------------------------

	async function load(initial = false) {
		try {
			const r = await apiGet<SubscriptionsResponse>('/subscriptions');
			subs = r.subscriptions;
			available = true;
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
			const r = await apiGet<RoutingResponse>('/routing');
			routing = r;
		} catch (e) {
			if (!(e instanceof ApiError && e.status === 503)) {
				error = e instanceof Error ? e.message : String(e);
			}
		}
		if (initial) {
			try {
				const r = await apiGet<ProfilesResponse>('/profiles');
				profiles = r.profiles;
			} catch {
				/* non-fatal */
			}
			try {
				const r = await apiGet<DevicesResponse>('/devices');
				devices = r.devices;
			} catch {
				/* non-fatal */
			}
		}
		loading = false;
	}

	onMount(() => {
		load(true);
		// Routing decisions can change underneath us when the
		// scheduler kicks in, so we refresh on a slow interval.
		timer = setInterval(() => load(false), 15000);
	});

	onDestroy(() => {
		if (timer) clearInterval(timer);
	});

	// --- actions ---------------------------------------------------------------

	async function addSubscription() {
		addSubError = null;
		if (!addSubURL.trim()) {
			addSubError = $_('routing.err_url_required');
			return;
		}
		if (!addSubName.trim()) {
			addSubError = $_('routing.err_name_required');
			return;
		}
		addSubBusy = true;
		try {
			await apiPost('/subscriptions', {
				url: addSubURL.trim(),
				display_name: addSubName.trim(),
				user_agent: addSubUA.trim() || undefined
			});
			showAddSub = false;
			addSubURL = '';
			addSubName = '';
			addSubUA = '';
			await load(false);
		} catch (e) {
			addSubError = e instanceof Error ? e.message : String(e);
		} finally {
			addSubBusy = false;
		}
	}

	async function addManualURI() {
		addURIError = null;
		if (!addURIText.trim()) {
			addURIError = $_('routing.err_uri_required');
			return;
		}
		addURIBusy = true;
		try {
			await apiPost('/subscriptions/manual/uris', { uri: addURIText.trim() });
			showAddURI = false;
			addURIText = '';
			await load(false);
		} catch (e) {
			addURIError = e instanceof Error ? e.message : String(e);
		} finally {
			addURIBusy = false;
		}
	}

	async function refreshSub(id: string) {
		refreshing[id] = true;
		try {
			const r = await apiPost<SubscriptionRefreshResponse>(
				`/subscriptions/${id}/refresh`,
				{},
				{ timeoutMs: 30000 }
			);
			// Surface the latest snapshot directly instead of re-loading
			// the whole world (avoids flicker).
			subs = subs.map((s) => (s.id === id ? r.subscription : s));
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		} finally {
			refreshing[id] = false;
		}
		// Routing may have changed too — pull it.
		try {
			routing = await apiGet<RoutingResponse>('/routing');
		} catch {
			/* non-fatal */
		}
	}

	async function removeSub(s: Subscription) {
		if (!confirm($_('routing.remove_subscription_confirm', { values: { name: s.display_name } })))
			return;
		try {
			await apiDelete(`/subscriptions/${s.id}`);
			await load(false);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function removeManualServer(srvID: string) {
		if (!confirm($_('routing.remove_uri_confirm'))) return;
		try {
			await apiDelete(`/subscriptions/manual/uris/${srvID}`);
			await load(false);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	async function pickServer(p: Profile, tag: string) {
		try {
			// PUT requires the full profile body. Mutate just route_via
			// and round-trip everything else.
			await apiPut(`/profiles/${p.id}`, { ...p, route_via: tag });
			pickingFor = null;
			await load(false);
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	// --- helpers ---------------------------------------------------------------

	function tagLabel(tag: string): string {
		// "<sub-id>:<srv-id>" → human label = sub.display_name + server.display_name
		if (!tag) return $_('routing.server_direct');
		const [subID, srvID] = tag.split(':');
		const sub = subs.find((s) => s.id === subID);
		if (!sub) return tag;
		const srv = sub.servers.find((s) => s.id === srvID);
		if (!srv) return `${sub.display_name} · ?`;
		return `${sub.display_name} · ${srv.display_name || srv.id}`;
	}

	function statusBadge(status: RoutingDevice['status']) {
		switch (status) {
			case 'tunnel':
				return {
					label: $_('routing.status_tunnel'),
					cls: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400',
					icon: 'bi-shield-lock-fill'
				};
			case 'kill':
				return {
					label: $_('routing.status_kill'),
					cls: 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-400',
					icon: 'bi-x-octagon-fill'
				};
			default:
				return {
					label: $_('routing.status_direct'),
					cls: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400',
					icon: 'bi-arrow-right-circle'
				};
		}
	}

	function deviceLabel(mac: string): string {
		const d = devices.find((d) => d.mac === mac);
		if (!d) return mac;
		return d.display_name || d.hostname || `${mac.slice(-5)}`;
	}

	function profileLabel(id: string): string {
		const p = profiles.find((p) => p.id === id);
		if (!p) return id || '—';
		return p.name;
	}

	const profilesNonGuest = $derived(profiles.filter((p) => p.id !== 'guest'));
</script>

<svelte:head>
	<title>{$_('routing.title')} · KnotOS</title>
</svelte:head>

<div class="max-w-6xl mx-auto p-6 space-y-6">
	<header class="space-y-1">
		<h1 class="text-2xl font-semibold">{$_('routing.title')}</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 max-w-3xl">{$_('routing.subtitle')}</p>
	</header>

	{#if !available}
		<div class="surface p-6 text-sm text-zinc-500">{$_('routing.disabled')}</div>
	{:else if error}
		<div class="surface border-red-300 dark:border-red-500/30 p-4 text-sm">
			<i class="bi bi-exclamation-triangle text-red-500 mr-2"></i>{error}
		</div>
	{:else if loading}
		<div class="surface p-6 flex items-center gap-3 text-zinc-500">
			<div class="spinner"></div>
			{$_('common.loading')}
		</div>
	{:else}
		<!-- Top-line warning when servers are missing. -->
		{#if routing.missing_outbounds.length > 0}
			<div class="surface border-red-300 dark:border-red-500/30 p-4">
				<div class="flex items-start gap-3">
					<i class="bi bi-shield-exclamation text-red-500 text-xl mt-0.5"></i>
					<div class="text-sm">
						<div class="font-medium text-red-700 dark:text-red-400">
							{$_('routing.missing_outbounds', {
								values: { count: routing.missing_outbounds.length }
							})}
						</div>
						<div class="mt-1 text-zinc-600 dark:text-zinc-400 font-mono text-xs">
							{routing.missing_outbounds.join(' · ')}
						</div>
					</div>
				</div>
			</div>
		{/if}

		<!-- Add buttons -->
		<div class="flex flex-wrap gap-2">
			<button class="btn btn-primary" onclick={() => (showAddSub = true)}>
				<i class="bi bi-plus-circle"></i>
				{$_('routing.add_subscription')}
			</button>
			<button class="btn btn-ghost" onclick={() => (showAddURI = true)}>
				<i class="bi bi-link-45deg"></i>
				{$_('routing.add_uri')}
			</button>
		</div>

		<!-- Subscriptions list -->
		{#if subs.length === 1 && subs[0].id === 'manual' && subs[0].servers.length === 0}
			<div class="surface p-6 text-center">
				<div
					class="w-14 h-14 rounded-full bg-brand-100 dark:bg-brand-500/15 mx-auto flex items-center justify-center mb-3"
				>
					<i class="bi bi-globe2 text-brand-600 dark:text-brand-400 text-2xl"></i>
				</div>
				<div class="font-medium">{$_('routing.empty_title')}</div>
				<div class="text-sm text-zinc-500 mt-1 max-w-md mx-auto">{$_('routing.empty_help')}</div>
			</div>
		{:else}
			<div class="space-y-4">
				{#each subs as sub (sub.id)}
					<section class="surface p-4 space-y-3">
						<header class="flex items-start gap-3">
							<div
								class="w-10 h-10 rounded-lg flex items-center justify-center shrink-0
								{sub.id === 'manual'
									? 'bg-zinc-100 dark:bg-zinc-800 text-zinc-500'
									: 'bg-brand-100 dark:bg-brand-500/15 text-brand-600 dark:text-brand-400'}"
							>
								<i class="bi {sub.id === 'manual' ? 'bi-link-45deg' : 'bi-cloud-download'} text-lg"
								></i>
							</div>
							<div class="flex-1 min-w-0">
								<div class="font-medium truncate">{sub.display_name}</div>
								<div class="text-xs text-zinc-500 mt-0.5">
									{$_('routing.servers_count', { values: { count: sub.servers.length } })}
									{#if sub.id === 'manual'}
										· {$_('routing.manual_help')}
									{:else if sub.last_fetched}
										· {$_('routing.last_fetched', {
											values: { when: relativeTime(sub.last_fetched) }
										})}
									{:else}
										· {$_('routing.never_fetched')}
									{/if}
								</div>
								{#if sub.last_error}
									<div class="text-xs text-red-600 dark:text-red-400 mt-1">
										<i class="bi bi-exclamation-triangle mr-1"></i>
										{$_('routing.fetch_error', { values: { err: sub.last_error } })}
									</div>
								{/if}
							</div>
							<div class="flex gap-1 shrink-0">
								{#if sub.id !== 'manual'}
									<button
										class="btn btn-ghost text-sm py-1 px-3"
										onclick={() => refreshSub(sub.id)}
										disabled={refreshing[sub.id]}
									>
										{#if refreshing[sub.id]}
											<span class="spinner"></span>{$_('routing.refreshing')}
										{:else}
											<i class="bi bi-arrow-clockwise"></i>{$_('routing.refresh')}
										{/if}
									</button>
									<button
										class="btn btn-ghost text-sm py-1 px-2 text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10"
										onclick={() => removeSub(sub)}
										title={$_('routing.remove_subscription_confirm', {
											values: { name: sub.display_name }
										})}
									>
										<i class="bi bi-trash"></i>
									</button>
								{/if}
							</div>
						</header>

						<!-- Server list -->
						{#if sub.servers.length > 0}
							<ul class="divide-y divide-zinc-200 dark:divide-zinc-800 -mx-1">
								{#each sub.servers as srv (srv.id)}
									<li class="px-1 py-2 flex items-center gap-3 text-sm">
										<i class="bi bi-server text-zinc-400 shrink-0"></i>
										<div class="flex-1 min-w-0">
											<div class="truncate">{srv.display_name || srv.id}</div>
											<div class="text-xs text-zinc-500 truncate font-mono">
												{srv.outbound.type}
												{#if srv.outbound.server}· {srv.outbound.server}:{srv.outbound.port}{/if}
											</div>
										</div>
										{#if sub.id === 'manual'}
											<button
												class="btn btn-ghost text-sm py-1 px-2 text-red-600"
												onclick={() => removeManualServer(srv.id)}
												title={$_('routing.remove_uri_confirm')}
											>
												<i class="bi bi-trash"></i>
											</button>
										{/if}
									</li>
								{/each}
							</ul>
						{/if}
					</section>
				{/each}
			</div>
		{/if}

		<!-- Profiles → server assignment -->
		<section class="surface p-4 space-y-3">
			<header>
				<h2 class="font-medium">{$_('routing.profiles_section')}</h2>
				<p class="text-xs text-zinc-500 mt-0.5">{$_('routing.profiles_help')}</p>
			</header>
			<ul class="divide-y divide-zinc-200 dark:divide-zinc-800 -mx-2">
				{#each profilesNonGuest as p (p.id)}
					{@const isMissing = p.route_via && routing.missing_outbounds.includes(p.route_via)}
					<li class="px-2 py-2 flex items-center gap-3">
						<i class="bi bi-shield-check text-zinc-400 shrink-0"></i>
						<div class="flex-1 min-w-0">
							<div class="font-medium truncate">{p.name}</div>
							<div class="text-xs text-zinc-500 truncate flex items-center gap-1">
								<i class="bi {p.route_via ? 'bi-arrow-right-short' : 'bi-arrow-right'}"></i>
								{#if p.route_via}
									<span class:text-red-600={isMissing} class:font-mono={true}>
										{tagLabel(p.route_via)}
									</span>
									{#if isMissing}
										<i class="bi bi-exclamation-triangle text-red-500"></i>
									{/if}
								{:else}
									<span>{$_('routing.server_direct')}</span>
								{/if}
							</div>
						</div>
						<button class="btn btn-ghost text-sm py-1 px-3" onclick={() => (pickingFor = p)}>
							{$_('routing.server_pick')}
						</button>
					</li>
				{/each}
			</ul>
		</section>

		<!-- Devices status -->
		<section class="surface p-4 space-y-3">
			<header>
				<h2 class="font-medium">{$_('routing.devices_section')}</h2>
				<p class="text-xs text-zinc-500 mt-0.5">{$_('routing.devices_help')}</p>
			</header>
			{#if routing.devices.length === 0}
				<div class="text-sm text-zinc-500 py-4 text-center">—</div>
			{:else}
				<ul class="divide-y divide-zinc-200 dark:divide-zinc-800 -mx-2">
					{#each routing.devices as d (d.mac)}
						{@const badge = statusBadge(d.status)}
						<li class="px-2 py-2 flex items-center gap-3 text-sm">
							<i class="bi bi-laptop text-zinc-400 shrink-0"></i>
							<div class="flex-1 min-w-0">
								<div class="truncate font-medium">{deviceLabel(d.mac)}</div>
								<div class="text-xs text-zinc-500 truncate">
									{profileLabel(d.profile_id)}
									{#if d.outbound !== 'direct' && d.outbound !== 'block'}
										· <span class="font-mono">{tagLabel(d.outbound)}</span>
									{/if}
								</div>
							</div>
							<span class="text-xs px-2 py-1 rounded-md flex items-center gap-1 shrink-0 {badge.cls}">
								<i class="bi {badge.icon}"></i>
								{badge.label}
							</span>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/if}
</div>

<!-- Add-subscription modal -->
{#if showAddSub}
	<div
		class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
		role="presentation"
		onclick={(e) => {
			if (e.target === e.currentTarget) showAddSub = false;
		}}
	>
		<div class="surface p-5 max-w-md w-full">
			<h2 class="text-lg font-semibold mb-4">{$_('routing.add_subscription_title')}</h2>
			<div class="space-y-3">
				<label class="block">
					<span class="text-xs text-zinc-500">{$_('routing.add_url_label')}</span>
					<input
						type="url"
						bind:value={addSubURL}
						placeholder={$_('routing.add_url_placeholder')}
						class="input mt-1 font-mono text-sm"
					/>
				</label>
				<label class="block">
					<span class="text-xs text-zinc-500">{$_('routing.add_name_label')}</span>
					<input
						type="text"
						bind:value={addSubName}
						placeholder={$_('routing.add_name_placeholder')}
						class="input mt-1"
					/>
				</label>
				<label class="block">
					<span class="text-xs text-zinc-500">{$_('routing.add_ua_label')}</span>
					<input
						type="text"
						bind:value={addSubUA}
						placeholder={$_('routing.add_ua_placeholder')}
						class="input mt-1 font-mono text-sm"
					/>
					<span class="text-xs text-zinc-500 mt-1 block">{$_('routing.add_ua_help')}</span>
				</label>
				{#if addSubError}
					<div class="text-xs text-red-600">{addSubError}</div>
				{/if}
			</div>
			<div class="flex justify-end gap-2 mt-5">
				<button class="btn btn-ghost" onclick={() => (showAddSub = false)} disabled={addSubBusy}>
					{$_('common.cancel')}
				</button>
				<button class="btn btn-primary" onclick={addSubscription} disabled={addSubBusy}>
					{#if addSubBusy}
						<span class="spinner"></span>{$_('common.saving')}
					{:else}
						{$_('common.save')}
					{/if}
				</button>
			</div>
		</div>
	</div>
{/if}

<!-- Add-URI modal -->
{#if showAddURI}
	<div
		class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
		role="presentation"
		onclick={(e) => {
			if (e.target === e.currentTarget) showAddURI = false;
		}}
	>
		<div class="surface p-5 max-w-md w-full">
			<h2 class="text-lg font-semibold mb-4">{$_('routing.add_uri_title')}</h2>
			<div class="space-y-3">
				<label class="block">
					<span class="text-xs text-zinc-500">{$_('routing.add_uri_label')}</span>
					<textarea
						bind:value={addURIText}
						placeholder={$_('routing.add_uri_placeholder')}
						class="input mt-1 font-mono text-xs h-24"
					></textarea>
					<span class="text-xs text-zinc-500 mt-1 block">{$_('routing.add_uri_help')}</span>
				</label>
				{#if addURIError}
					<div class="text-xs text-red-600">{addURIError}</div>
				{/if}
			</div>
			<div class="flex justify-end gap-2 mt-5">
				<button class="btn btn-ghost" onclick={() => (showAddURI = false)} disabled={addURIBusy}>
					{$_('common.cancel')}
				</button>
				<button class="btn btn-primary" onclick={addManualURI} disabled={addURIBusy}>
					{#if addURIBusy}
						<span class="spinner"></span>{$_('common.saving')}
					{:else}
						{$_('common.save')}
					{/if}
				</button>
			</div>
		</div>
	</div>
{/if}

<!-- Server picker -->
{#if pickingFor}
	<div
		class="fixed inset-0 z-50 bg-black/50 flex items-center justify-center p-4"
		role="presentation"
		onclick={(e) => {
			if (e.target === e.currentTarget) pickingFor = null;
		}}
	>
		<div class="surface p-5 max-w-lg w-full">
			<h2 class="text-lg font-semibold mb-4">
				{$_('routing.server_picker_title', { values: { profile: pickingFor.name } })}
			</h2>
			<div class="space-y-1 max-h-96 overflow-y-auto -mx-1 pr-1">
				<!-- Direct option -->
				<button
					type="button"
					class="w-full text-left px-3 py-2 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 flex items-center gap-3"
					class:bg-brand-50={!pickingFor.route_via}
					class:dark:bg-brand-500-15={!pickingFor.route_via}
					onclick={() => pickingFor && pickServer(pickingFor, '')}
				>
					<i class="bi bi-arrow-right-circle text-zinc-400"></i>
					<div class="flex-1 min-w-0">
						<div class="font-medium">{$_('routing.server_direct')}</div>
						<div class="text-xs text-zinc-500">{$_('routing.server_direct_help')}</div>
					</div>
					{#if !pickingFor.route_via}
						<i class="bi bi-check2 text-brand-500"></i>
					{/if}
				</button>

				{#each subs as sub (sub.id)}
					{#if sub.servers.length > 0}
						<div class="px-3 pt-3 pb-1 text-xs uppercase tracking-wide text-zinc-400">
							{sub.display_name}
						</div>
						{#each sub.servers as srv (srv.id)}
							{@const tag = `${sub.id}:${srv.id}`}
							{@const selected = pickingFor.route_via === tag}
							<button
								type="button"
								class="w-full text-left px-3 py-2 rounded-md hover:bg-zinc-100 dark:hover:bg-zinc-800 flex items-center gap-3"
								class:bg-brand-50={selected}
								class:dark:bg-brand-500-15={selected}
								onclick={() => pickingFor && pickServer(pickingFor, tag)}
							>
								<i class="bi bi-server text-zinc-400"></i>
								<div class="flex-1 min-w-0">
									<div class="font-medium truncate">{srv.display_name || srv.id}</div>
									<div class="text-xs text-zinc-500 truncate font-mono">
										{srv.outbound.type}
										{#if srv.outbound.server}· {srv.outbound.server}:{srv.outbound.port}{/if}
									</div>
								</div>
								{#if selected}
									<i class="bi bi-check2 text-brand-500"></i>
								{/if}
							</button>
						{/each}
					{/if}
				{/each}
			</div>
			<div class="flex justify-end mt-4">
				<button class="btn btn-ghost" onclick={() => (pickingFor = null)}>
					{$_('common.cancel')}
				</button>
			</div>
		</div>
	</div>
{/if}
