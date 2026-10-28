// How much of a workspace's limits is still free ("room"), so the create and
// settings forms can cap their pickers before the server refuses. The server
// stays authoritative; these numbers only make the forms honest up front.
import { fmtBytes, fmtCpu } from '$lib/api/client';
import type { Capacity, QuotaLimits, QuotaUsage } from '$lib/api/types';

export type Room = {
	memory: number | null;
	cpu: number | null;
	apps: number | null;
	bots: number | null;
	games: number | null;
	web_apps: number | null;
	sites: number | null;
};

/** Nothing known or nothing bounded: every field unlimited. */
export const unlimited: Room = { memory: null, cpu: null, apps: null, bots: null, games: null, web_apps: null, sites: null };

/** Resolved limits of a capacity payload (older payloads carry only the memory and app fields). */
export function capLimits(cap: Capacity | null): QuotaLimits {
	if (cap?.limits) return cap.limits;
	return {
		apps: cap?.max_bots ? cap.max_bots : null,
		bots: null,
		games: null,
		web_apps: null,
		sites: null,
		memory_bytes: cap?.max_memory_bytes ? cap.max_memory_bytes : null,
		nano_cpus: null
	};
}

function capUsage(cap: Capacity | null): QuotaUsage {
	return cap?.usage ?? { apps: cap?.bots ?? 0, bots: 0, games: 0, web_apps: 0, sites: 0, memory_bytes: cap?.memory_bytes ?? 0, nano_cpus: 0 };
}

/**
 * The room left under each limit: null when the limit is null (unlimited),
 * the actor is exempt, or nothing is loaded yet; otherwise
 * max(0, limit − usage + used). `used` is what the thing being edited already
 * holds (its own memory and CPU count toward usage, but it may keep them).
 */
export function room(cap: Capacity | null, used: { memory?: number; cpu?: number } = {}): Room {
	if (!cap || cap.exempt) return unlimited;
	const l = capLimits(cap);
	const u = capUsage(cap);
	const left = (limit: number | null, usage: number, keep = 0) => (limit == null ? null : Math.max(0, limit - usage + keep));
	return {
		memory: left(l.memory_bytes, u.memory_bytes, used.memory ?? 0),
		cpu: left(l.nano_cpus, u.nano_cpus, used.cpu ?? 0),
		apps: left(l.apps, u.apps),
		bots: left(l.bots, u.bots),
		games: left(l.games, u.games),
		web_apps: left(l.web_apps, u.web_apps),
		sites: left(l.sites, u.sites)
	};
}

/** The smaller of a node's maximum and the workspace room (room null = only the node bounds). */
export function clampMax(nodeMax: number, roomMax: number | null): number {
	return roomMax == null ? nodeMax : Math.min(nodeMax, roomMax);
}

/** "X of Y left" for a bounded limit; "" when unlimited. */
export function roomText(kind: 'memory' | 'cpu' | 'count', left: number | null, limit: number | null): string {
	if (left == null || limit == null) return '';
	const f = kind === 'memory' ? fmtBytes : kind === 'cpu' ? fmtCpu : String;
	return `${f(left)} of ${f(limit)} left`;
}
