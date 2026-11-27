// Stale-while-revalidate time series. A store keeps what it shows while it
// loads the next window (no blanking, no layout jump), caches answers by
// window key, aborts a request that a new window made pointless, and
// refreshes a moving window incrementally: it asks only for buckets after the
// second-to-last one it has (the last may still have been filling), appends
// them and drops what fell off the left edge, so the chart scrolls.

import { ApiError } from '$lib/api/client';
import type { SeriesQuery, Window } from '$lib/api/metrics';

export type Columnar = { times: number[]; step_ms?: number };

const cache = new Map<string, unknown>();
const CACHE_MAX = 48;
function remember(key: string, v: unknown) {
	cache.delete(key);
	cache.set(key, v);
	if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value!);
}

/** Every top-level array as long as `times` is a column. */
function columns(d: Columnar): string[] {
	const n = d.times.length;
	const r = d as unknown as Record<string, unknown>;
	return Object.keys(d).filter((k) => {
		const a = r[k];
		return Array.isArray(a) && a.length === n && a.every((x) => x === null || typeof x === 'number');
	});
}

/** Drops the samples before `start`. */
export function trimLeft<T extends Columnar>(d: T, start: number): T {
	const at = d.times.findIndex((t) => t >= start);
	const lo = at < 0 ? d.times.length : at;
	if (lo === 0) return d;
	const out = { ...d } as Record<string, unknown>;
	for (const k of columns(d)) out[k] = (d as unknown as Record<string, unknown[]>)[k].slice(lo);
	// Per-entry columns ({ values: [...] } lists, such as the apps of /series/bots).
	for (const [k, v] of Object.entries(d))
		if (Array.isArray(v) && v.length && v.every((x) => x && typeof x === 'object' && Array.isArray((x as { values?: unknown }).values)))
			out[k] = (v as unknown as { values: unknown[] }[]).map((x) => (x.values.length === d.times.length ? { ...x, values: x.values.slice(lo) } : x));
	return out as T;
}

/**
 * prev up to and including `since`, then inc; trimmed to start. Scalars come
 * from inc, except a total the server could not know: an answer with no new
 * buckets reports mem_total and disk_total as 0, and those must not replace
 * the real figures.
 */
export function mergeAfter<T extends Columnar>(prev: T, inc: T, since: number, start: number): T {
	// A server that ignored `since` sends everything: keep only what is new.
	const first = inc.times.findIndex((t) => t > since);
	if (first !== 0) inc = trimLeft(inc, first < 0 ? Infinity : inc.times[first]);
	const keep = prev.times.findIndex((t) => t > since);
	const hi = keep < 0 ? prev.times.length : keep;
	const out = { ...prev, ...inc } as Record<string, unknown>;
	const p = prev as unknown as Record<string, unknown[]>;
	const q = inc as unknown as Record<string, unknown[]>;
	const pv = prev as unknown as Record<string, unknown>;
	for (const [k, v] of Object.entries(inc as unknown as Record<string, unknown>))
		if ((v === 0 || v === null || v === undefined) && typeof pv[k] === 'number' && pv[k] !== 0) out[k] = pv[k];
	for (const k of columns(prev)) out[k] = p[k].slice(0, hi).concat(Array.isArray(q[k]) ? q[k] : new Array(inc.times.length).fill(null));
	return trimLeft(out as T, start);
}

/** Appends one point; columns missing from `values` repeat their last value. */
export function appendPoint<T extends Columnar>(d: T, t: number, values: Record<string, number | null>, start: number): T {
	if (d.times.length && t <= d.times[d.times.length - 1]) return d;
	const out = { ...d } as Record<string, unknown>;
	const p = d as unknown as Record<string, (number | null)[]>;
	for (const k of columns(d)) {
		const col = p[k];
		out[k] = col.concat([k === 'times' ? t : k in values ? values[k] : (col[col.length - 1] ?? null)]);
	}
	return trimLeft(out as T, start);
}

export function estimateStep(times: number[]): number | undefined {
	if (times.length < 3) return undefined;
	const d: number[] = [];
	for (let i = 1; i < times.length; i++) d.push(times[i] - times[i - 1]);
	d.sort((a, b) => a - b);
	return d[Math.floor(d.length / 2)];
}

export type Fetcher<T> = (q: SeriesQuery, signal: AbortSignal) => Promise<T>;

/** How long one series request may take before it counts as failed. */
export const REQUEST_TIMEOUT_MS = 25_000;

/**
 * An abort controller that also aborts itself after `ms`. A request that
 * never answers (a dropped connection, a laptop waking from sleep) must not
 * hold the store, or the poll that waits for it, forever: the window keeps
 * moving while the data does not, until the charts are empty.
 */
export function timedController(ms = REQUEST_TIMEOUT_MS): { ctrl: AbortController; timedOut: () => boolean; done: () => void } {
	const ctrl = new AbortController();
	let late = false;
	const timer = setTimeout(() => {
		late = true;
		ctrl.abort();
	}, ms);
	return { ctrl, timedOut: () => late, done: () => clearTimeout(timer) };
}

export class SeriesStore<T extends Columnar> {
	data = $state.raw<T | null>(null);
	loading = $state(false);
	error = $state('');
	#ctrl: AbortController | null = null;
	#key = '';
	#fetch: Fetcher<T>;
	#incremental: boolean;
	#points: number;
	#errorText: string;

	constructor(fetcher: Fetcher<T>, opts: { incremental?: boolean; points?: number; errorText?: string } = {}) {
		this.#fetch = fetcher;
		this.#incremental = opts.incremental ?? true;
		this.#points = opts.points ?? 600;
		this.#errorText = opts.errorText ?? 'These figures are unavailable right now.';
	}

	/** The window key the store shows (or is loading). */
	get key() {
		return this.#key;
	}

	/**
	 * Shows window `key`. A cached answer appears at once (and is then brought
	 * up to date); otherwise the current data stays until the answer arrives.
	 */
	async load(key: string, w: Window, moving = false) {
		this.#ctrl?.abort();
		this.#ctrl = null;
		this.#key = key;
		const hit = cache.get(key) as T | undefined;
		if (hit) {
			this.data = moving ? trimLeft(hit, w.start) : hit;
			if (moving) return this.refresh(w);
			return;
		}
		await this.#full(key, w);
	}

	async #full(key: string, w: Window) {
		const { ctrl, timedOut, done } = timedController();
		this.#ctrl = ctrl;
		this.loading = true;
		try {
			const d = await this.#fetch({ ...w, points: this.#points }, ctrl.signal);
			if (ctrl.signal.aborted || this.#key !== key) return;
			this.data = d;
			this.error = '';
			remember(key, d);
		} catch (e) {
			if (ctrl.signal.aborted && !timedOut()) return;
			this.error = e instanceof ApiError && e.status !== 404 ? e.message : this.#errorText;
		} finally {
			done();
			if (this.#ctrl === ctrl) {
				this.#ctrl = null;
				this.loading = false;
			}
		}
	}

	/** Brings a moving window up to date (incrementally when it can). */
	async refresh(w: Window) {
		if (this.#ctrl) return; // a load is running; it is newer anyway
		const key = this.#key;
		const prev = this.data;
		// Nothing to build on, or what is held has slid (almost) out of the
		// window (a long sleep, failed refreshes): load the window again.
		if (!prev || !this.#incremental || prev.times.length < 3 || prev.times[prev.times.length - 1] < w.start + (w.end - w.start) / 2) return this.#full(key, w);
		const since = prev.times[prev.times.length - 2];
		const step = prev.step_ms ?? estimateStep(prev.times);
		const { ctrl, timedOut, done } = timedController();
		this.#ctrl = ctrl;
		try {
			const inc = await this.#fetch({ ...w, step, since }, ctrl.signal);
			if (ctrl.signal.aborted || this.#key !== key || this.data !== prev) return;
			this.data = mergeAfter(prev, inc, since, w.start);
			this.error = '';
			remember(key, this.data);
		} catch (e) {
			if (ctrl.signal.aborted && !timedOut()) return;
			this.error = e instanceof ApiError && e.status !== 404 ? e.message : this.#errorText;
		} finally {
			done();
			if (this.#ctrl === ctrl) this.#ctrl = null;
		}
	}

	/** Live: append one sample (columns absent from values repeat their last value). */
	push(t: number, values: Record<string, number | null>, start: number) {
		if (!this.data) return;
		this.data = appendPoint(this.data, t, values, start);
	}

	/** Shows the current data shifted to a new left edge, without a request. */
	trim(start: number) {
		if (this.data) this.data = trimLeft(this.data, start);
	}

	abort() {
		this.#ctrl?.abort();
		this.#ctrl = null;
		this.loading = false;
	}
}
