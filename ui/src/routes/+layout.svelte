<script lang="ts">
	import '../app.css';
	let { children } = $props();
</script>

<div class="app">
	<header>
		<a href="/" class="brand">KnotOS</a>
	</header>

	<main>
		{@render children()}
	</main>

	<footer>
		<small>KnotOS v0.0.0-dev — pre-alpha skeleton</small>
	</footer>
</div>

<style>
	.app {
		display: flex;
		flex-direction: column;
		min-height: 100vh;
		font-family: system-ui, -apple-system, sans-serif;
	}
	header {
		padding: 1rem 1.5rem;
		border-bottom: 1px solid #e5e7eb;
	}
	.brand {
		font-weight: 700;
		font-size: 1.25rem;
		text-decoration: none;
		color: inherit;
	}
	main {
		flex: 1;
		padding: 2rem 1.5rem;
		max-width: 960px;
		width: 100%;
		margin: 0 auto;
	}
	footer {
		padding: 1rem 1.5rem;
		border-top: 1px solid #e5e7eb;
		color: #6b7280;
	}
</style>
