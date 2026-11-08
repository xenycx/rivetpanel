// How much of a workspace's limits is still free ("room"), so the create and
// settings forms can cap their pickers before the server refuses. The server
// stays authoritative; these numbers only make the forms honest up front.
import { fmtBytes, fmtCpu } from '$lib/api/client';
import type { Capacity, QuotaLimits, QuotaUsage } from '$lib/api/types';
import { fmtAllowance } from '$lib/limits';

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
	return allowanceRoom(cap, used);
}

/**
 * The room left under the workspace allowance whoever looks, the exempt
 * administrator included: for showing the room, never for capping a picker.
 */
export function allowanceRoom(cap: Capacity | null, used: { memory?: number; cpu?: number } = {}): Room {
	if (!cap) return unlimited;
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

type Kind = 'memory' | 'cpu';
const what: Record<Kind, string> = { memory: 'memory', cpu: 'CPU' };

/**
 * Names the bound that limits a memory or CPU picker:
 * "Limited by the workspace memory allowance: 1.8 of 2 GiB used." or
 * "Limited by the node maximum: up to 4 GiB on Main.". With `extra`, memory
 * the new app brings besides its own (add-ons) counts as used. An exempt
 * administrator sees the allowance with "(you are exempt as an
 * administrator)" and is bounded by the node maximum only.
 */
export function boundText(kind: Kind, cap: Capacity | null, nodeMax: number, nodeName: string, opts: { keep?: number; extra?: number } = {}): string {
	const f = kind === 'memory' ? fmtBytes : fmtCpu;
	const l = capLimits(cap);
	const u = capUsage(cap);
	const limit = kind === 'memory' ? l.memory_bytes : l.nano_cpus;
	const usedNow = (kind === 'memory' ? u.memory_bytes : u.nano_cpus) - (opts.keep ?? 0) + (opts.extra ?? 0);
	const node = nodeMax ? `up to ${f(nodeMax)} on ${nodeName}` : '';
	if (limit == null) return node ? `Limited by the node maximum: ${node}.` : '';
	// "1.5 of 2 cores used", "1.8 GiB of 2.0 GiB used".
	const usedText = kind === 'cpu' ? String(+(Math.max(0, usedNow) / 1e9).toFixed(2)) : f(Math.max(0, usedNow));
	const allowance = limit === 0 ? 'none allowed' : `${usedText} of ${f(limit)} used`;
	if (cap?.exempt) return `${node ? `Limited by the node maximum: ${node}. ` : ''}Workspace ${what[kind]} allowance: ${allowance} (you are exempt as an administrator).`;
	const left = Math.max(0, limit - usedNow);
	if (!nodeMax || left < nodeMax) return `Limited by the workspace ${what[kind]} allowance: ${allowance}.`;
	return `Limited by the node maximum: ${node}. Workspace ${what[kind]} allowance: ${allowance}.`;
}

/** "Bots and containers" style refusal when a count allows nothing more; "" when there is room or the actor is exempt. */
export function countBlock(cap: Capacity | null, keys: (keyof QuotaLimits)[], labels: Partial<Record<keyof QuotaLimits, string>>): string {
	if (!cap || cap.exempt) return '';
	const r = room(cap);
	const l = capLimits(cap);
	const ws = cap.workspace ? `The workspace ${cap.workspace.name || 'Personal'}` : 'This workspace';
	for (const k of keys) {
		if (r[k as keyof Room] !== 0) continue;
		const lim = l[k];
		const name = labels[k] ?? String(k);
		if (lim === 0) return `${ws} doesn't allow ${name}. Ask an administrator to raise its allowance.`;
		return `${ws} is at its allowance: ${fmtAllowance('count', lim)} of ${fmtAllowance('count', lim)} ${name} used. Delete one, or ask an administrator to raise the allowance.`;
	}
	return '';
}

/**
 * A warning when an app needing `need` bytes would not fit in the node
 * budget left on its node: creating still works, but starting is refused
 * until other apps stop. "" when it fits or no budget is set.
 */
export function budgetWarning(cap: Capacity | null, need: number): string {
	const b = cap?.node_budget;
	if (!b || need <= 0 || need <= b.free_bytes) return '';
	return `This app needs ${fmtBytes(need)}, but only ${fmtBytes(b.free_bytes)} of the ${fmtBytes(b.budget_bytes)} node budget is free. It will be created, but the panel refuses to start it until other apps on the node stop or an administrator raises the node budget.`;
}
