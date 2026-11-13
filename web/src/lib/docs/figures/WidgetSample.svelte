<script lang="ts">
	import WidgetGrid from '$lib/components/WidgetGrid.svelte';
	import type { DashboardWidget } from '$lib/api/types';
	import Figure from './Figure.svelte';
	// Sample data through the real renderer, so the figure always matches
	// what a bot's dashboard shows.
	const base = { group: 'Operations', min_height: 0, position: 0, updated_at_ms: 0, expires_at_ms: null, stale: false };
	const widgets: DashboardWidget[] = [
		{ ...base, key: 'latency', kind: 'line', title: 'Gateway latency', span: 2, data: { unit: 'ms', points: [35, 52, 41, 68, 55, 76, 62, 48, 44, 42].map((value, i) => ({ label: `${i}`, value })) } },
		{ ...base, key: 'gateway', kind: 'status', title: 'Discord gateway', span: 1, data: { state: 'good', text: 'Connected' } },
		{ ...base, key: 'guilds', kind: 'metric', title: 'Servers', span: 1, data: { value: 1284, delta: 12, detail: 'this week' } },
		{ ...base, key: 'queue', kind: 'progress', title: 'Queue', span: 2, data: { value: 68, max: 100, unit: '%' } }
	];
</script>

<Figure caption="Rendered by the same component as a bot's Analytics tab, from the sample payloads on this page.">
	<div class="-mt-5"><WidgetGrid {widgets} /></div>
</Figure>
