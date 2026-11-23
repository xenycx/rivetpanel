// Monitoring API (1.20.0): columnar time series for a node and its apps,
// events, CPU spikes, "what happened at t", and live process lists.
// Every time is Unix milliseconds; value arrays run parallel to `times`
// and hold null where nothing was measured.
import { api } from './client';

export type Col = (number | null)[];

export type NodeSeries = {
	node_id: string;
	start_ms: number;
	end_ms: number;
	step_ms: number;
	times: number[];
	cpu: Col; // percent of all cores
	cpu_max: Col;
	mem: Col; // bytes
	mem_max: Col;
	mem_total: number;
	load1: Col;
	net_rx: Col; // bytes per second
	net_tx: Col;
	disk_read: Col;
	disk_write: Col;
	disk_used: Col;
	disk_total: number;
	swap: Col;
	cpus: number;
};

export type BotSeriesEntry = { bot_id: string; name: string; values: Col };
/** CPU in cores or memory in bytes per app, plus "other" (apps outside the top N) and host usage no app accounts for. */
export type NodeBotsSeries = { times: number[]; bots: BotSeriesEntry[]; other: Col; unattributed: Col };

export type LiveSample = { t: number; cpu: number; mem: number; load1: number; net_rx: number; net_tx: number; disk_read: number; disk_write: number; swap: number };

export type EventKind = 'deploy' | 'start' | 'stop' | 'restart' | 'crash' | 'backup' | 'spike' | 'oom';
export type MetricEvent = { t_ms: number; kind: EventKind | string; bot_id?: string; bot_name?: string; text: string };

export type ProcessEntry = { pid: number; comm: string; cmd: string; cpu_pct: number; rss_bytes: number; bot_id?: string; bot_name?: string; container_id?: string };
export type ProcessList = { sampled_at_ms: number; source: string; items: ProcessEntry[] };

export type SpikeBot = { bot_id: string; name: string; cpu_cores: number; share_pct: number };
export type Spike = {
	id: number;
	started_at_ms: number;
	ended_at_ms: number | null;
	peak_cpu_pct: number;
	avg_cpu_pct: number;
	top_bots: SpikeBot[];
	top_processes: ProcessEntry[];
};
/** threshold_pct is optional: older servers do not report it and 80 % is assumed. */
export type SpikeList = { spikes: Spike[]; threshold_pct?: number };

export type Moment = {
	t_ms: number;
	/** null when nothing was sampled around t. */
	host: { cpu: number | null; mem: number | null; load1: number | null } | null;
	bots: { bot_id: string; name: string; cpu_cores: number; mem_bytes: number; share_pct: number }[];
	processes: ProcessList | null;
	events: MetricEvent[];
	spike: Spike | null;
};

export type BotSeries = {
	times: number[];
	cpu: Col; // cores
	mem: Col; // bytes
	net_rx: Col; // bytes per second
	net_tx: Col;
	blk_read: Col;
	blk_write: Col;
	pids: Col;
	mem_limit: number;
	cpu_limit?: number;
	step_ms?: number;
	source?: string;
};

export type Window = { start: number; end: number };
export type SeriesQuery = Window & { step?: number; points?: number; since?: number };

function qs(q: Record<string, number | string | undefined>): string {
	const p = new URLSearchParams();
	for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '') p.set(k, String(typeof v === 'number' ? Math.round(v) : v));
	return p.toString();
}
const seriesQs = (q: SeriesQuery) => qs({ start: q.start, end: q.end, step: q.step, points: q.step ? undefined : q.points, since: q.since });
const enc = encodeURIComponent;

// Go may send null for an empty list: lists are normalised to [] here so
// views never have to guard.
const arr = <T>(v: T[] | null | undefined): T[] => v ?? [];
const procList = (p: ProcessList | null): ProcessList | null => (p ? { ...p, items: arr(p.items) } : null);
const spike = (s: Spike): Spike => ({ ...s, top_bots: arr(s.top_bots), top_processes: arr(s.top_processes) });

export const metrics = {
	nodeSeries: (node: string, q: SeriesQuery, signal?: AbortSignal) => api<NodeSeries>('GET', `/nodes/${enc(node)}/series?${seriesQs(q)}`, undefined, signal).then((r) => ({ ...r, times: arr(r.times) })),
	nodeBotsSeries: (node: string, q: SeriesQuery & { top?: number; metric: 'cpu' | 'mem' }, signal?: AbortSignal) =>
		api<NodeBotsSeries>('GET', `/nodes/${enc(node)}/series/bots?${seriesQs(q)}&${qs({ top: q.top ?? 8, metric: q.metric })}`, undefined, signal).then((r) => ({
			...r,
			times: arr(r.times),
			bots: arr(r.bots),
			other: arr(r.other),
			unattributed: arr(r.unattributed)
		})),
	/** Path of the live SSE stream (event "sample", data = LiveSample). */
	streamPath: (node: string) => `/api/v1/nodes/${enc(node)}/series/stream`,
	events: (node: string, w: Window, signal?: AbortSignal) =>
		api<{ events: MetricEvent[] }>('GET', `/nodes/${enc(node)}/events?${qs({ start: w.start, end: w.end })}`, undefined, signal).then((r) => ({ events: arr(r.events) })),
	spikes: (node: string, w: Window, signal?: AbortSignal) =>
		api<SpikeList>('GET', `/nodes/${enc(node)}/spikes?${qs({ start: w.start, end: w.end })}`, undefined, signal).then((r) => ({ ...r, spikes: arr(r.spikes).map(spike) })),
	at: (node: string, t: number, window?: number, signal?: AbortSignal) =>
		api<Moment>('GET', `/nodes/${enc(node)}/at?${qs({ t, window })}`, undefined, signal).then((m) => ({
			...m,
			bots: arr(m.bots),
			events: arr(m.events),
			processes: procList(m.processes),
			spike: m.spike ? spike(m.spike) : null
		})),
	processes: (node: string, limit = 25, signal?: AbortSignal) => api<ProcessList>('GET', `/nodes/${enc(node)}/processes?${qs({ limit })}`, undefined, signal).then((p) => procList(p)!),
	botSeries: (bot: string, q: SeriesQuery, signal?: AbortSignal) => api<BotSeries>('GET', `/bots/${enc(bot)}/series?${seriesQs(q)}`, undefined, signal).then((r) => ({ ...r, times: arr(r.times) })),
	botProcesses: (bot: string, signal?: AbortSignal) => api<ProcessList>('GET', `/bots/${enc(bot)}/processes`, undefined, signal).then((p) => procList(p)!)
};

/** Event kind → chart colour token. */
export function eventTone(kind: string): 'fail' | 'warn' | 'run' | 'action' | 'data' | 'muted' {
	switch (kind) {
		case 'crash':
		case 'oom':
			return 'fail';
		case 'spike':
			return 'warn';
		case 'start':
		case 'restart':
			return 'run';
		case 'deploy':
			return 'action';
		case 'backup':
			return 'data';
		default:
			return 'muted';
	}
}

export const eventLabel: Record<string, string> = {
	deploy: 'Deploy',
	start: 'Start',
	stop: 'Stop',
	restart: 'Restart',
	crash: 'Crash',
	backup: 'Backup',
	spike: 'CPU spike',
	oom: 'Out of memory'
};
