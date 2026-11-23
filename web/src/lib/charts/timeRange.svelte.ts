// Grafana-like time range state: a relative preset ("now-1h" … "now") or an
// absolute span (from a drag-zoom or the picker), a refresh interval and a
// Live toggle. Optionally mirrored in the URL (?from=&to=&refresh=&live=)
// so a view can be shared and survives a reload.

import { untrack } from 'svelte';
import { replaceState } from '$app/navigation';
import { page } from '$app/state';

export type Preset = { id: string; label: string; ms: number };

export const presets: Preset[] = [
	{ id: '5m', label: '5m', ms: 5 * 60_000 },
	{ id: '15m', label: '15m', ms: 15 * 60_000 },
	{ id: '1h', label: '1h', ms: 3600_000 },
	{ id: '6h', label: '6h', ms: 6 * 3600_000 },
	{ id: '24h', label: '24h', ms: 24 * 3600_000 },
	{ id: '7d', label: '7d', ms: 7 * 86400_000 },
	{ id: '30d', label: '30d', ms: 30 * 86400_000 }
];

export const refreshOptions = [
	{ ms: 0, label: 'Off' },
	{ ms: 5_000, label: '5s' },
	{ ms: 10_000, label: '10s' },
	{ ms: 30_000, label: '30s' },
	{ ms: 60_000, label: '1m' }
];

/** Longest relative range the Live stream is offered for. */
export const LIVE_MAX_MS = 3600_000;

const unitMs: Record<string, number> = { s: 1000, m: 60_000, h: 3600_000, d: 86400_000, w: 7 * 86400_000 };

/** "now-15m" → 900000; null when not a relative expression. */
export function parseRelative(s: string): number | null {
	const m = /^now-(\d+)([smhdw])$/.exec(s);
	return m ? Number(m[1]) * unitMs[m[2]] : null;
}
const isAbs = (s: string) => /^\d{10,14}$/.test(s);

export type RangeOptions = { preset?: string; refreshMs?: number; url?: boolean; presets?: Preset[] };

export class TimeRange {
	from = $state('now-1h');
	to = $state('now');
	refreshMs = $state(30_000);
	live = $state(false);
	readonly presets: Preset[];
	readonly #url: boolean;

	constructor(opts: RangeOptions = {}) {
		this.presets = opts.presets ?? presets;
		this.from = `now-${opts.preset ?? '1h'}`;
		this.refreshMs = opts.refreshMs ?? 30_000;
		this.#url = opts.url ?? false;
		if (this.#url && typeof location !== 'undefined') this.#read(new URL(location.href).searchParams);
	}

	#read(q: URLSearchParams) {
		const f = q.get('from');
		const t = q.get('to');
		if (f && t && ((parseRelative(f) !== null && t === 'now') || (isAbs(f) && isAbs(t) && +f < +t))) {
			this.from = f;
			this.to = t;
		}
		const r = Number(q.get('refresh'));
		if (q.has('refresh') && refreshOptions.some((o) => o.ms === r)) this.refreshMs = r;
		this.live = q.get('live') === '1' && this.canLive;
	}

	/** Relative to now (the window moves forward on refresh). */
	get relative() {
		return this.to === 'now' && parseRelative(this.from) !== null;
	}
	get span(): number {
		if (this.relative) return parseRelative(this.from)!;
		return Math.max(1000, +this.to - +this.from);
	}
	/** Changes whenever the asked-for window changes (not when "now" moves). */
	get key(): string {
		return `${this.from}|${this.to}`;
	}
	get preset(): Preset | undefined {
		return this.relative ? this.presets.find((p) => p.ms === this.span) : undefined;
	}
	get canLive() {
		return this.relative && this.span <= LIVE_MAX_MS;
	}

	/** The concrete window in ms. */
	resolve(now = Date.now()): { start: number; end: number } {
		if (this.relative) return { start: now - this.span, end: now };
		return { start: +this.from, end: +this.to };
	}

	setPreset(id: string) {
		const p = this.presets.find((x) => x.id === id);
		if (!p) return;
		this.from = `now-${p.id}`;
		this.to = 'now';
		if (!this.canLive) this.live = false;
	}

	setAbsolute(start: number, end: number) {
		if (!(end > start)) return;
		this.from = String(Math.round(start));
		this.to = String(Math.round(Math.min(end, Date.now())));
		this.live = false;
	}

	/** Doubles the window around its middle (Grafana's "zoom out"). */
	zoomOut() {
		const { start, end } = this.resolve();
		const mid = (start + end) / 2;
		const span = Math.min((end - start) * 2, 400 * 86400_000);
		const now = Date.now();
		if (mid + span / 2 >= now) {
			const p = this.presets.find((x) => x.ms >= span);
			if (p) return this.setPreset(p.id);
			return this.setAbsolute(now - span, now);
		}
		this.setAbsolute(mid - span / 2, mid + span / 2);
	}

	setLive(on: boolean) {
		this.live = on && this.canLive;
	}

	/** Human label of the window. */
	get label(): string {
		if (this.relative) return `Last ${this.preset?.label ?? fmtSpan(this.span)}`;
		const { start, end } = this.resolve();
		return `${fmtStamp(start, end - start)} – ${fmtStamp(end, end - start)}`;
	}

	/** Keeps the URL in step; call during component initialisation. */
	bindUrl() {
		if (!this.#url) return;
		$effect(() => {
			const f = this.from,
				t = this.to,
				r = this.refreshMs,
				l = this.live;
			untrack(() => {
				const u = new URL(location.href);
				u.searchParams.set('from', f);
				u.searchParams.set('to', t);
				u.searchParams.set('refresh', String(r));
				if (l) u.searchParams.set('live', '1');
				else u.searchParams.delete('live');
				if (u.href === location.href) return;
				try {
					replaceState(u, page.state);
				} catch {
					/* the router is not ready yet: the next change writes it */
				}
			});
		});
	}
}

export function fmtSpan(ms: number): string {
	if (ms >= 86400_000 && ms % 86400_000 === 0) return `${ms / 86400_000}d`;
	if (ms >= 3600_000) return `${+(ms / 3600_000).toFixed(1)}h`;
	if (ms >= 60_000) return `${Math.round(ms / 60_000)}m`;
	return `${Math.round(ms / 1000)}s`;
}

/** A timestamp precise enough for the span it is part of. */
export function fmtStamp(t: number, span = 0): string {
	const d = new Date(t);
	if (span >= 2 * 86400_000) return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
	const sameDay = new Date().toDateString() === d.toDateString();
	return d.toLocaleString(undefined, sameDay ? { hour: '2-digit', minute: '2-digit', second: span < 3600_000 ? '2-digit' : undefined } : { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}
