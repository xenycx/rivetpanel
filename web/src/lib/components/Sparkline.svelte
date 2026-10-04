<script lang="ts">
	// fluid: stretch to the container width (the line scales, the stroke does not).
	let { values, max = 100, label, width = 160, height = 36, fluid = false }: { values: number[]; max?: number; label: string; width?: number; height?: number; fluid?: boolean } = $props();
	const points = $derived.by(() => {
		if (values.length < 2) return '';
		const step = width / (values.length - 1);
		return values.map((v, i) => `${(i * step).toFixed(1)},${(height - (Math.min(v, max) / max) * (height - 2) - 1).toFixed(1)}`).join(' ');
	});
</script>

<svg width={fluid ? '100%' : width} {height} viewBox="0 0 {width} {height}" preserveAspectRatio={fluid ? 'none' : undefined} role="img" aria-label={label} class="block text-data">
	<line x1="0" y1={height - 0.5} x2={width} y2={height - 0.5} stroke="currentColor" stroke-opacity="0.2" vector-effect="non-scaling-stroke" />
	{#if points}<polyline {points} fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" vector-effect="non-scaling-stroke" />{/if}
</svg>
