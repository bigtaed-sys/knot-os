<script lang="ts">
	import { onMount, onDestroy, tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { _ } from 'svelte-i18n';
	import { apiGet, apiPost, apiDelete, ApiError } from '$lib/api';
	import type { ModemSMS } from '$lib/types';

	let messages = $state<ModemSMS[]>([]);
	let selected = $state<string | null>(null); // active conversation number
	let compose = $state('');
	let newNumber = $state('');
	let loading = $state(true);
	let sending = $state(false);
	let error = $state<string | null>(null);
	let modemPresent = $state(true);
	let timer: ReturnType<typeof setInterval> | null = null;
	let threadEl = $state<HTMLDivElement | null>(null);

	// Conversations: messages grouped by peer number, most-recent first.
	interface Convo {
		number: string;
		messages: ModemSMS[];
		last: ModemSMS;
	}
	const convos = $derived.by<Convo[]>(() => {
		const byNum = new Map<string, ModemSMS[]>();
		for (const m of messages) {
			const n = m.number || '—';
			if (!byNum.has(n)) byNum.set(n, []);
			byNum.get(n)!.push(m);
		}
		const list: Convo[] = [];
		for (const [number, msgs] of byNum) {
			msgs.sort((a, b) => Number(a.id) - Number(b.id));
			list.push({ number, messages: msgs, last: msgs[msgs.length - 1] });
		}
		// Newest conversation first (by highest message id).
		list.sort((a, b) => Number(b.last.id) - Number(a.last.id));
		return list;
	});

	const activeConvo = $derived(convos.find((c) => c.number === selected) ?? null);

	function errMsg(e: unknown): string {
		if (e instanceof ApiError) {
			const b = e.body as { error?: { message?: string } } | undefined;
			return b?.error?.message ?? e.message;
		}
		return e instanceof Error ? e.message : String(e);
	}

	async function load(initial = false) {
		try {
			const r = await apiGet<{ messages: ModemSMS[] }>('/modem/sms', { timeoutMs: 8000 });
			messages = r.messages ?? [];
			modemPresent = true;
			if (initial && !selected && convos.length > 0) selected = convos[0].number;
			error = null;
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				goto('/login', { replaceState: true });
				return;
			}
			if (e instanceof ApiError && e.status === 503) {
				modemPresent = false;
			} else if (!initial) {
				/* transient poll error — keep prior */
			} else {
				error = errMsg(e);
			}
		} finally {
			loading = false;
		}
	}

	async function send() {
		const number = (selected ?? newNumber).trim();
		if (!number || !compose.trim()) return;
		sending = true;
		error = null;
		try {
			await apiPost('/modem/sms', { number, text: compose }, { timeoutMs: 30000 });
			compose = '';
			selected = number;
			newNumber = '';
			await load();
			await scrollToEnd();
		} catch (e) {
			error = errMsg(e);
		} finally {
			sending = false;
		}
	}

	async function del(id: string) {
		try {
			await apiDelete(`/modem/sms/${id}`);
			await load();
		} catch (e) {
			error = errMsg(e);
		}
	}

	async function scrollToEnd() {
		await tick();
		if (threadEl) threadEl.scrollTop = threadEl.scrollHeight;
	}

	function startNew() {
		selected = null;
		newNumber = '';
		compose = '';
	}

	$effect(() => {
		// Scroll to the newest message whenever the active thread changes.
		void activeConvo?.messages.length;
		scrollToEnd();
	});

	onMount(() => {
		load(true);
		timer = setInterval(() => load(false), 10000);
	});
	onDestroy(() => {
		if (timer) clearInterval(timer);
	});
</script>

<svelte:head>
	<title>{$_('messages.title')} · KnotOS</title>
</svelte:head>

<header class="mb-4 flex items-center justify-between gap-3">
	<div>
		<h1 class="text-2xl font-semibold">{$_('messages.title')}</h1>
		<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-1">{$_('messages.subtitle')}</p>
	</div>
	<button class="btn-ghost text-sm shrink-0" type="button" onclick={startNew}>
		<i class="bi bi-pencil-square"></i>{$_('messages.new')}
	</button>
</header>

{#if loading}
	<div class="flex items-center justify-center py-16 text-zinc-400"><div class="spinner"></div></div>
{:else if !modemPresent}
	<div class="surface border-amber-300 dark:border-amber-500/30 p-4 text-sm flex items-start gap-3">
		<i class="bi bi-info-circle text-amber-500 text-lg mt-0.5"></i>
		<span>{$_('messages.no_modem')}</span>
	</div>
{:else}
	{#if error}
		<div class="flex items-start gap-2 p-3 mb-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
			<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i><span>{error}</span>
		</div>
	{/if}

	<div class="surface overflow-hidden grid grid-cols-1 md:grid-cols-[18rem_1fr]" style="height: 70vh">
		<!-- Conversation list -->
		<aside
			class="border-b md:border-b-0 md:border-r border-zinc-200 dark:border-zinc-800 overflow-y-auto
				{selected || newNumber !== '' ? 'hidden md:block' : ''}"
		>
			{#if convos.length === 0}
				<p class="p-4 text-sm text-zinc-500 dark:text-zinc-400">{$_('messages.empty')}</p>
			{:else}
				{#each convos as c (c.number)}
					<button
						type="button"
						onclick={() => (selected = c.number)}
						class="w-full text-left px-4 py-3 border-b border-zinc-100 dark:border-zinc-800/70
							{selected === c.number ? 'bg-brand-50 dark:bg-brand-500/10' : 'hover:bg-zinc-50 dark:hover:bg-zinc-800/40'}"
					>
						<div class="flex items-center justify-between gap-2">
							<span class="font-medium text-sm font-mono truncate">{c.number}</span>
							<span class="text-[10px] text-zinc-400 shrink-0">
								{c.last.timestamp ? new Date(c.last.timestamp).toLocaleDateString() : ''}
							</span>
						</div>
						<div class="text-xs text-zinc-500 dark:text-zinc-400 truncate mt-0.5">
							{#if c.last.sent}<i class="bi bi-arrow-up-right"></i> {/if}{c.last.text}
						</div>
					</button>
				{/each}
			{/if}
		</aside>

		<!-- Thread / composer -->
		<section class="flex flex-col min-w-0 {!selected && newNumber === '' ? 'hidden md:flex' : ''}">
			<!-- Thread header -->
			<div class="flex items-center gap-2 px-4 h-12 border-b border-zinc-200 dark:border-zinc-800 shrink-0">
				<button
					class="md:hidden text-zinc-500"
					type="button"
					aria-label={$_('messages.new')}
					onclick={() => {
						selected = null;
						newNumber = '';
					}}
				>
					<i class="bi bi-chevron-left"></i>
				</button>
				{#if selected}
					<span class="font-mono text-sm font-medium">{selected}</span>
				{:else}
					<input
						class="input font-mono text-sm py-1 max-w-56"
						bind:value={newNumber}
						placeholder={$_('messages.to_ph')}
					/>
				{/if}
			</div>

			<!-- Messages -->
			<div bind:this={threadEl} class="flex-1 overflow-y-auto p-4 space-y-2 bg-zinc-50/60 dark:bg-zinc-900/40">
				{#if activeConvo}
					{#each activeConvo.messages as m (m.id)}
						<div class="flex {m.sent ? 'justify-end' : 'justify-start'}">
							<div class="group relative max-w-[80%]">
								<div
									class="rounded-2xl px-3 py-2 text-sm break-words whitespace-pre-wrap
										{m.sent
										? 'bg-brand-500 text-white rounded-br-sm'
										: 'bg-white dark:bg-zinc-800 border border-zinc-200 dark:border-zinc-700 rounded-bl-sm'}"
								>
									{m.text}
								</div>
								<div class="flex items-center gap-2 mt-0.5 text-[10px] text-zinc-400 {m.sent ? 'justify-end' : ''}">
									{#if m.timestamp}<span>{new Date(m.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>{/if}
									<button
										type="button"
										class="opacity-0 group-hover:opacity-100 hover:text-red-500"
										title={$_('messages.delete')}
										onclick={() => del(m.id)}
									>
										<i class="bi bi-trash"></i>
									</button>
								</div>
							</div>
						</div>
					{/each}
				{:else if newNumber === '' && !selected}
					<div class="h-full flex items-center justify-center text-sm text-zinc-400">
						{$_('messages.pick')}
					</div>
				{/if}
			</div>

			<!-- Composer -->
			{#if selected || newNumber !== ''}
				<div class="flex items-end gap-2 p-3 border-t border-zinc-200 dark:border-zinc-800 shrink-0">
					<textarea
						bind:value={compose}
						rows="1"
						placeholder={$_('messages.message_ph')}
						class="input flex-1 resize-none max-h-28"
						onkeydown={(e) => {
							if (e.key === 'Enter' && !e.shiftKey) {
								e.preventDefault();
								send();
							}
						}}
					></textarea>
					<button
						class="btn-primary shrink-0"
						type="button"
						disabled={sending || !compose.trim() || (!selected && !newNumber.trim())}
						onclick={send}
					>
						{#if sending}<span class="spinner"></span>{:else}<i class="bi bi-send"></i>{/if}
					</button>
				</div>
			{/if}
		</section>
	</div>
{/if}
