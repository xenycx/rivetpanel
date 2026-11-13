import { Perm, type Bot, type Site } from '$lib/api/types';
import type { GameDetail } from '$lib/api/games';
import type { Features } from '$lib/session.svelte';
import type { IconName } from '$lib/components/ui/Icon.svelte';

// One registry for the sections of every app page. Bots, game servers, site
// apps and containers are the same record on the server, so a concept has one
// id, one label and one icon wherever it appears; each tab declares the kinds
// and capabilities it needs instead of pages keeping their own lists.

/** What kind of app a page shows. 'container' runs an image the user chose. */
export type AppKind = 'bot' | 'game' | 'web' | 'container';

export type TabGroupId = 'overview' | 'code' | 'configuration' | 'services' | 'data' | 'observability' | 'settings';

/** What a tab may look at to decide whether it applies. */
export type TabContext = {
	kind: AppKind;
	bot: Bot;
	features: Features;
	/** What the server type offers (game servers only; null until loaded). */
	game?: GameDetail | null;
	/** The site a web app belongs to. */
	site?: Site | null;
	/** Whether the viewer may open a tab that needs `perm`: 0 anyone with access, -1 owner or administrator, otherwise a Perm bit. The server enforces the same rules. */
	allow: (perm: number) => boolean;
};

export type TabDef = {
	/** The ?tab= value. Never rename one; add an alias instead. */
	id: string;
	label: string | ((c: TabContext) => string);
	icon: IconName;
	group: TabGroupId;
	kinds: readonly AppKind[];
	/** 0 (default) anyone with access, -1 owner or administrator, otherwise a Perm bit. */
	perm?: number | ((c: TabContext) => number);
	/** A panel feature the tab needs; left out when the panel does not run it. */
	feature?: keyof Features;
	/** Any further capability the app must have. */
	when?: (c: TabContext) => boolean;
	/** Changes need a stopped app; the page says so above the tab. */
	needsStop?: boolean;
	/** Changes save while the app runs and apply when it restarts; the page says so above the tab. */
	appliesOnRestart?: boolean;
	/** Extra search words for the command palette. */
	words?: string;
};

/** A tab resolved for one app and viewer: what the tab bars render. */
// disabled: shown greyed out and not clickable (unless it is the open tab); hint says why.
export type Tab = { id: string; label: string; icon: IconName; group: TabGroupId; needsStop?: boolean; appliesOnRestart?: boolean; disabled?: boolean; hint?: string };

const apps: readonly AppKind[] = ['bot', 'game', 'container'];
const has = (c: TabContext, feature: string) => !!c.game?.spec.features?.includes(feature);

/** Every section, in the order pages show them (by group, then as listed). */
export const tabDefs: readonly TabDef[] = [
	{ id: 'manage', label: 'Overview', icon: 'overview', group: 'overview', kinds: ['bot', 'game', 'web', 'container'], words: 'console status summary start stop restart logs output terminal activity source details' },
	{ id: 'players', label: 'Players', icon: 'users', group: 'overview', kinds: ['game'], when: (c) => has(c, 'minecraft-players'), words: 'online whitelist ops ban kick' },
	{ id: 'page', label: 'Public page', icon: 'globe', group: 'overview', kinds: ['bot'], perm: Perm.console, words: 'site studio widgets sdk' },
	{ id: 'terminal', label: 'Terminal', icon: 'terminal', group: 'overview', kinds: ['container'], perm: Perm.admin, feature: 'terminal', words: 'shell exec tty bash sh command line herdr' },

	{ id: 'files', label: 'Files', icon: 'folder', group: 'code', kinds: ['bot', 'game', 'web', 'container'], perm: Perm.files, feature: 'files', words: 'editor upload code data' },
	{ id: 'deploy', label: 'Deployments', icon: 'rocket', group: 'code', kinds: ['bot', 'web'], perm: Perm.files, words: 'github push webhook release build rollback publish' },
	{ id: 'packages', label: 'Packages', icon: 'package', group: 'code', kinds: ['bot'], perm: Perm.files, feature: 'files', words: 'npm pip dependencies install' },
	{ id: 'mods', label: (c) => (c.game?.spec.addons?.kind === 'plugin' ? 'Plugins' : 'Mods'), icon: 'package', group: 'code', kinds: ['game'], when: (c) => !!c.game?.spec.addons, words: 'modrinth plugins mods install' },

	// A game server's configuration holds its server-type variables, which
	// "Change server settings" (the variables permission) may edit. A bot's
	// startup needs "Configuration and startup" (or full access).
	{ id: 'startup', label: 'Configuration', icon: 'sliders', group: 'configuration', kinds: ['bot', 'game', 'web', 'container'], perm: (c) => (c.kind === 'game' ? Perm.env : c.kind === 'bot' ? Perm.configure : Perm.admin), appliesOnRestart: true, words: 'startup command entrypoint arguments restart policy build script version jvm port' },
	{ id: 'properties', label: 'Properties', icon: 'list', group: 'configuration', kinds: ['game'], perm: Perm.files, when: (c) => has(c, 'minecraft-properties'), words: 'server.properties motd difficulty gamemode' },
	{ id: 'env', label: 'Variables', icon: 'key', group: 'configuration', kinds: ['bot', 'web', 'container'], perm: Perm.env, appliesOnRestart: true, words: 'env environment secrets token config' },
	{ id: 'network', label: 'Network', icon: 'network', group: 'configuration', kinds: apps, perm: Perm.admin, appliesOnRestart: true, words: 'ports publish outbound allocations' },

	// Companion database containers. A static site has nothing to connect them to.
	{ id: 'addons', label: 'Services', icon: 'layers', group: 'services', kinds: ['bot', 'game', 'web', 'container'], when: (c) => c.kind !== 'web' || c.site?.mode === 'app', needsStop: true, words: 'postgres postgresql redis mongodb mariadb mysql database cache' },

	{ id: 'backups', label: 'Backups', icon: 'archive', group: 'data', kinds: [...apps, 'web'], perm: Perm.files, feature: 'backups', words: 'restore snapshot download automatic' },
	{ id: 'schedules', label: 'Schedules', icon: 'clock', group: 'data', kinds: apps, feature: 'schedules', words: 'cron restart timer' },

	// Game servers have no SDK heartbeat or probe: their health is the process
	// state and the game query in the header.
	{ id: 'alerts', label: 'Health', icon: 'activity', group: 'observability', kinds: ['bot'], perm: Perm.console, feature: 'health', words: 'probe heartbeat sdk uptime tcp http' },
	{ id: 'usage', label: 'Analytics', icon: 'chart', group: 'observability', kinds: apps, perm: Perm.console, feature: 'usage', words: 'usage cpu memory network uptime crashes deployments backups history trends csv' },

	{ id: 'settings', label: 'Settings', icon: 'gear', group: 'settings', kinds: apps, perm: Perm.admin, appliesOnRestart: true, words: 'rename memory cpu limits transfer delete notifications alerts discord email crash' },
	{ id: 'users', label: 'Access', icon: 'shield', group: 'settings', kinds: apps, perm: -1, words: 'share invite permissions users' }
];

const groupOrder: TabGroupId[] = ['overview', 'code', 'configuration', 'services', 'data', 'observability', 'settings'];
/** How each group is named in the tab bar. */
export const tabGroups: Record<TabGroupId, { label: (k: AppKind) => string; icon: (k: AppKind) => IconName }> = {
	overview: { label: () => 'Overview', icon: () => 'overview' },
	code: { label: (k) => (k === 'game' ? 'Content' : 'Code'), icon: (k) => (k === 'game' ? 'folder' : 'code') },
	configuration: { label: () => 'Configuration', icon: () => 'sliders' },
	services: { label: () => 'Services', icon: () => 'layers' },
	data: { label: () => 'Data', icon: () => 'archive' },
	observability: { label: () => 'Observability', icon: () => 'activity' },
	settings: { label: () => 'Settings and access', icon: () => 'gear' }
};

/** Older and alternative ?tab= values, and the tab each one opens. */
export const tabAliases: Record<string, string> = {
	// Retired names.
	console: 'manage',
	ai: 'manage',
	overview: 'manage',
	logs: 'manage',
	analytics: 'page',
	// The site page's former ids and the labels used today.
	deployments: 'deploy',
	configuration: 'startup',
	build: 'startup',
	variables: 'env',
	environment: 'env',
	services: 'addons',
	databases: 'addons',
	health: 'alerts',
	access: 'users',
	plugins: 'mods'
};
// A game server's Health tab became Settings → Notifications.
const kindAliases: Partial<Record<AppKind, Record<string, string>>> = { game: { alerts: 'settings' } };

const resolve = (v: TabDef['label'] | TabDef['perm'], c: TabContext) => (typeof v === 'function' ? v(c) : v);

/** The tabs one app shows to the viewer, in display order. */
export function tabsFor(c: TabContext): Tab[] {
	return tabDefs
		.filter((d) => d.kinds.includes(c.kind) && (!d.feature || c.features[d.feature]) && (!d.when || d.when(c)) && c.allow((resolve(d.perm, c) as number | undefined) ?? 0))
		.map((d) => ({ id: d.id, label: resolve(d.label, c) as string, icon: d.icon, group: d.group, needsStop: d.needsStop, appliesOnRestart: d.appliesOnRestart }));
}

/**
 * The tab a ?tab= value opens: aliases are followed, and a tab this app or
 * viewer does not have falls back to the first one. `canonical` is the id the
 * URL should say when an alias was used.
 */
export function resolveTab(asked: string | null, kind: AppKind, tabs: Pick<Tab, 'id'>[]): { id: string; canonical: string | null } {
	const a = asked ?? '';
	const want = kindAliases[kind]?.[a] ?? tabAliases[a] ?? a;
	return { id: tabs.some((t) => t.id === want) ? want : (tabs[0]?.id ?? 'manage'), canonical: asked && want !== asked ? want : null };
}

/** A link in a tab bar. */
export type TabLink = { id: string; label: string; icon: IconName; href: string; pin?: boolean; iconOnly?: boolean; disabled?: boolean; hint?: string };

/** Up to this many tabs sit in one row; more are grouped. */
export const FLAT_TABS = 7;

/**
 * The tab bars of a page. Up to FLAT_TABS tabs form one row. More are grouped:
 * the first row names the groups (a group with one tab shows that tab), Settings
 * and Access sit behind a gear at its end, and the open group's tabs form a
 * second row.
 */
export function tabNav(tabs: Tab[], current: string, kind: AppKind, href: (id: string) => string): { primary: TabLink[]; active: string; secondary: TabLink[]; groupLabel: string } {
	const link = (t: Tab): TabLink => ({ id: t.id, label: t.label, icon: t.icon, href: href(t.id), disabled: t.disabled, hint: t.hint });
	if (tabs.length <= FLAT_TABS) return { primary: tabs.map(link), active: current, secondary: [], groupLabel: '' };
	const primary: TabLink[] = [];
	let active = current;
	let secondary: TabLink[] = [];
	let groupLabel = '';
	for (const g of groupOrder) {
		const members = tabs.filter((t) => t.group === g);
		if (!members.length) continue;
		const pin = g === 'settings';
		const open = members.some((t) => t.id === current);
		if (members.length === 1) {
			primary.push({ ...link(members[0]), pin, iconOnly: pin });
			continue;
		}
		const id = `group-${g}`;
		const label = tabGroups[g].label(kind);
		primary.push({ id, label, icon: tabGroups[g].icon(kind), href: href(open ? current : (members.find((t) => !t.disabled) ?? members[0]).id), pin, iconOnly: pin });
		if (open) {
			active = id;
			secondary = members.map(link);
			groupLabel = label;
		}
	}
	return { primary, active, secondary, groupLabel };
}
