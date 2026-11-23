<script lang="ts">
	import UChart, { type Annotation, type ChartSeries } from '$lib/charts/UChart.svelte';
	import type { ChartGroup } from '$lib/charts/sync.svelte';
	import { formatter } from './format';

	// An A2UI TimeSeries: the assistant's numbers drawn with the panel's own
	// chart. The server checked the shape; this side still drops anything
	// that is not a finite number, and caps sizes.
	type Line = { label?: unknown; values?: unknown; color?: unknown; stacked?: unknown; dashed?: unknown };
	let {
		title = '',
		unit = 'number',
		times,
		lines,
		annotations,
		threshold,
		yMax,
		height,
		group
	}: { title?: string; unit?: string; times: unknown; lines: Line[]; annotations?: unknown; threshold?: unknown; yMax?: unknown; height?: unknown; group?: ChartGroup } = $props();

	const MAX_POINTS = 300;
	const MAX_SERIES = 8;
	const COLORS = new Set(['action', 'data', 'run', 'warn', 'fail', 'muted', 'ink']);
	const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);

	const ts = $derived(Array.isArray(times) ? times.slice(0, MAX_POINTS).map((t) => (finite(t) ? t : NaN)) : []);
	const okTimes = $derived(ts.length > 0 && ts.every((t) => Number.isFinite(t)));
	const list = $derived(lines.slice(0, MAX_SERIES));
	const series = $derived<ChartSeries[]>(
		list.map((l, i) => {
			const label = String(l.label ?? `Series ${i + 1}`).slice(0, 80);
			const c = typeof l.color === 'string' ? l.color : '';
			const color = COLORS.has(c) || /^cat:[0-9]$/.test(c) ? c : /unattributed|other/i.test(label) && list.length > 1 ? 'muted' : list.length === 1 ? 'action' : `cat:${i}`;
			return { label, color, stacked: l.stacked === true, dashed: l.dashed === true, fill: list.length === 1 && l.stacked !== true };
		})
	);
	const data = $derived({
		times: okTimes ? ts : [],
		series: list.map((l) => {
			const v = Array.isArray(l.values) ? l.values : [];
			return ts.map((_, k) => (finite(v[k]) ? v[k] : null));
		})
	});
	const notes = $derived<Annotation[]>(
		Array.isArray(annotations)
			? annotations
					.slice(0, 40)
					.filter((a) => a && finite(a.t))
					.map((a) => ({ t: a.t, end: finite(a.end) ? a.end : a.end === null ? null : undefined, kind: typeof a.kind === 'string' ? a.kind : 'event', label: String(a.label ?? '').slice(0, 200) }))
			: []
	);
	const format = $derived(formatter(unit));
	const h = $derived(finite(height) ? Math.max(80, Math.min(400, height)) : 150);
</script>

<figure class="min-w-0">
	{#if title}<figcaption class="mb-1 text-small font-medium text-ink">{title}</figcaption>{/if}
	<UChart
		label={title || 'Chart'}
		{data}
		{series}
		{format}
		{group}
		height={h}
		yMax={unit === 'percent' && !finite(yMax) ? 100 : finite(yMax) ? yMax : undefined}
		annotations={notes}
		threshold={finite(threshold) ? { value: threshold, label: format(threshold) } : null}
		empty={okTimes ? 'No samples in this range.' : 'This chart has no valid times.'}
	/>
</figure>
