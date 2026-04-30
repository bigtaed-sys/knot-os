<script lang="ts">
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
				error = 'Wrong password.';
			} else if (e instanceof ApiError && e.status === 409) {
				// Server says auth not configured — push the user into setup.
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

<div class="login">
	<h1>Sign in</h1>
	<p class="muted">Enter the admin password you set during setup.</p>

	<form onsubmit={submit}>
		<label>
			<span>Password</span>
			<input
				type="password"
				bind:value={password}
				autocomplete="current-password"
				required
			/>
		</label>

		{#if error}
			<p class="error" role="alert">{error}</p>
		{/if}

		<button type="submit" disabled={submitting || password.length === 0}>
			{submitting ? 'Signing in…' : 'Sign in'}
		</button>
	</form>
</div>

<style>
	.login {
		max-width: 360px;
		margin: 2rem auto;
	}
	h1 {
		margin-top: 0;
	}
	.muted {
		color: #6b7280;
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 1rem;
		margin-top: 1.5rem;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	label span {
		font-weight: 500;
	}
	input {
		padding: 0.5rem 0.75rem;
		border: 1px solid #d1d5db;
		border-radius: 6px;
		font-size: 1rem;
	}
	input:focus {
		outline: 2px solid #2563eb;
		outline-offset: -1px;
	}
	button {
		padding: 0.6rem 1rem;
		background: #2563eb;
		color: white;
		border: none;
		border-radius: 6px;
		font-size: 1rem;
		font-weight: 500;
		cursor: pointer;
	}
	button:disabled {
		background: #9ca3af;
		cursor: not-allowed;
	}
	button:not(:disabled):hover {
		background: #1d4ed8;
	}
	.error {
		margin: 0;
		color: #b91c1c;
	}
</style>
