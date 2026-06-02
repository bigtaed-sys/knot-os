<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPut, ApiError, API_BASE } from '$lib/api';
	import { humanDays } from '$lib/format';
	import type { Profile, ProfilesResponse } from '$lib/types';
	import ScheduleEditor from '$lib/components/ScheduleEditor.svelte';

	let profiles = $state<Profile[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	let editing = $state<Profile | null>(null);
	let saving = $state(false);
	let savedFlash = $state(false);

	// new-profile-id input state for the create form
	let newId = $state('');
	let newIdError = $derived.by(() => {
		if (!newId) return null;
		if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(newId)) return $_('profiles.err_id_format');
		if (profiles.some((p) => p.id === newId)) return $_('profiles.create_id_existing');
		return null;
	});

	async function refresh() {
		loading = true;
		try {
			const r = await apiGet<ProfilesResponse>('/profiles');
			profiles = r.profiles;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			error = e instanceof Error ? e.message : String(e);
		} finally {
			loading = false;
		}
	}

	function startEdit(p: Profile) {
		// Deep-clone for buffered editing.
		editing = JSON.parse(JSON.stringify(p));
		if (!editing!.block_windows) editing!.block_windows = [];
		if (!editing!.dns_blocklists) editing!.dns_blocklists = [];
	}

	function startCreate() {
		if (!newId || newIdError) return;
		editing = {
			id: newId,
			name: newId,
			description: '',
			block_windows: [],
			dns_blocklists: [],
			safe_search: false,
			builtin: false
		};
	}

	function cancelEdit() {
		editing = null;
	}

	async function save() {
		if (!editing) return;
		saving = true;
		error = null;
		try {
			const payload = {
				name: editing.name,
				description: editing.description ?? '',
				block_windows: editing.block_windows ?? [],
				dns_blocklists: editing.dns_blocklists ?? [],
				safe_search: editing.safe_search ?? false,
				// Preserve routing assignment (managed on the VPN page) so
				// editing a profile here doesn't silently clear its tunnel.
				route_via: editing.route_via ?? '',
				route_domains: editing.route_domains ?? []
			};
			await apiPut(`/profiles/${encodeURIComponent(editing.id)}`, payload);
			savedFlash = true;
			setTimeout(() => (savedFlash = false), 2000);
			editing = null;
			newId = '';
			await refresh();
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				error = body?.error?.message ?? e.message;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			saving = false;
		}
	}

	async function del(p: Profile) {
		if (!confirm($_('profiles.delete_confirm', { values: { name: p.name } }))) return;
		try {
			const res = await fetch(`${API_BASE}/profiles/${encodeURIComponent(p.id)}`, {
				method: 'DELETE',
				credentials: 'same-origin'
			});
			if (!res.ok) {
				const body = await res.json().catch(() => null);
				throw new Error(body?.error?.message ?? `HTTP ${res.status}`);
			}
			await refresh();
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
		}
	}

	function blocklistsToText(arr: string[] | undefined): string {
		return (arr ?? []).join(', ');
	}
	function textToBlocklists(text: string): string[] {
		return text
			.split(',')
			.map((s) => s.trim())
			.filter((s) => s.length > 0);
	}

	onMount(refresh);
</script>

<header class="mb-6">
	<h1 class="text-2xl font-semibold">{$_('profiles.title')}</h1>
	<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('profiles.subtitle')}</p>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400">
		<div class="spinner"></div>
	</div>
{:else}
	<!-- Create form -->
	{#if !editing}
		<section class="surface p-4 mb-5">
			<form
				class="flex flex-wrap items-end gap-3"
				onsubmit={(e) => {
					e.preventDefault();
					startCreate();
				}}
			>
				<div class="flex-1 min-w-[180px]">
					<label class="label" for="newid">{$_('profiles.new')}</label>
					<input
						id="newid"
						class="input"
						bind:value={newId}
						placeholder={$_('profiles.new_id_placeholder')}
					/>
					{#if newIdError}
						<p class="help text-red-600 dark:text-red-400">{newIdError}</p>
					{:else}
						<p class="help">{$_('profiles.id_help')}</p>
					{/if}
				</div>
				<button class="btn-primary" type="submit" disabled={!newId || !!newIdError}>
					<i class="bi bi-plus-lg"></i>
					{$_('profiles.new')}
				</button>
			</form>
		</section>
	{/if}

	<!-- Editor (modal-like) -->
	{#if editing}
		<section class="surface p-5 mb-5">
			<header class="flex items-start justify-between gap-3 mb-4">
				<div>
					<div class="flex items-center gap-2 flex-wrap">
						<h2 class="font-semibold text-lg">{editing.name || editing.id}</h2>
						{#if editing.builtin}
							<span class="badge badge-info">{$_('profiles.builtin_badge')}</span>
						{/if}
						<span class="text-xs font-mono text-zinc-500">{editing.id}</span>
					</div>
					{#if editing.builtin}
						<p class="text-xs text-zinc-500 dark:text-zinc-400 mt-1">
							{$_('profiles.builtin_locked')}
						</p>
					{/if}
				</div>
				<button
					type="button"
					class="btn-ghost text-sm"
					onclick={cancelEdit}
					aria-label={$_('common.cancel')}
				>
					<i class="bi bi-x-lg"></i>
				</button>
			</header>

			<div class="space-y-5">
				<div>
					<label class="label" for="pname">{$_('profiles.name_label')}</label>
					<input
						id="pname"
						class="input"
						bind:value={editing.name}
						placeholder={$_('profiles.name_placeholder')}
					/>
				</div>

				<div>
					<label class="label" for="pdesc">{$_('profiles.description_label')}</label>
					<textarea
						id="pdesc"
						class="input"
						rows="2"
						bind:value={editing.description}
						placeholder={$_('profiles.description_placeholder')}
					></textarea>
				</div>

				<div>
					<div class="label flex items-center justify-between">
						<span>{$_('profiles.schedule_section')}</span>
					</div>
					<p class="help mb-3">{$_('profiles.schedule_help')}</p>
					<ScheduleEditor bind:windows={editing.block_windows!} />
				</div>

				<div>
					<label class="label" for="pbl">{$_('profiles.blocklists_section')}</label>
					<input
						id="pbl"
						class="input"
						value={blocklistsToText(editing.dns_blocklists)}
						oninput={(e) => {
							editing!.dns_blocklists = textToBlocklists((e.currentTarget as HTMLInputElement).value);
						}}
						placeholder="ads, trackers"
					/>
					<p class="help">{$_('profiles.blocklists_help')}</p>
				</div>

				<div>
					<label class="flex items-start gap-2.5 cursor-pointer">
						<input
							type="checkbox"
							class="rounded text-brand-600 mt-0.5"
							bind:checked={editing.safe_search}
						/>
						<span>
							<span class="label !mb-0 flex items-center gap-2">
								<i class="bi bi-search-heart text-brand-600 dark:text-brand-300"></i>
								{$_('profiles.safesearch_label')}
							</span>
							<span class="help block">{$_('profiles.safesearch_help')}</span>
						</span>
					</label>
				</div>

				{#if error}
					<div class="flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
						<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
						<span>{error}</span>
					</div>
				{/if}

				<div class="flex items-center gap-2">
					<button class="btn-primary" type="button" disabled={saving} onclick={save}>
						{#if saving}
							<span class="spinner"></span>
							{$_('profiles.saving')}
						{:else}
							<i class="bi bi-check2"></i>
							{$_('profiles.save')}
						{/if}
					</button>
					<button class="btn-ghost" type="button" disabled={saving} onclick={cancelEdit}>
						{$_('common.cancel')}
					</button>
					{#if savedFlash}
						<span class="text-sm text-emerald-600 dark:text-emerald-400 flex items-center gap-1 ml-1">
							<i class="bi bi-check-circle-fill"></i>
							{$_('profiles.saved')}
						</span>
					{/if}
				</div>
			</div>
		</section>
	{/if}

	<!-- List of profiles -->
	{#if !editing}
		<div class="space-y-3">
			{#each profiles as p (p.id)}
				<article class="surface p-5">
					<div class="flex items-start gap-4">
						<div
							class="w-12 h-12 shrink-0 rounded-xl bg-brand-100 dark:bg-brand-500/15 flex items-center justify-center text-brand-700 dark:text-brand-300"
						>
							<i class="bi bi-shield-check text-xl"></i>
						</div>
						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2 flex-wrap">
								<h2 class="font-semibold">{p.name}</h2>
								<span class="text-xs font-mono text-zinc-500">{p.id}</span>
								{#if p.builtin}
									<span class="badge badge-info">{$_('profiles.builtin_badge')}</span>
								{/if}
							</div>
							{#if p.description}
								<p class="text-sm text-zinc-600 dark:text-zinc-300 mt-1.5">{p.description}</p>
							{/if}
							<div class="mt-2 flex flex-wrap gap-2 text-xs">
								{#if p.block_windows && p.block_windows.length > 0}
									{#each p.block_windows as w}
										<span class="badge badge-warn">
											<i class="bi bi-clock"></i>
											{humanDays(w.days)}, {w.start}-{w.end}
										</span>
									{/each}
								{:else}
									<span class="badge badge-neutral">{$_('profiles.no_schedule')}</span>
								{/if}
								{#if p.dns_blocklists && p.dns_blocklists.length > 0}
									<span class="badge badge-info">
										<i class="bi bi-funnel"></i>
										{p.dns_blocklists.join(', ')}
									</span>
								{/if}
								{#if p.safe_search}
									<span class="badge badge-info">
										<i class="bi bi-search-heart"></i>
										{$_('profiles.safesearch_badge')}
									</span>
								{/if}
								{#if p.route_via && p.route_domains && p.route_domains.length > 0}
									<span class="badge badge-neutral">
										<i class="bi bi-signpost-split"></i>
										{$_('profiles.split_badge', { values: { n: p.route_domains.length } })}
									</span>
								{/if}
							</div>
						</div>
						<div class="flex flex-col gap-2 items-stretch">
							<button class="btn-ghost text-sm" onclick={() => startEdit(p)} aria-label="Edit">
								<i class="bi bi-pencil"></i>
							</button>
							{#if !p.builtin}
								<button
									class="btn-ghost text-sm text-red-600 dark:text-red-400"
									onclick={() => del(p)}
									aria-label={$_('profiles.delete')}
								>
									<i class="bi bi-trash"></i>
								</button>
							{/if}
						</div>
					</div>
				</article>
			{/each}

			{#if profiles.length === 0}
				<div class="surface p-10 text-center">
					<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('profiles.empty_help')}</p>
				</div>
			{/if}
		</div>
	{/if}
{/if}
