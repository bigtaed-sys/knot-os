<script lang="ts">
	import { _ } from 'svelte-i18n';
	import { goto } from '$app/navigation';
	import { apiPost, ApiError } from '$lib/api';

	let password = $state('');
	let submitting = $state(false);
	let error = $state<string | null>(null);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		submitting = true;
		error = null;
		try {
			await apiPost('/auth/login', { password });
			goto('/', { replaceState: true });
		} catch (e) {
			if (e instanceof ApiError && e.status === 401) {
				error = $_('auth.wrong_password');
			} else if (e instanceof ApiError && e.status === 409) {
				goto('/setup', { replaceState: true });
				return;
			} else {
				error = e instanceof Error ? e.message : String(e);
			}
		} finally {
			submitting = false;
		}
	}
</script>

<div class="min-h-screen flex items-center justify-center p-4 bg-gradient-to-br from-zinc-50 via-zinc-50 to-brand-50 dark:from-zinc-950 dark:via-zinc-900 dark:to-brand-950">
	<div class="w-full max-w-sm">
		<!-- Brand -->
		<div class="flex flex-col items-center mb-6">
			<div class="w-14 h-14 rounded-2xl bg-gradient-to-br from-brand-500 to-brand-700 flex items-center justify-center shadow-lg mb-3">
				<i class="bi bi-diagram-3-fill text-white text-2xl"></i>
			</div>
			<h1 class="text-xl font-semibold text-zinc-900 dark:text-zinc-100">{$_('app.name')}</h1>
			<p class="text-sm text-zinc-500 dark:text-zinc-400">{$_('app.tagline')}</p>
		</div>

		<div class="surface p-6">
			<div class="mb-5">
				<h2 class="font-semibold text-lg">{$_('auth.login_title')}</h2>
				<p class="text-sm text-zinc-500 dark:text-zinc-400 mt-0.5">{$_('auth.login_subtitle')}</p>
			</div>

			<form onsubmit={submit} class="space-y-4">
				<div>
					<label for="pw" class="label">{$_('auth.password')}</label>
					<div class="relative">
						<i class="bi bi-lock absolute left-3 top-1/2 -translate-y-1/2 text-zinc-400"></i>
						<input
							id="pw"
							type="password"
							class="input pl-10"
							bind:value={password}
							autocomplete="current-password"
							required
						/>
					</div>
				</div>

				{#if error}
					<div class="flex items-start gap-2 p-3 rounded-lg bg-red-50 dark:bg-red-500/10 text-red-700 dark:text-red-300 text-sm">
						<i class="bi bi-exclamation-circle mt-0.5 shrink-0"></i>
						<span>{error}</span>
					</div>
				{/if}

				<button type="submit" class="btn-primary w-full" disabled={submitting || password.length === 0}>
					{#if submitting}
						<span class="spinner"></span>
						{$_('auth.signing_in')}
					{:else}
						<i class="bi bi-box-arrow-in-right"></i>
						{$_('auth.sign_in')}
					{/if}
				</button>
			</form>
		</div>
	</div>
</div>
