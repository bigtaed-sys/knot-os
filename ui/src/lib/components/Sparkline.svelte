<!--
	Sparkline — small inline traffic graph for the devices list and
	the bandwidth-detail panel.

	Renders two stacked translucent areas: incoming (top, brand color)
	and outgoing (bottom, slate). Both share an autoscaled y-axis so
	the visual is always informative — even idle devices show a flat
	baseline rather than a flat zero that disappears.

	Props:
	  values: array of {kbps_in, kbps_out} pairs, oldest first.
	  height: pixel height (default 28).
	  width:  pixel width (default 96).
	  showLabels: if true, draws "↓ X / ↑ Y" rate text overlay.
-->
<script lang="ts">
	import type { Sample } from '$lib/types';

	let {
		values = [],
		height = 28,
		width = 96,
		showLabels = false
	}: {
		values?: Sample[];
		height?: number;
		width?: number;
		showLabels?: boolean;
	} = $props();

	const peak = $derived.by(() => {
		let m = 0;
		for (const v of values) {
			if (v.kbps_in > m) m = v.kbps_in;
			if (v.kbps_out > m) m = v.kbps_out;
		}
		// Floor at 1 Kbps so an idle line still has a baseline scale.
		return Math.max(m, 1);
	});

	function pathFor(field: 'kbps_in' | 'kbps_out'): string {
		if (values.length === 0) return '';
		const stepX = width / Math.max(values.length - 1, 1);
		const pts: string[] = [];
		for (let i = 0; i < values.length; i++) {
			const v = values[i][field];
			const x = i * stepX;
			const y = height - (v / peak) * (height - 2) - 1;
			pts.push(`${i === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`);
		}
		// Close the area down to the bottom edge.
		pts.push(`L ${width} ${height}`);
		pts.push(`L 0 ${height}`);
		pts.push('Z');
		return pts.join(' ');
	}

	const pathIn = $derived(pathFor('kbps_in'));
	const pathOut = $derived(pathFor('kbps_out'));

	const last = $derived(values[values.length - 1] ?? { kbps_in: 0, kbps_out: 0 });

	function fmt(kbps: number): string {
		if (kbps < 0.5) return '—';
		if (kbps < 1000) return Math.round(kbps) + 'K';
		return (kbps / 1000).toFixed(1) + 'M';
	}
</script>

<div class="flex items-center gap-2 text-xs">
	<svg
		{width}
		{height}
		viewBox="0 0 {width} {height}"
		class="shrink-0 overflow-visible"
		aria-hidden="true"
	>
		<!-- Outgoing area (bottom layer, slate) -->
		<path
			d={pathOut}
			class="fill-zinc-300/40 dark:fill-zinc-600/30"
		/>
		<!-- Incoming area (top layer, brand) -->
		<path
			d={pathIn}
			class="fill-brand-400/50 dark:fill-brand-500/30"
		/>
		<!-- Incoming stroke for definition -->
		<path
			d={pathIn.replace(/L 0 \d+(\.\d+)? Z$/, '').replace(/L \d+(\.\d+)? \d+(\.\d+)?$/, '')}
			class="fill-none stroke-brand-500 dark:stroke-brand-400"
			stroke-width="1"
		/>
	</svg>
	{#if showLabels}
		<div class="leading-tight tabular-nums text-zinc-600 dark:text-zinc-400">
			<div>↓ {fmt(last.kbps_in)}</div>
			<div>↑ {fmt(last.kbps_out)}</div>
		</div>
	{/if}
</div>
