// One vocabulary for limits, shared by every surface that shows one.
//
// App size: memory, CPU and processes of one app (Docker enforces it).
// Workspace allowance: what one workspace may hold in total; the panel
//   refuses creating, resizing or moving past it. The panel-wide value is
//   the Default allowance.
// Node budget: the memory sizes of apps wanted running on a node; the
//   panel refuses a start past it.
// Node maximum: the largest app size a node accepts.
import { fmtBytes, fmtCpu } from '$lib/api/client';
import type { QuotaLimits, WorkspaceQuota } from '$lib/api/types';

/** Where an allowance value comes from. */
export type LimitSource = 'builtin' | 'env' | 'panel' | 'workspace';

export const sourceText: Record<LimitSource, string> = {
	builtin: 'Built-in',
	env: 'Environment',
	panel: 'Panel default',
	workspace: 'This workspace'
};

/** The one wording for an unlimited value. */
export const NO_LIMIT = 'No limit';

/** Where "How limits work" is explained. */
export const LIMITS_HELP = '/docs#workspace-limits';

type CountKey = 'apps' | 'bots' | 'games' | 'web_apps' | 'sites';

/** Allowance counts in display order: Apps in total first. */
export const countFields: { key: CountKey; quota: keyof WorkspaceQuota & `max_${string}`; label: string; one: string; many: string }[] = [
	{ key: 'apps', quota: 'max_apps', label: 'Apps in total', one: 'app in total', many: 'apps in total' },
	{ key: 'bots', quota: 'max_bots', label: 'Bots and containers', one: 'bot or container', many: 'bots and containers' },
	{ key: 'games', quota: 'max_games', label: 'Game servers', one: 'game server', many: 'game servers' },
	{ key: 'web_apps', quota: 'max_web_apps', label: 'Web apps', one: 'web app', many: 'web apps' },
	{ key: 'sites', quota: 'max_sites', label: 'Sites', one: 'site', many: 'sites' }
];

/** What each count includes, for help text. */
export const countHelp: Record<CountKey, string> = {
	apps: 'Bots and containers, game servers and web apps together.',
	bots: 'Bots, container apps and each Compose service.',
	games: 'Game servers.',
	web_apps: 'Apps behind sites from GitHub.',
	sites: 'Sites, bot public pages included.'
};

/** A count, memory or CPU allowance value as people read it: "No limit", "None allowed", "2 GiB", "1.5 cores". */
export function fmtAllowance(kind: 'count' | 'memory' | 'cpu', v: number | null | undefined): string {
	if (v == null) return NO_LIMIT;
	if (v === 0) return 'None allowed';
	return kind === 'memory' ? fmtBytes(v).replace('.0 ', ' ') : kind === 'cpu' ? fmtCpu(v) : String(v);
}

/** "Inherits 10 · Built-in": what an empty allowance field falls back to. */
export function inheritsText(kind: 'count' | 'memory' | 'cpu', v: number | null | undefined, source: LimitSource | undefined): string {
	return `Inherits ${fmtAllowance(kind, v).replace(/^N/, 'n')}${source ? ` · ${sourceText[source]}` : ''}`;
}

export const limitKind = (key: keyof QuotaLimits): 'count' | 'memory' | 'cpu' => (key === 'memory_bytes' ? 'memory' : key === 'nano_cpus' ? 'cpu' : 'count');
