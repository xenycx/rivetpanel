// Types and formatting for the administration pages that watch and configure
// the host: Host (resources, bots, storage, logs) and Environment.

export type HistoryPoint = {
	sampled_at_ms: number;
	cpu_percent: number;
	cpu_max: number;
	logical_cpus: number;
	memory_used_bytes: number;
	memory_max: number;
	memory_total_bytes: number;
	disk_used_bytes: number;
	disk_total_bytes: number;
	running_bots: number;
	load1: number;
	swap_used_bytes: number;
	swap_total_bytes: number;
	net_rx_bps: number;
	net_tx_bps: number;
	disk_read_bps: number;
	disk_write_bps: number;
};
export type History = { node_id: string; range: string; since_ms: number; bucket_ms: number; points: HistoryPoint[] };
export type Range = '1h' | '6h' | '24h' | '7d' | '30d';
export const ranges: { id: Range; label: string }[] = [
	{ id: '1h', label: '1 hour' },
	{ id: '6h', label: '6 hours' },
	{ id: '24h', label: '24 hours' },
	{ id: '7d', label: '7 days' },
	{ id: '30d', label: '30 days' }
];

export type HostSnapshot = {
	generated_at_ms: number;
	host: { hostname: string; os: string; kernel: string; arch: string; cpu_model: string; cores: number; uptime_sec: number; booted_at_ms: number; load1: number; load5: number; load15: number };
	memory: { total: number; used: number; available: number; free: number; buffers: number; cached: number; swap_total: number; swap_used: number; node_budget: number };
	panel: {
		version: string;
		go_version: string;
		pid: number;
		uptime_ms: number;
		started_ms: number;
		rss_bytes: number;
		heap_bytes: number;
		sys_bytes: number;
		goroutines: number;
		threads: number;
		open_files: number;
		file_limit: number;
		gc_count: number;
		gc_pause_ms: number;
		http: { requests: number; errors: number; in_flight: number; avg_ms: number };
		db_bytes: number;
	};
	storage: { key: string; label: string; path: string; bytes: number; partial: boolean; counted_at_ms: number; fs_total: number; fs_free: number; inodes_total: number; inodes_free: number; device: number; note?: string }[];
	docker: {
		enabled: boolean;
		ready: boolean;
		error?: string;
		version?: string;
		cgroup_v2: boolean;
		rootless: boolean;
		swap_limit: boolean;
		workers: number;
		queued: number;
		tracked: number;
		user?: string;
		containers_running: number;
		containers_stopped: number;
		builders: number;
		diagnostics: number;
		problem?: string;
	};
	logs: LogStats;
};

export type BotUsage = {
	id: string;
	name: string;
	kind: 'bot' | 'game' | 'web';
	owner: string;
	runtime: string;
	state: string;
	running: boolean;
	cpu_cores: number;
	cpu_limit_cores: number;
	mem_used_bytes: number;
	mem_limit_bytes: number;
	pids: number;
	net_rx_bytes: number;
	net_tx_bytes: number;
	disk_bytes: number;
	measured: boolean;
	started_at_ms?: number;
};
export type BotsReport = { generated_at_ms: number; bots: BotUsage[]; total: number; running: number; cpu_cores: number; mem_used_bytes: number; mem_reserved_bytes: number; truncated: boolean };

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';
export type LogEntry = { seq: number; time_ms: number; level: LogLevel; msg: string; attrs?: { key: string; value: string }[] };
export type LogStats = { capacity: number; held: number; oldest_seq: number; latest_seq: number; oldest_ms: number; total: number; errors: number; warnings: number; last_error_ms: number };

export type EnvVar = {
	name: string;
	group: string;
	description: string;
	kind: 'text' | 'int' | 'bytes' | 'duration' | 'bool' | 'enum' | 'path' | 'url' | 'secret';
	default: string;
	options?: string[];
	example?: string;
	boot?: boolean;
	managed?: string;
	danger?: string;
	editable: boolean;
	secret: boolean;
	source: 'default' | 'environment' | 'panel';
	value: string;
	set: boolean;
	env_value: string;
	env_set: boolean;
	override: boolean;
	pending: boolean;
	running: string;
};
export type EnvView = { vars: EnvVar[]; groups: string[]; pending: number; can_restart: boolean; rejected?: string; unreadable?: string[]; started_at_ms: number };

export const fmtBps = (n: number): string => {
	if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GiB/s';
	if (n >= 1024 ** 2) return (n / 1024 ** 2).toFixed(1) + ' MiB/s';
	if (n >= 1024) return Math.round(n / 1024) + ' KiB/s';
	return Math.round(n) + ' B/s';
};

/** "3 d 4 h", "5 h 12 min", "7 min". */
export function fmtUptime(sec: number): string {
	const d = Math.floor(sec / 86400);
	const h = Math.floor((sec % 86400) / 3600);
	const m = Math.floor((sec % 3600) / 60);
	if (d) return `${d} d ${h} h`;
	if (h) return `${h} h ${m} min`;
	if (m) return `${m} min`;
	return `${Math.max(0, Math.round(sec))} s`;
}

export const pct = (a: number, b: number): number => (b > 0 ? Math.round((a / b) * 100) : 0);
export const tone = (p: number): 'run' | 'warn' | 'fail' => (p >= 90 ? 'fail' : p >= 75 ? 'warn' : 'run');
