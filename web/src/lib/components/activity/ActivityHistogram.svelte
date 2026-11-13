<script lang="ts">
	import type { ActivityStats } from '$lib/activity';

	// The window's activity over time. Bars stack what the panel did by
	// itself (bottom) under what people did (top); failures hang below the
	// baseline. Drag across the chart to zoom into that span, click a bar to
	// zoom into it; arrow keys move between bars and Enter zooms.
	let {
		stats,
		loading = false,
		readout = $bindable(null),
		onselect
	}: {
		stats: ActivityStats | null;
		loading?: boolean;
		/** Out: the hovered bar, described for a caption outside the chart. */
		readout?: { when: string; people: number; automatic: number; failed: number } | null;
		onselect: (from: number, to: number) => void;
	} = $props();

	let width = $state(0);
	let svg = $state<SVGSVGElement>();
	const H = 112; // drawing height
	const top = 8;
	const base = 82; // baseline y
	const failDepth = 14; // room below the baseline for failures
	const buckets = $derived(stats?.buckets ?? []);
	const n = $derived(buckets.length);
	const max = $derived(Math.max(1, ...buckets.map((b) => b.people + b.automatic)));
	const maxFail = $derived(Math.max(1, ...buckets.map((b) => b.failed)));
	const step = $derived(n ? width / n : 0);
	const gap = $derived(step > 6 ? Math.min(3, step * 0.22) : step > 3 ? 1 : 0);
	const scale = (v: number) => (v / max) * (base - top);

	let hover = $state<number | null>(null);
	let drag = $state<{ a: number; b: number } | null>(null);
	let pointerId: number | null = null;

	function indexAt(clientX: number): number {
		const r = svg!.getBoundingClientRect();
		return Math.max(0, Math.min(n - 1, Math.floor(((clientX - r.left) / r.width) * n)));
	}
	function down(e: PointerEvent) {
		if (!n || e.button !== 0) return;
		pointerId = e.pointerId;
		svg!.setPointerCapture(e.pointerId);
		const i = indexAt(e.clientX);
		drag = { a: i, b: i };
	}
	function move(e: PointerEvent) {
		if (!n) return;
		const i = indexAt(e.clientX);
		hover = i;
		if (drag && e.pointerId === pointerId) drag = { ...drag, b: i };
	}
	function up(e: PointerEvent) {
		if (!drag || e.pointerId !== pointerId) return;
		const lo = Math.min(drag.a, drag.b);
		const hi = Math.max(drag.a, drag.b);
		drag = null;
		pointerId = null;
		zoom(lo, hi);
	}
	function zoom(lo: number, hi: number) {
		if (!stats || !n) return;
		const from = buckets[lo].t;
		const to = Math.min(buckets[hi].t + stats.bucket_ms, Date.now());
		// A single bucket that is already the smallest step has nothing to zoom into.
		if (lo === hi && stats.bucket_ms <= 60_000) return;
		onselect(from, to);
	}
	function key(e: KeyboardEvent) {
		if (!n) return;
		if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
			e.preventDefault();
			hover = Math.max(0, Math.min(n - 1, (hover ?? (e.key === 'ArrowLeft' ? n : -1)) + (e.key === 'ArrowLeft' ? -1 : 1)));
		} else if (e.key === 'Home' || e.key === 'End') {
			e.preventDefault();
			hover = e.key === 'Home' ? 0 : n - 1;
		} else if ((e.key === 'Enter' || e.key === ' ') && hover !== null) {
			e.preventDefault();
			zoom(hover, hover);
		}
	}

	// ---- Labels -----------------------------------------------------------------------
	const bucketMs = $derived(stats?.bucket_ms ?? 3600_000);
	function when(t: number, withEnd = false): string {
		const d = new Date(t);
		const day = bucketMs >= 86400_000;
		const date = d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' });
		if (day) return bucketMs > 86400_000 && withEnd ? `${date} – ${new Date(t + bucketMs - 1).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}` : date;
		const time = (x: number) => new Date(x).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
		return withEnd ? `${date}, ${time(t)}–${time(t + bucketMs)}` : `${date}, ${time(t)}`;
	}
	function tick(t: number): string {
		const d = new Date(t);
		const span = n * bucketMs;
		if (span <= 2 * 86400_000) return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
		return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	}
	const ticks = $derived.by(() => {
		if (!n || width < 10) return [];
		const want = Math.max(2, Math.min(7, Math.floor(width / 110)));
		const every = Math.max(1, Math.round(n / want));
		const out: { x: number; text: string; anchor: string }[] = [];
		for (let i = 0; i < n; i += every) out.push({ x: i * step, text: tick(buckets[i].t), anchor: i === 0 ? 'start' : 'middle' });
		return out;
	});
	const tip = $derived(hover !== null && buckets[hover] ? buckets[hover] : null);
	$effect(() => {
		readout = tip ? { when: when(tip.t, true), people: tip.people, automatic: tip.automatic, failed: tip.failed } : null;
	});
	const summary = $derived(
		stats ? `${stats.total} events between ${when(stats.from)} and ${when(stats.to)}; ${stats.by_source.person ?? 0} by people. Drag or use arrow keys and Enter to zoom.` : 'Loading activity over time'
	);
	const selRange = $derived(drag ? [Math.min(drag.a, drag.b), Math.max(drag.a, drag.b)] : null);
</script>

<div class="hist" class:loading>
	<div class="relative" bind:clientWidth={width}>
		<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (the chart is operable with arrow keys and Enter) -->
		<svg
			bind:this={svg}
			role="img"
			aria-label={summary}
			tabindex="0"
			width={Math.max(0, width)}
			height={H}
			viewBox="0 0 {Math.max(1, width)} {H}"
			onpointerdown={down}
			onpointermove={move}
			onpointerup={up}
			onpointercancel={() => ((drag = null), (pointerId = null))}
			onpointerleave={() => !drag && (hover = null)}
			onkeydown={key}
			onblur={() => (hover = null)}
			ondblclick={(e) => e.preventDefault()}
		>
			<line x1="0" x2={width} y1={base + 0.5} y2={base + 0.5} class="axis" />
			<line x1="0" x2={width} y1={top + 0.5} y2={top + 0.5} class="grid" />
			<line x1="0" x2={width} y1={(top + base) / 2 + 0.5} y2={(top + base) / 2 + 0.5} class="grid" />
			{#if selRange}
				<rect class="brush" x={selRange[0] * step} y={top - 6} width={(selRange[1] - selRange[0] + 1) * step} height={base - top + failDepth + 8} rx="4" />
			{/if}
			{#each buckets as b, i (i)}
				{@const auto = scale(b.automatic)}
				{@const people = scale(b.people)}
				{@const x = i * step + gap / 2}
				{@const w = Math.max(1, step - gap)}
				<g class="bar" class:dim={hover !== null && hover !== i && !selRange} class:on={hover === i}>
					<rect class="hit" x={i * step} y={top} width={step} height={base - top + failDepth} />
					<rect class="auto" {x} width={w} rx={Math.min(2, w / 3)} style:y="{base - auto}px" style:height="{auto}px" />
					<rect class="people" {x} width={w} rx={Math.min(2, w / 3)} style:y="{base - auto - people - (people && auto ? 1 : 0)}px" style:height="{people}px" />
					{#if b.failed}
						<rect class="fail" x={x + w * 0.2} width={Math.max(1, w * 0.6)} rx={Math.min(1.5, w / 4)} y={base + 3} style:height="{2 + (b.failed / maxFail) * (failDepth - 5)}px" />
					{/if}
				</g>
			{/each}
		</svg>
	</div>
	<div class="ticks" aria-hidden="true">
		{#each ticks as t (t.x)}<span style:left="{t.x}px" data-anchor={t.anchor}>{t.text}</span>{/each}
	</div>
</div>

<style>
	.hist {
		position: relative;
		user-select: none;
		-webkit-user-select: none;
	}
	svg {
		display: block;
		overflow: visible;
		cursor: crosshair;
		touch-action: pan-y;
		outline: none;
		border-radius: var(--radius-inner);
	}
	svg:focus-visible {
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-action) 55%, transparent);
	}
	.axis {
		stroke: var(--color-rule);
	}
	.grid {
		stroke: var(--color-rule-soft);
		stroke-dasharray: 2 4;
	}
	.hit {
		fill: transparent;
	}
	.auto,
	.people,
	.fail {
		transition:
			y 420ms cubic-bezier(0.16, 1, 0.3, 1),
			height 420ms cubic-bezier(0.16, 1, 0.3, 1),
			opacity 160ms;
	}
	.auto {
		fill: color-mix(in srgb, var(--color-data) 70%, var(--color-panel));
	}
	.people {
		fill: var(--color-ink);
		opacity: 0.82;
	}
	.fail {
		fill: var(--color-fail);
		opacity: 0.85;
	}
	.bar.dim .auto,
	.bar.dim .people {
		opacity: 0.38;
	}
	.bar.on .people {
		opacity: 1;
	}
	.loading .auto,
	.loading .people {
		opacity: 0.3;
	}
	.brush {
		fill: color-mix(in srgb, var(--color-action) 10%, transparent);
		stroke: color-mix(in srgb, var(--color-action) 55%, transparent);
		stroke-width: 1;
	}
	.ticks {
		position: relative;
		height: 1.125rem;
		margin-top: 0.125rem;
		font-size: 0.6875rem;
		color: var(--color-muted);
		font-variant-numeric: tabular-nums;
	}
	.ticks span {
		position: absolute;
		top: 0;
		white-space: nowrap;
		transform: translateX(-50%);
	}
	.ticks span[data-anchor='start'] {
		transform: none;
	}
	@media (prefers-reduced-motion: reduce) {
		.auto,
		.people,
		.fail {
			transition: none;
		}
	}
</style>
