<script lang="ts" module>
	export type Series = {
		label: string;
		values: (number | null)[];
		color?: 'action' | 'run' | 'warn' | 'fail' | 'muted';
		/** Fill under the line. */
		area?: boolean;
		/** Thin dashed line, for peaks beside an average. */
		dashed?: boolean;
	};
</script>

<script lang="ts">
	// A small, honest time-series chart: labelled axes, a hover read-out with
	// every series at that moment, and a text summary for screen readers. Values
	// are drawn as given; nothing is smoothed.
	let {
		times,
		series,
		format = (n: number) => String(Math.round(n * 10) / 10),
		max,
		height = 150,
		label
	}: { times: number[]; series: Series[]; format?: (n: number) => string; max?: number; height?: number; label: string } = $props();

	let width = $state(0);
	let hover = $state<number | null>(null);
	let tipW = $state(0);
	let tipH = $state(0);

	function niceMax(v: number): number {
		if (v <= 0) return 1;
		const e = 10 ** Math.floor(Math.log10(v));
		for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * e) return m * e;
		return 10 * e;
	}
	const top = $derived.by(() => {
		if (max !== undefined) return max;
		let m = 0;
		for (const s of series) for (const v of s.values) if (v !== null && v > m) m = v;
		return niceMax(m * 1.08);
	});
	// Reserve the left gutter from the widest y-axis label (10px monospace is
	// about 6.1px per character) so long values such as "40 KiB/s" are not cut off.
	const gutter = $derived(Math.max(28, Math.ceil(Math.max(...[0, 0.5, 1].map((f) => format(top * f).length)) * 6.1) + 10));
	const pad = $derived({ l: gutter, r: 8, t: 8, b: 20 });
	const iw = $derived(Math.max(0, width - pad.l - pad.r));
	const ih = $derived(height - pad.t - pad.b);
	const x = (i: number) => pad.l + (times.length > 1 ? (i / (times.length - 1)) * iw : iw / 2);
	const y = (v: number) => pad.t + ih - (Math.min(Math.max(v, 0), top) / top) * ih;

	// Break the line at gaps (null) so a missing sample is not drawn as a value.
	function segments(values: (number | null)[]): string[] {
		const out: string[] = [];
		let cur: string[] = [];
		values.forEach((v, i) => {
			if (v === null) {
				if (cur.length) out.push(cur.join(' '));
				cur = [];
			} else cur.push(`${cur.length ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`);
		});
		if (cur.length) out.push(cur.join(' '));
		return out;
	}
	const areaPath = (values: (number | null)[]) => {
		const pts = values.map((v, i) => (v === null ? null : [x(i), y(v)])).filter(Boolean) as number[][];
		if (pts.length < 2) return '';
		return `M${pts[0][0].toFixed(1)},${(pad.t + ih).toFixed(1)} ` + pts.map((p) => `L${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' ') + ` L${pts.at(-1)![0].toFixed(1)},${(pad.t + ih).toFixed(1)}Z`;
	};

	const yTicks = $derived([0, 0.5, 1].map((f) => ({ v: top * f, y: y(top * f) })));
	const span = $derived(times.length > 1 ? times.at(-1)! - times[0] : 0);
	const fmtT = (t: number) => {
		const d = new Date(t);
		return span > 36 * 3600_000 ? d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
	};
	// Up to four evenly spaced labels, fewer on a narrow chart so they never run
	// into each other (about 80px per label); with only a few samples several would
	// land on the same one, so each index is used once (duplicate keys would also break the list).
	const xCount = $derived(Math.min(4, Math.max(2, Math.floor(iw / 80) + 1)));
	const xTicks = $derived(
		times.length < 2
			? []
			: [...new Set(Array.from({ length: xCount }, (_, k) => Math.round((k / (xCount - 1)) * (times.length - 1))))].map((i) => ({ i, label: fmtT(times[i]), anchor: i === 0 ? 'start' : i === times.length - 1 ? 'end' : 'middle' }))
	);

	// The whole plot height is the hover target: the nearest sample on the x axis
	// wins regardless of where the pointer is vertically, so a narrow spike is as
	// easy to read as a flat stretch. Touch works the same way (tap or drag).
	function move(e: PointerEvent) {
		if (times.length < 1 || iw <= 0) return;
		const r = (e.currentTarget as SVGElement).getBoundingClientRect();
		const f = (e.clientX - r.left - pad.l) / iw;
		hover = Math.min(times.length - 1, Math.max(0, Math.round(f * (times.length - 1))));
	}
	function leave(e: PointerEvent) {
		// A finger lifting off fires pointerleave; keep the read-out until the next touch.
		if (e.pointerType === 'mouse') hover = null;
	}
	const colorClass = { action: 'text-data', run: 'text-run', warn: 'text-warn', fail: 'text-fail', muted: 'text-muted' } as const;
	// The read-out sits beside the hovered column, never on it, and never takes
	// pointer events. Of the four corners around the guide line (right/left, top/
	// bottom of the plot) it takes the one covering the fewest drawn points, so a
	// spike at or next to the cursor stays visible and reachable.
	const tip = $derived.by(() => {
		if (hover === null || hover >= times.length) return null;
		const i = hover;
		const cx = x(i);
		const gap = 12;
		const w = tipW || 160;
		const h = tipH || 60;
		const clampL = (l: number) => Math.min(Math.max(l, 2), Math.max(2, width - w - 2));
		const clampT = (t: number) => Math.min(Math.max(t, 0), Math.max(0, height - h));
		const lefts = [clampL(cx + gap), clampL(cx - gap - w)];
		const tops = [clampT(pad.t), clampT(pad.t + ih - h)];
		let best = { left: lefts[0], top: tops[0], score: Infinity };
		for (const [li, left] of lefts.entries())
			for (const [ti, top] of tops.entries()) {
				let score = li * 0.5 + ti * 0.25; // prefer right, then top, on ties
				const coversColumn = left <= cx + 4 && left + w >= cx - 4;
				for (const s of series)
					s.values.forEach((v, k) => {
						if (v === null || v === undefined) return;
						const px = x(k);
						const py = y(v);
						if (px >= left - 4 && px <= left + w + 4 && py >= top - 4 && py <= top + h + 4) score += k === i ? 1000 : 1;
					});
				if (coversColumn) score += 5;
				if (score < best.score) best = { left, top, score };
			}
		return { i, left: best.left, top: best.top };
	});

	const summary = $derived.by(() => {
		const s = series[0];
		const vs = s?.values.filter((v): v is number => v !== null) ?? [];
		if (!vs.length) return `${label}: no samples.`;
		return `${label}: now ${format(vs.at(-1)!)}, lowest ${format(Math.min(...vs))}, highest ${format(Math.max(...vs))}, average ${format(vs.reduce((a, b) => a + b, 0) / vs.length)}.`;
	});
</script>

<div class="min-w-0" bind:clientWidth={width}>
	{#if series.filter((s) => !s.dashed).length > 1}
		<ul class="mb-1 flex flex-wrap gap-x-4 gap-y-0.5 text-small text-muted" aria-hidden="true">
			{#each series.filter((s) => !s.dashed) as s (s.label)}
				<li class="flex items-center gap-1.5"><span class="inline-block h-0.5 w-3.5 rounded-pill bg-current {colorClass[s.color ?? 'action']}"></span>{s.label}</li>
			{/each}
		</ul>
	{/if}
	{#if times.length < 2}
		<p class="grid place-items-center text-small text-muted" style="height: {height}px">Not enough samples in this range yet.</p>
	{:else if width > 0}
		<div class="relative">
			<svg {width} {height} role="img" aria-label={summary} class="block touch-pan-y select-none" onpointermove={move} onpointerdown={move} onpointerleave={leave}>
				{#each yTicks as t, k (k)}
					<line x1={pad.l} x2={width - pad.r} y1={t.y} y2={t.y} stroke="var(--color-rule-soft)" stroke-dasharray={t.v === 0 ? undefined : '2 3'} />
					<text x={pad.l - 6} y={t.y + 3.5} text-anchor="end" class="fill-muted font-mono text-[10px]">{format(t.v)}</text>
				{/each}
				{#each xTicks as t (t.i)}
					<text x={x(t.i)} y={height - 5} text-anchor={t.anchor} class="fill-muted font-mono text-[10px]">{t.label}</text>
				{/each}
				{#each series as s (s.label)}
					<g class={colorClass[s.color ?? 'action']}>
						{#if s.area}<path d={areaPath(s.values)} fill="currentColor" fill-opacity="0.12" />{/if}
						{#each segments(s.values) as d, k (k)}
							<path {d} fill="none" stroke="currentColor" stroke-width={s.dashed ? 1 : 1.6} stroke-dasharray={s.dashed ? '3 3' : undefined} stroke-opacity={s.dashed ? 0.6 : 1} stroke-linejoin="round" stroke-linecap="round" />
						{/each}
					</g>
				{/each}
				<rect x={pad.l} y="0" width={iw} {height} fill="transparent" />
				{#if tip}
					<line x1={x(tip.i)} x2={x(tip.i)} y1={pad.t} y2={pad.t + ih} pointer-events="none" stroke="var(--color-muted)" stroke-opacity="0.5" />
					{#each series.filter((s) => !s.dashed) as s (s.label)}
						{#if s.values[tip.i] !== null && s.values[tip.i] !== undefined}
							<circle cx={x(tip.i)} cy={y(s.values[tip.i]!)} r="3.5" pointer-events="none" class="{colorClass[s.color ?? 'action']}" fill="var(--color-panel)" stroke="currentColor" stroke-width="1.6" />
						{/if}
					{/each}
				{/if}
			</svg>
			{#if tip}
				<div class="pointer-events-none absolute z-10 rounded-control border border-rule bg-raised px-2.5 py-1.5 text-small shadow-overlay" style="left: {tip.left}px; top: {tip.top}px; pointer-events: none" bind:clientWidth={tipW} bind:clientHeight={tipH} aria-hidden="true">
					<p class="font-mono text-[11px] text-muted">{new Date(times[tip.i]).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}</p>
					{#each series as s (s.label)}
						{#if s.values[tip.i] !== null && s.values[tip.i] !== undefined}
							<p class="flex items-center justify-between gap-4 whitespace-nowrap"><span class="flex items-center gap-1.5 text-muted"><span class="inline-block size-2 rounded-pill bg-current {colorClass[s.color ?? 'action']}"></span>{s.label}</span><span class="font-mono font-medium">{format(s.values[tip.i]!)}</span></p>
						{/if}
					{/each}
				</div>
			{/if}
		</div>
	{/if}
</div>
