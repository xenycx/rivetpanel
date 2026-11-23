<script lang="ts" module>
	export type ChartSeries = {
		label: string;
		/** Token ('action' | 'data' | 'run' | 'warn' | 'fail' | 'muted' | 'ink'), 'cat:<n>' for the n-th categorical colour, or a CSS colour. */
		color?: string;
		/** Fill the area under the line. */
		fill?: boolean;
		/** Thin dashed line (peaks beside an average). */
		dashed?: boolean;
		/** Stack on the series stacked before it (filled bands). */
		stacked?: boolean;
		/** Start hidden (the legend can show it). */
		hidden?: boolean;
		/** Value formatter for the legend; defaults to the chart's `format`. */
		format?: (n: number) => string;
		/** Leave out of the legend. */
		legend?: boolean;
	};
	/** Columnar data: one time array (ms) and one value array per series, null for gaps. */
	export type ChartData = { times: number[]; series: (number | null)[][] };
	/** A vertical marker at t, or a shaded region from t to end (end null = still going). */
	export type Annotation = { t: number; end?: number | null; kind?: string; label: string; color?: string };
	export type Threshold = { value: number; label?: string; color?: string };
</script>

<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';
	import { theme } from '$lib/ui/theme.svelte';
	import { eventTone } from '$lib/api/metrics';
	import { categoricalCss, readPalette, type Palette } from './colors';
	import { ChartGroup, type Span } from './sync.svelte';
	import { fmtStamp } from './timeRange.svelte';

	// A canvas time-series chart (uPlot). New data is pushed with setData;
	// the plot is only rebuilt when the set of series or the theme changes.
	// Drag to select a span (zooms the chart group), double-click to reset,
	// click to pick a moment (onpick).
	let {
		data,
		series,
		label,
		height = 160,
		format = (n: number) => String(Math.round(n * 10) / 10),
		yMin = 0,
		yMax,
		ySoftMax,
		xRange,
		annotations = [],
		threshold,
		group,
		onpick,
		loading = false,
		legend = true,
		empty = 'Not enough samples in this range yet.'
	}: {
		data: ChartData;
		series: ChartSeries[];
		/** Accessible name (a summary of the first series is added). */
		label: string;
		height?: number;
		/** Axis and legend formatter. */
		format?: (n: number) => string;
		yMin?: number;
		/** Fixed top of the y axis (100 for percent). */
		yMax?: number;
		/** The y axis reaches at least this far (a limit), more if the data does. */
		ySoftMax?: number;
		/** The x window to show (ms); defaults to the data's extent. */
		xRange?: Span | null;
		annotations?: Annotation[];
		threshold?: Threshold | null;
		/** Shared cursor and zoom with other charts. */
		group?: ChartGroup;
		/** A click on the plot: the sample time under the pointer and the annotation there, if any. */
		onpick?: (t: number, annotation: Annotation | null) => void;
		/** Shows a thin progress line (data stays visible). */
		loading?: boolean;
		legend?: boolean;
		empty?: string;
	} = $props();

	const grp = untrack(() => group) ?? new ChartGroup();

	let host: HTMLDivElement | undefined = $state();
	let u: uPlot | null = null;
	let pal: Palette | null = null;
	let hidden = $state<string[]>(untrack(() => series.filter((s) => s.hidden).map((s) => s.label)));
	let cursorIdx = $state<number | null>(null);
	let hoverAnn = $state<{ a: Annotation; left: number } | null>(null);

	// Plain copies for the draw hooks (read on every frame, not tracked).
	let annNow: Annotation[] = [];
	let thrNow: Threshold | null | undefined;
	let timesNow: number[] = [];

	// uPlot draws series in order: stacked bands first (top of the stack
	// first, opaque, so each lower band covers the one above it), then lines.
	const order = $derived.by(() => {
		const stacked = series.map((s, i) => ({ s, i })).filter((x) => x.s.stacked).map((x) => x.i);
		const rest = series.map((s, i) => ({ s, i })).filter((x) => !x.s.stacked).map((x) => x.i);
		return [...stacked.reverse(), ...rest];
	});
	const structure = $derived(series.map((s) => [s.label, s.color, s.fill, s.dashed, s.stacked].join('~')).join('|'));

	const aligned = $derived.by<uPlot.AlignedData>(() => {
		const n = data.times.length;
		const hide = new Set(hidden);
		const cols: (number | null)[][] = series.map((_, i) => data.series[i] ?? new Array(n).fill(null));
		// Cumulative values for stacked series (hidden ones drop out of the stack).
		const acc: (number | null)[][] = cols.slice();
		let below: (number | null)[] | null = null;
		series.forEach((s, i) => {
			if (!s.stacked || hide.has(s.label)) return;
			const col = cols[i];
			const out = new Array<number | null>(n);
			for (let k = 0; k < n; k++) {
				const v = col[k];
				const b = below ? below[k] : null;
				out[k] = v === null || v === undefined ? b : (b ?? 0) + v;
			}
			acc[i] = out;
			below = out;
		});
		return [data.times, ...order.map((i) => acc[i])] as uPlot.AlignedData;
	});

	function niceMax(v: number): number {
		if (!(v > 0)) return 1;
		const e = 10 ** Math.floor(Math.log10(v));
		for (const m of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (v <= m * e) return m * e;
		return 10 * e;
	}

	function xWindow(): Span | null {
		if (grp.zoom) return grp.zoom;
		if (xRange) return xRange;
		const t = data.times;
		return t.length > 1 ? { start: t[0], end: t[t.length - 1] } : null;
	}
	function applyX() {
		const w = xWindow();
		if (u && w && w.end > w.start) u.setScale('x', { min: w.start, max: w.end });
	}

	function tickFmt(v: number, span: number): string {
		const d = new Date(v);
		if (span > 3 * 86400_000) return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
		if (span > 36 * 3600_000) return d.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' });
		return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: span <= 10 * 60_000 ? '2-digit' : undefined });
	}

	function annColor(a: Annotation) {
		return a.color ?? eventTone(a.kind ?? '');
	}

	function build(width: number): uPlot {
		const p = (pal = readPalette());
		const font = `10px ${p.font}`;
		const opts: uPlot.Options = {
			width,
			height,
			ms: 1,
			legend: { show: false },
			padding: [8, 8, 0, 0],
			scales: {
				x: { time: true },
				y: {
					range: (_u, _min, max) => {
						if (yMax !== undefined) return [yMin, yMax];
						const top = Math.max(max ?? 0, ySoftMax ?? 0, thrNow?.value ?? 0);
						return [yMin, niceMax(top * 1.08)];
					}
				}
			},
			axes: [
				{
					stroke: p.muted,
					font,
					size: 22,
					space: 72,
					gap: 4,
					ticks: { show: false },
					grid: { stroke: p.ruleSoft, width: 1 },
					values: (self, splits) => {
						const span = (self.scales.x.max ?? 0) - (self.scales.x.min ?? 0);
						return splits.map((v) => tickFmt(v, span));
					}
				},
				{
					stroke: p.muted,
					font,
					space: 28,
					gap: 6,
					ticks: { show: false },
					grid: { stroke: p.ruleSoft, width: 1, dash: [2, 3] },
					values: (_self, splits) => splits.map((v) => format(v)),
					size: (_self, values) => Math.max(28, Math.ceil(Math.max(0, ...(values ?? []).map((s) => String(s).length)) * 6.2) + 12)
				}
			],
			cursor: {
				sync: { key: grp.key, setSeries: false },
				drag: { x: true, y: false, setScale: false },
				points: { size: 7, width: 1.5, fill: `rgb(${p.panel.join(',')})` },
				y: false
			},
			series: [
				{},
				...order.map((i): uPlot.Series => {
					const s = series[i];
					const stroke = p.color(s.color);
					return {
						label: s.label,
						show: !hidden.includes(s.label),
						stroke: s.dashed ? p.alpha(s.color, 0.75) : stroke,
						width: s.dashed ? 1 : s.stacked ? 1 : 1.5,
						dash: s.dashed ? [3, 3] : undefined,
						fill: s.stacked ? p.tint(s.color, p.dark ? 0.42 : 0.3) : s.fill ? p.alpha(s.color, 0.12) : undefined,
						points: { show: false },
						spanGaps: false
					};
				})
			],
			hooks: {
				drawClear: [
					(self) => {
						// Shaded regions (spikes) under the series.
						const ctx = self.ctx;
						const { left, top, width: w, height: h } = self.bbox;
						ctx.save();
						for (const a of annNow) {
							if (a.end === undefined) continue;
							const end = a.end ?? timesNow[timesNow.length - 1] ?? Date.now();
							const x0 = Math.max(left, self.valToPos(a.t, 'x', true));
							const x1 = Math.min(left + w, self.valToPos(end, 'x', true));
							if (x1 <= left || x0 >= left + w) continue;
							ctx.fillStyle = p.alpha(annColor(a), p.dark ? 0.14 : 0.1);
							ctx.fillRect(x0, top, Math.max(2, x1 - x0), h);
						}
						ctx.restore();
					}
				],
				draw: [
					(self) => {
						const ctx = self.ctx;
						const dpr = devicePixelRatio || 1;
						const { left, top, width: w, height: h } = self.bbox;
						ctx.save();
						ctx.lineWidth = dpr;
						if (thrNow) {
							const y = Math.round(self.valToPos(thrNow.value, 'y', true)) + 0.5;
							if (y >= top && y <= top + h) {
								ctx.strokeStyle = p.alpha(thrNow.color ?? 'warn', 0.8);
								ctx.setLineDash([4 * dpr, 4 * dpr]);
								ctx.beginPath();
								ctx.moveTo(left, y);
								ctx.lineTo(left + w, y);
								ctx.stroke();
								if (thrNow.label) {
									ctx.setLineDash([]);
									ctx.font = `${10 * dpr}px ${p.font}`;
									ctx.fillStyle = p.color(thrNow.color ?? 'warn');
									ctx.textAlign = 'right';
									ctx.fillText(thrNow.label, left + w - 4 * dpr, y - 4 * dpr);
								}
							}
						}
						ctx.setLineDash([3 * dpr, 3 * dpr]);
						for (const a of annNow) {
							if (a.end !== undefined) continue;
							const x = Math.round(self.valToPos(a.t, 'x', true)) + 0.5;
							if (x < left || x > left + w) continue;
							ctx.strokeStyle = p.alpha(annColor(a), 0.75);
							ctx.beginPath();
							ctx.moveTo(x, top);
							ctx.lineTo(x, top + h);
							ctx.stroke();
							// A small flag at the top marks where to hover.
							ctx.fillStyle = p.color(annColor(a));
							ctx.beginPath();
							ctx.moveTo(x - 4 * dpr, top);
							ctx.lineTo(x + 4 * dpr, top);
							ctx.lineTo(x, top + 5 * dpr);
							ctx.closePath();
							ctx.fill();
						}
						ctx.restore();
					}
				],
				setCursor: [
					(self) => {
						const idx = self.cursor.idx;
						const left = self.cursor.left ?? -1;
						cursorIdx = left < 0 ? null : (idx ?? null);
						if (left < 0 || idx == null) {
							if (grp.hoverT !== null && self.cursor.event) grp.hoverT = null;
							hoverAnn = null;
							return;
						}
						if (self.cursor.event) grp.hoverT = timesNow[idx] ?? null;
						let best: { a: Annotation; left: number } | null = null;
						let bestD = 6;
						const t = self.posToVal(left, 'x');
						for (const a of annNow) {
							const x = self.valToPos(a.t, 'x');
							const inRegion = a.end !== undefined && t >= a.t && t <= (a.end ?? Infinity);
							const d = inRegion ? 5 : Math.abs(x - left);
							if (d < bestD) {
								bestD = d;
								best = { a, left: Math.max(x, 0) + (parseFloat(self.over.style.left) || 0) };
							}
						}
						hoverAnn = best;
					}
				],
				setSelect: [
					(self) => {
						const s = self.select;
						if (s.width < 4) return;
						const start = self.posToVal(s.left, 'x');
						const end = self.posToVal(s.left + s.width, 'x');
						self.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
						grp.select({ start, end });
					}
				]
			}
		};
		const plot = new uPlot(opts, aligned, host!);
		let downX = 0;
		plot.over.addEventListener('mousedown', (e) => (downX = e.clientX));
		plot.over.addEventListener('click', (e) => {
			if (Math.abs(e.clientX - downX) > 3 || !onpick) return;
			const idx = plot.cursor.idx;
			const left = plot.cursor.left ?? -1;
			if (left < 0) return;
			const t = idx != null && timesNow[idx] !== undefined ? timesNow[idx] : plot.posToVal(left, 'x');
			onpick(t, hoverAnn?.a ?? null);
		});
		plot.over.addEventListener('dblclick', () => {
			grp.reset();
			// uPlot resets x to the data on double-click; put the window back.
			queueMicrotask(applyX);
		});
		plot.over.style.cursor = onpick ? 'pointer' : 'crosshair';
		return plot;
	}

	// (Re)build when the series set or the theme changes.
	$effect(() => {
		void structure;
		void theme.dark;
		void theme.accent;
		if (!host) return;
		const el = host;
		untrack(() => {
			timesNow = data.times;
			annNow = annotations;
			thrNow = threshold;
			u = build(el.clientWidth || 300);
			applyX();
		});
		return () => {
			u?.destroy();
			u = null;
		};
	});

	// New data: no rebuild, just setData (and keep the x window).
	$effect(() => {
		const d = aligned;
		timesNow = data.times;
		if (!u || u.series.length !== d.length) return;
		untrack(() => {
			u!.batch(() => {
				u!.setData(d, true);
				applyX();
			});
		});
	});

	$effect(() => {
		void grp.zoom;
		void xRange?.start;
		void xRange?.end;
		untrack(applyX);
	});

	$effect(() => {
		const hide = new Set(hidden);
		if (!u) return;
		const plot = u;
		untrack(() => {
			plot.batch(() => order.forEach((i, k) => plot.series[k + 1]?.show !== !hide.has(series[i].label) && plot.setSeries(k + 1, { show: !hide.has(series[i].label) })));
		});
	});

	$effect(() => {
		annNow = annotations;
		thrNow = threshold;
		if (u) untrack(() => u!.redraw(false, false));
	});

	$effect(() => {
		if (u && u.height !== height) untrack(() => u!.setSize({ width: u!.width, height }));
	});

	onMount(() => {
		let raf = 0;
		const ro = new ResizeObserver(() => {
			cancelAnimationFrame(raf);
			raf = requestAnimationFrame(() => {
				const w = host?.clientWidth ?? 0;
				if (u && w > 0 && w !== u.width) u.setSize({ width: w, height });
			});
		});
		if (host) ro.observe(host);
		return () => {
			cancelAnimationFrame(raf);
			ro.disconnect();
		};
	});

	function toggle(s: ChartSeries, e: MouseEvent) {
		if (e.altKey || e.metaKey || e.ctrlKey) {
			const others = series.filter((x) => x.label !== s.label && x.legend !== false).map((x) => x.label);
			const isolated = !hidden.includes(s.label) && others.every((l) => hidden.includes(l));
			hidden = isolated ? [] : others;
		} else hidden = hidden.includes(s.label) ? hidden.filter((l) => l !== s.label) : [...hidden, s.label];
	}

	function lastValue(col: (number | null)[] | undefined): number | null {
		if (!col) return null;
		for (let k = col.length - 1; k >= 0; k--) if (col[k] !== null && col[k] !== undefined) return col[k];
		return null;
	}
	const legendItems = $derived(
		series
			.map((s, i) => ({ s, i }))
			.filter((x) => x.s.legend !== false)
			.map(({ s, i }) => {
				const col = data.series[i];
				const v = cursorIdx !== null ? (col?.[cursorIdx] ?? null) : lastValue(col);
				return { s, v: v === null ? '—' : (s.format ?? format)(v) };
			})
	);
	const cursorTime = $derived(cursorIdx !== null ? data.times[cursorIdx] : null);
	const span = $derived(data.times.length > 1 ? data.times[data.times.length - 1] - data.times[0] : 0);
	const summary = $derived.by(() => {
		const vs = (data.series[0] ?? []).filter((v): v is number => v !== null && v !== undefined);
		if (!vs.length) return `${label}: no samples.`;
		let lo = Infinity,
			hi = -Infinity,
			sum = 0;
		for (const v of vs) {
			lo = Math.min(lo, v);
			hi = Math.max(hi, v);
			sum += v;
		}
		return `${label}: now ${format(vs[vs.length - 1])}, lowest ${format(lo)}, highest ${format(hi)}, average ${format(sum / vs.length)}.`;
	});
	const swatch: Record<string, string> = { action: 'bg-action', data: 'bg-data', run: 'bg-run', warn: 'bg-warn', fail: 'bg-fail', muted: 'bg-muted', ink: 'bg-ink' };
	const catCss = (c: string) => categoricalCss(Number(c.slice(4)) || 0, theme.dark);
</script>

<div class="uchart relative min-w-0">
	<div class="relative" role="img" aria-label={summary}>
		{#if loading}<div class="uchart-progress" aria-hidden="true"></div>{/if}
		<div bind:this={host} class="min-w-0 select-none" style="height: {height}px"></div>
		{#if data.times.length < 2}
			<p class="pointer-events-none absolute inset-0 grid place-items-center text-small text-muted">{empty}</p>
		{/if}
		{#if hoverAnn}
			<div class="pointer-events-none absolute top-1 z-10 max-w-64 -translate-x-1/2 rounded-control border border-rule bg-raised px-2 py-1 text-small shadow-overlay" style="left: clamp(4rem, {hoverAnn.left}px, calc(100% - 4rem))" aria-hidden="true">
				<p class="font-mono text-label text-muted tabular-nums">{fmtStamp(hoverAnn.a.t, span)}{#if hoverAnn.a.end !== undefined}{' '}– {hoverAnn.a.end ? fmtStamp(hoverAnn.a.end, span) : 'now'}{/if}</p>
				<p class="leading-snug">{hoverAnn.a.label}</p>
			</div>
		{/if}
	</div>
	{#if legend}
		<div class="mt-1.5 flex flex-wrap items-center gap-x-1 gap-y-0.5 text-small">
			{#if cursorTime !== null}<span class="mr-1 font-mono text-label text-muted tabular-nums">{fmtStamp(cursorTime, span)}</span>{/if}
			{#each legendItems as it (it.s.label)}
				{@const off = hidden.includes(it.s.label)}
				<button
					type="button"
					class="flex items-center gap-1.5 rounded-control px-1.5 py-0.5 hover:bg-paper-2/70 {off ? 'text-muted opacity-55' : ''}"
					aria-pressed={!off}
					title="Click to show or hide · Alt-click to show only this"
					onclick={(e) => toggle(it.s, e)}
				>
					{#if it.s.color?.startsWith('cat:')}
						<span class="inline-block size-2 shrink-0 rounded-xs" style="background: {catCss(it.s.color)}"></span>
					{:else}
						<span class="inline-block shrink-0 {it.s.dashed ? 'h-0 w-3 border-t border-dashed border-current' : 'size-2 rounded-xs'} {it.s.dashed ? '' : (swatch[it.s.color ?? 'data'] ?? 'bg-data')}" style={it.s.color && !swatch[it.s.color] && !it.s.dashed ? `background: ${it.s.color}` : ''}></span>
					{/if}
					<span class="max-w-40 truncate {off ? 'line-through' : 'text-muted'}">{it.s.label}</span>
					<span class="font-mono font-medium tabular-nums">{it.v}</span>
				</button>
			{/each}
		</div>
	{/if}
</div>

<style>
	.uchart :global(.u-select) {
		background: color-mix(in srgb, var(--color-action) 14%, transparent);
	}
	.uchart :global(.u-cursor-x) {
		border-right: 1px dashed color-mix(in srgb, var(--color-muted) 70%, transparent);
	}
	.uchart :global(.uplot) {
		font-family: var(--font-mono);
	}
	.uchart-progress {
		position: absolute;
		inset: 0 0 auto 0;
		height: 2px;
		z-index: 5;
		overflow: hidden;
		background: linear-gradient(90deg, transparent, var(--color-action), transparent);
		background-size: 40% 100%;
		background-repeat: no-repeat;
		animation: uchart-progress 1.1s linear infinite;
		opacity: 0.7;
	}
	@keyframes uchart-progress {
		from {
			background-position: -40% 0;
		}
		to {
			background-position: 140% 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.uchart-progress {
			animation: none;
			background: var(--color-action);
			opacity: 0.35;
		}
	}
</style>
