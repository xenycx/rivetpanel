// Usage analytics (GET /bots/:id/usage, GET /admin/analytics). Values the
// panel has no data for are null, and the charts leave a gap there.

export type UsagePoint = {
	t: number;
	cpu: number | null; // average cores in use
	cpu_max: number | null;
	mem: number | null; // average bytes
	mem_max: number | null;
	mem_limit: number;
	net_rx: number | null; // bytes during the bucket
	net_tx: number | null;
	disk: number | null;
	uptime: number | null; // percent of the time it was wanted running
	crashes: number;
	starts: number;
};

export type UsageEventPoint = {
	t: number;
	deploys_ok: number;
	deploys_failed: number;
	deploy_avg_ms: number | null;
	backups_ok: number;
	backups_failed: number;
	backup_bytes: number;
};

export type UsageTotals = {
	cpu_avg: number | null;
	cpu_max: number | null;
	mem_avg: number | null;
	mem_max: number | null;
	net_rx: number;
	net_tx: number;
	disk: number | null;
	uptime: number | null;
	crashes: number;
	starts: number;
	deploys_ok: number;
	deploys_failed: number;
	deploy_avg_ms: number | null;
	backups_ok: number;
	backups_failed: number;
	backup_bytes: number;
};

export type BotUsage = {
	range: string;
	resolution_s: number;
	from_ms: number;
	to_ms: number;
	points: UsagePoint[];
	event_resolution_s: number;
	events: UsageEventPoint[];
	totals: UsageTotals;
};

export type OverviewPoint = {
	t: number;
	cpu: number;
	mem: number;
	net_rx: number;
	net_tx: number;
	uptime: number | null;
	crashes: number;
	starts: number;
	deploys_ok: number;
	deploys_failed: number;
	backups_ok: number;
	backups_failed: number;
	backup_bytes: number;
	new_users: number;
	tickets?: number | null;
};

export type OverviewConsumer = { bot_id: string; name: string; kind: string; owner?: string; cpu: number; mem: number; net_bytes: number };

export type OverviewNode = {
	id: string;
	name: string;
	location: string;
	samples: number;
	cpu_avg: number | null;
	cpu_max: number | null;
	mem_avg: number | null;
	mem_total: number;
	disk_used: number;
	disk_total: number;
	net_rx_bps: number | null;
	net_tx_bps: number | null;
	running_max: number;
	cpu_series: (number | null)[];
};

export type OverviewLocation = { name: string; nodes: number; cpu_avg: number | null; mem_avg: number; mem_total: number; disk_used: number; disk_total: number };

export type Overview = {
	range: string;
	resolution_s: number;
	from_ms: number;
	to_ms: number;
	scope: 'all' | 'delegated';
	users_total: number;
	users_new: number;
	bots: Record<string, { total: number; running: number }>;
	points: OverviewPoint[];
	totals: UsageTotals;
	top_cpu: OverviewConsumer[];
	top_mem: OverviewConsumer[];
	top_net: OverviewConsumer[];
	nodes?: OverviewNode[];
	locations?: OverviewLocation[];
	tickets?: { opened: number; closed: number; open_now: number; median_response_ms: number | null; answered: number };
};

export const botRanges = [
	{ id: '1h', label: '1 hour' },
	{ id: '24h', label: '24 hours' },
	{ id: '7d', label: '7 days' },
	{ id: '30d', label: '30 days' },
	{ id: '90d', label: '90 days' }
] as const;

export const overviewRanges = [
	{ id: '24h', label: '24 hours' },
	{ id: '7d', label: '7 days' },
	{ id: '30d', label: '30 days' },
	{ id: '90d', label: '90 days' }
] as const;

export const fmtCores = (n: number) => (n >= 10 ? n.toFixed(1) : n >= 1 ? n.toFixed(2) : n.toFixed(3).replace(/0+$/, '').replace(/\.$/, '') || '0') + ' cores';
export const fmtPct = (n: number) => `${n >= 99.95 ? 100 : n.toFixed(n >= 10 ? 1 : 2)}%`;
export const fmtCount = (n: number) => (Number.isInteger(n) ? n.toLocaleString() : n.toFixed(1));

export function fmtDuration(ms: number): string {
	const s = Math.round(ms / 1000);
	if (s < 60) return `${s} s`;
	if (s < 3600) return `${Math.floor(s / 60)} min ${s % 60 ? `${s % 60} s` : ''}`.trim();
	const h = Math.floor(s / 3600);
	if (h < 48) return `${h} h ${Math.round((s % 3600) / 60)} min`;
	return `${Math.round(h / 24)} days`;
}

export function fmtResolution(s: number): string {
	return s >= 86400 ? 'day' : s >= 3600 ? 'hour' : `${Math.round(s / 60)} minutes`;
}
