<script lang="ts">
	import { goto } from '$app/navigation';
	import { apiGet, apiPost, ApiError } from '$lib/api';
	import type { ScanResponse, ScannedNetwork } from '$lib/types';

	type Step = 1 | 2 | 3 | 4;
	let step = $state<Step>(1);

	// Step 1 — device + admin password
	let deviceName = $state('knot');
	let country = $state('RU');
	let password = $state('');
	let passwordConfirm = $state('');
	let step1Error = $derived.by(() => {
		if (!deviceName) return 'Device name is required.';
		if (!/^[a-zA-Z0-9_-]{1,63}$/.test(deviceName))
			return 'Device name: letters, digits, hyphen, underscore only.';
		if (!/^[A-Z]{2}$/.test(country)) return 'Country must be a 2-letter code (e.g. RU, US).';
		if (password.length < 8) return 'Password must be at least 8 characters.';
		if (password !== passwordConfirm) return 'Passwords do not match.';
		return null;
	});

	// Step 2 — pick uplink
	let networks = $state<ScannedNetwork[]>([]);
	let scanning = $state(false);
	let uplinkSSID = $state('');
	let uplinkPSK = $state('');
	let uplinkSecured = $derived(networks.find((n) => n.ssid === uplinkSSID)?.secured ?? true);
	let step2Error = $derived.by(() => {
		if (!uplinkSSID) return 'Select an upstream Wi-Fi network.';
		if (uplinkSecured && !uplinkPSK) return 'Password is required for secured networks.';
		return null;
	});

	async function scan() {
		scanning = true;
		try {
			const r = await apiGet<ScanResponse>('/setup/scan');
			networks = r.networks.toSorted((a, b) => b.rssi_dbm - a.rssi_dbm);
		} catch (e) {
			submitError = e instanceof Error ? e.message : String(e);
		} finally {
			scanning = false;
		}
	}

	// Step 3 — AP
	let apSSID = $state('KnotNet');
	let apPSK = $state('');
	let apBand = $state<'2.4'>('2.4'); // 5GHz unsupported on Zero 2W ap0
	let step3Error = $derived.by(() => {
		if (!apSSID) return 'Broadcast SSID is required.';
		if (apPSK.length > 0 && apPSK.length < 8) return 'Wi-Fi password must be at least 8 characters (or empty for an open network).';
		return null;
	});

	// Submit
	let submitting = $state(false);
	let submitError = $state<string | null>(null);

	async function submit() {
		submitting = true;
		submitError = null;
		try {
			await apiPost('/setup/complete', {
				device: { name: deviceName, country },
				password,
				uplink: { ssid: uplinkSSID, psk: uplinkSecured ? uplinkPSK : '' },
				ap: { ssid: apSSID, psk: apPSK, band: apBand }
			});
			goto('/', { replaceState: true });
		} catch (e) {
			if (e instanceof ApiError) {
				const body = e.body as { error?: { message?: string } } | undefined;
				submitError = body?.error?.message ?? e.message;
			} else {
				submitError = e instanceof Error ? e.message : String(e);
			}
		} finally {
			submitting = false;
		}
	}

	function next() {
		if (step === 1 && !step1Error) step = 2;
		else if (step === 2 && !step2Error) step = 3;
		else if (step === 3 && !step3Error) step = 4;
	}
	function back() {
		if (step > 1) step = (step - 1) as Step;
	}

	$effect(() => {
		if (step === 2 && networks.length === 0 && !scanning) scan();
	});
</script>

<div class="wizard">
	<header>
		<h1>Welcome to KnotOS</h1>
		<p class="muted">Let's get your device on the network.</p>
		<ol class="progress">
			<li class:active={step >= 1}>Device</li>
			<li class:active={step >= 2}>Upstream Wi-Fi</li>
			<li class:active={step >= 3}>Broadcast Wi-Fi</li>
			<li class:active={step >= 4}>Review</li>
		</ol>
	</header>

	{#if step === 1}
		<section>
			<h2>Device &amp; admin</h2>

			<label>
				<span>Device name</span>
				<input bind:value={deviceName} />
				<small>Used as hostname and on the LAN as <code>{deviceName}.local</code>.</small>
			</label>

			<label>
				<span>Country</span>
				<input bind:value={country} maxlength="2" style="text-transform:uppercase" />
				<small>2-letter code, e.g. RU, US, DE. Used for legal Wi-Fi channel rules.</small>
			</label>

			<label>
				<span>Admin password</span>
				<input type="password" bind:value={password} autocomplete="new-password" />
			</label>

			<label>
				<span>Confirm password</span>
				<input type="password" bind:value={passwordConfirm} autocomplete="new-password" />
			</label>

			{#if step1Error && (password || passwordConfirm)}
				<p class="error">{step1Error}</p>
			{/if}
		</section>
	{:else if step === 2}
		<section>
			<h2>Upstream Wi-Fi</h2>
			<p class="muted">
				This is the network KnotOS connects to. On a Zero 2W its channel will also be used by your
				broadcast network.
			</p>

			<div class="scan-row">
				<button class="ghost" onclick={scan} disabled={scanning}>
					{scanning ? 'Scanning…' : 'Rescan'}
				</button>
				<small>{networks.length} networks visible</small>
			</div>

			<ul class="networks">
				{#each networks as n}
					<li>
						<label>
							<input type="radio" bind:group={uplinkSSID} value={n.ssid} />
							<span class="ssid">{n.ssid || '(hidden)'}</span>
							<span class="meta">
								{n.rssi_dbm} dBm · ch {n.channel} · {n.band} GHz
								{#if !n.secured}<em>open</em>{/if}
							</span>
						</label>
					</li>
				{/each}
			</ul>

			{#if uplinkSSID && uplinkSecured}
				<label class="psk">
					<span>Wi-Fi password for "{uplinkSSID}"</span>
					<input type="password" bind:value={uplinkPSK} autocomplete="off" />
				</label>
			{/if}
		</section>
	{:else if step === 3}
		<section>
			<h2>Broadcast Wi-Fi</h2>
			<p class="muted">This is the network your devices will connect to.</p>

			<label>
				<span>SSID</span>
				<input bind:value={apSSID} />
			</label>

			<label>
				<span>Password</span>
				<input type="password" bind:value={apPSK} autocomplete="off" />
				<small>Leave empty for an open network. Otherwise minimum 8 characters.</small>
			</label>

			<label>
				<span>Band</span>
				<select bind:value={apBand}>
					<option value="2.4">2.4 GHz</option>
				</select>
				<small>Only 2.4 GHz is supported on Zero 2W's broadcast interface.</small>
			</label>

			{#if step3Error}<p class="error">{step3Error}</p>{/if}
		</section>
	{:else if step === 4}
		<section>
			<h2>Review</h2>
			<dl>
				<dt>Device</dt>
				<dd>{deviceName} <span class="muted">({country})</span></dd>

				<dt>Upstream</dt>
				<dd>{uplinkSSID}</dd>

				<dt>Broadcast</dt>
				<dd>{apSSID} <span class="muted">({apBand} GHz)</span></dd>
			</dl>

			<p class="warn">
				After applying, the setup network will disappear and {apSSID} will appear on the same channel
				as the upstream. Reconnect to {apSSID}, then sign in with your admin password.
			</p>

			{#if submitError}<p class="error">{submitError}</p>{/if}
		</section>
	{/if}

	<footer class="actions">
		{#if step > 1}
			<button class="ghost" onclick={back} disabled={submitting}>Back</button>
		{:else}
			<span></span>
		{/if}

		{#if step < 4}
			<button
				onclick={next}
				disabled={(step === 1 && !!step1Error) ||
					(step === 2 && !!step2Error) ||
					(step === 3 && !!step3Error)}
			>
				Next
			</button>
		{:else}
			<button class="primary" onclick={submit} disabled={submitting}>
				{submitting ? 'Applying…' : 'Apply'}
			</button>
		{/if}
	</footer>
</div>

<style>
	.wizard {
		max-width: 560px;
		margin: 1rem auto;
	}
	header {
		margin-bottom: 1.5rem;
	}
	h1 {
		margin: 0 0 0.25rem;
	}
	h2 {
		margin: 0 0 1rem;
	}
	.muted {
		color: #6b7280;
	}
	.progress {
		display: flex;
		gap: 0.5rem;
		list-style: none;
		padding: 0;
		margin: 1rem 0 0;
		font-size: 0.85rem;
		color: #9ca3af;
		flex-wrap: wrap;
	}
	.progress li {
		padding: 0.25rem 0.5rem;
		border-radius: 999px;
		background: #f3f4f6;
	}
	.progress li.active {
		color: #1f2937;
		background: #dbeafe;
	}
	section {
		display: flex;
		flex-direction: column;
		gap: 1rem;
		padding: 1.25rem;
		border: 1px solid #e5e7eb;
		border-radius: 8px;
		background: #f9fafb;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
	}
	label > span {
		font-weight: 500;
	}
	input,
	select {
		padding: 0.5rem 0.75rem;
		border: 1px solid #d1d5db;
		border-radius: 6px;
		font-size: 1rem;
		background: white;
	}
	small {
		color: #6b7280;
	}
	.scan-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
	}
	.networks {
		list-style: none;
		padding: 0;
		margin: 0;
		max-height: 280px;
		overflow-y: auto;
		border: 1px solid #e5e7eb;
		border-radius: 6px;
		background: white;
	}
	.networks li + li {
		border-top: 1px solid #f3f4f6;
	}
	.networks label {
		flex-direction: row;
		align-items: center;
		gap: 0.75rem;
		padding: 0.5rem 0.75rem;
		cursor: pointer;
	}
	.networks label:hover {
		background: #f9fafb;
	}
	.ssid {
		flex: 1;
		font-weight: 500;
	}
	.meta {
		color: #6b7280;
		font-size: 0.85rem;
	}
	em {
		font-style: normal;
		margin-left: 0.5rem;
		color: #b45309;
	}
	.psk {
		margin-top: 0.5rem;
	}
	.actions {
		display: flex;
		justify-content: space-between;
		margin-top: 1.25rem;
	}
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: 0.5rem 1rem;
		margin: 0;
	}
	dt {
		color: #6b7280;
	}
	dd {
		margin: 0;
	}
	button {
		padding: 0.5rem 1rem;
		border-radius: 6px;
		border: 1px solid transparent;
		font-size: 1rem;
		font-weight: 500;
		cursor: pointer;
		background: #2563eb;
		color: white;
	}
	button:not(:disabled):hover {
		background: #1d4ed8;
	}
	button.primary {
		background: #059669;
	}
	button.primary:not(:disabled):hover {
		background: #047857;
	}
	button.ghost {
		background: white;
		color: #1f2937;
		border-color: #d1d5db;
	}
	button.ghost:not(:disabled):hover {
		background: #f9fafb;
	}
	button:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.error {
		color: #b91c1c;
		margin: 0;
	}
	.warn {
		padding: 0.75rem 1rem;
		background: #fef3c7;
		color: #78350f;
		border-radius: 6px;
		margin: 0;
	}
</style>
