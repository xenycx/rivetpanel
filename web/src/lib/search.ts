// What the "Go to" palette can find. Static entries (pages, settings, admin
// sections, docs) live here with the words people use for them; bots, sites,
// people and chats are added from the API when the palette opens.

import { tabDefs, type AppKind } from '$lib/tabs';
import type { IconName } from '$lib/components/ui/Icon.svelte';
import type { AvatarKind } from '$lib/avatar';

export type Scope = 'all' | 'actions' | 'pages' | 'bots' | 'sites' | 'workspaces' | 'admin' | 'people' | 'chats';
export const scopes: { id: Scope; label: string }[] = [
	{ id: 'all', label: 'All' },
	{ id: 'actions', label: 'Actions' },
	{ id: 'pages', label: 'Pages' },
	{ id: 'bots', label: 'Bots & servers' },
	{ id: 'sites', label: 'Sites' },
	{ id: 'workspaces', label: 'Workspaces' },
	{ id: 'admin', label: 'Admin' },
	{ id: 'people', label: 'People' },
	{ id: 'chats', label: 'AI chats' }
];

/** Display order of the groups, and the scope each belongs to. */
export const groups: { id: string; label: string; scope: Scope }[] = [
	{ id: 'action', label: 'Actions', scope: 'actions' },
	{ id: 'bot', label: 'Apps', scope: 'bots' },
	{ id: 'bot-tab', label: 'App sections', scope: 'bots' },
	{ id: 'site', label: 'Websites', scope: 'sites' },
	{ id: 'workspace', label: 'Workspaces', scope: 'workspaces' },
	{ id: 'page', label: 'Pages', scope: 'pages' },
	{ id: 'settings', label: 'Your settings', scope: 'pages' },
	{ id: 'admin', label: 'Administration', scope: 'admin' },
	{ id: 'env', label: 'Environment variables', scope: 'admin' },
	{ id: 'person', label: 'Users', scope: 'people' },
	{ id: 'chat', label: 'AI chats', scope: 'chats' },
	{ id: 'docs', label: 'Documentation', scope: 'pages' }
];

export type Entry = {
	key: string;
	group: string;
	label: string;
	hint: string;
	icon: IconName;
	/** A logo shown instead of the icon (workspaces). */
	logo?: { src?: string | null; kind: AvatarKind; seed: string };
	/** Extra words that find it (synonyms, what is on the page). */
	words?: string;
	href?: string;
	run?: () => void;
	/** Shown at the top of an empty search. */
	pinned?: boolean;
	boost?: number;
};

export const pages: Entry[] = [
	{ key: 'p-overview', group: 'page', label: 'Overview', hint: 'Capacity and apps at a glance', icon: 'home', href: '/dashboard', words: 'dashboard home fleet capacity' },
	{ key: 'p-apps', group: 'page', label: 'Apps', hint: 'Bots, game servers, containers and websites', icon: 'box', href: '/apps', words: 'apps list filter batch all' },
	{ key: 'p-bots', group: 'page', label: 'Bots', hint: 'Apps → Bots', icon: 'terminal', href: '/apps?kind=bot', words: 'discord bots list filter batch' },
	{ key: 'p-servers', group: 'page', label: 'Game servers', hint: 'Apps → Game servers', icon: 'gamepad', href: '/apps?kind=game', words: 'game minecraft paper fabric forge valheim rust zomboid steam' },
	{ key: 'p-containers', group: 'page', label: 'Containers', hint: 'Apps → Containers and stacks', icon: 'box', href: '/apps?kind=container', words: 'docker container image compose stack' },
	{ key: 'p-new', group: 'page', label: 'New', hint: 'Everything you can create', icon: 'plus', href: '/new', words: 'create start new run' },
	{ key: 'p-templates', group: 'page', label: 'Templates', hint: 'Bot templates, game server types and site templates', icon: 'layers', href: '/templates', words: 'starter discord.js discord.py jda example bot templates blueprints eggs server types site templates' },
	{ key: 'p-templates-games', group: 'page', label: 'Game server templates', hint: 'Templates · every server type you can create', icon: 'gamepad', href: '/templates?tab=games', words: 'blueprints eggs minecraft steam server types imported' },
	{ key: 'p-templates-sites', group: 'page', label: 'Site templates', hint: 'Templates · static site starters with previews', icon: 'globe', href: '/templates?tab=sites', words: 'landing docs portfolio coming soon community starter website' },
	{ key: 'p-sites', group: 'page', label: 'Websites', hint: 'Apps → Websites: static files, builds and web apps', icon: 'globe', href: '/apps?kind=web', words: 'sites static website pages domains app build' },
	{ key: 'p-activity', group: 'page', label: 'Activity', hint: 'Work in progress and recent changes', icon: 'activity', href: '/activity', words: 'audit log history operations changes builds deployments' },
	{ key: 'p-notifications', group: 'page', label: 'Notifications', hint: 'Alerts, deployments, replies and announcements', icon: 'bell', href: '/notifications', words: 'inbox bell alerts unread' },
	{ key: 'p-support', group: 'page', label: 'Support', hint: 'Your support tickets', icon: 'lifebuoy', href: '/support', words: 'help ticket question staff' },
	{ key: 'p-help', group: 'page', label: 'Help center', hint: 'Articles from the panel staff', icon: 'lifebuoy', href: '/help', words: 'knowledgebase kb articles faq' },
	{ key: 'p-status', group: 'page', label: 'Status page', hint: 'Public status, incidents and maintenance', icon: 'activity', href: '/status', words: 'uptime incidents outage maintenance' },
	{ key: 'p-docs', group: 'page', label: 'Documentation', hint: 'Guides and limits', icon: 'book', href: '/docs', words: 'help manual guide docs how to' },
	{ key: 'p-api', group: 'page', label: 'Automation API', hint: 'OpenAPI description', icon: 'code', href: '/api/v1/automation/openapi.yaml', words: 'openapi rest tokens swagger' },
	{ key: 'p-about', group: 'page', label: 'What RivetPanel does', hint: 'Overview of the product', icon: 'info', href: '/', words: 'about landing features' },

	{ key: 's-profile', group: 'settings', label: 'Profile', hint: 'Name and picture', icon: 'users', href: '/settings/profile', words: 'account avatar display name email' },
	{ key: 's-notifications', group: 'settings', label: 'Notification settings', hint: 'In-panel and email per category', icon: 'bell', href: '/settings/notifications', words: 'email alerts preferences mute' },
	{ key: 's-appearance', group: 'settings', label: 'Appearance', hint: 'Theme, accent colour and corners', icon: 'sliders', href: '/settings/appearance', words: 'dark light theme accent color colour radius corners' },
	{ key: 's-workspaces', group: 'settings', label: 'Workspaces', hint: 'Your workspaces and members', icon: 'building', href: '/settings/workspaces', words: 'team members roles invite' },
	{ key: 's-accounts', group: 'settings', label: 'Connected accounts', hint: 'GitHub and Discord links', icon: 'link', href: '/settings/connected-accounts', words: 'github discord oauth connect link repo' },
	{ key: 's-security', group: 'settings', label: 'Security', hint: 'Password, two-step sign-in and sessions', icon: 'shield', href: '/settings/security', words: 'password mfa 2fa totp sessions devices recovery codes' },
	{ key: 's-sftp', group: 'settings', label: 'SFTP and API keys', hint: 'Keys for SFTP and automation tokens', icon: 'key', href: '/settings/sftp', words: 'ssh token automation api keys' }
];

export const adminPages: Entry[] = [
	{ key: 'a-users', group: 'admin', label: 'Users', hint: 'Administration · accounts and invitations', icon: 'users', href: '/admin/users', words: 'accounts people invite register roles disable admin' },
	{ key: 'a-workspaces', group: 'admin', label: 'Workspaces', hint: 'Administration · every workspace', icon: 'building', href: '/admin/workspaces', words: 'teams members' },
	{ key: 'a-sites', group: 'admin', label: 'Sites and domains', hint: 'Administration · hosted sites', icon: 'globe', href: '/admin/sites', words: 'static hosting custom domain dns suspend' },
	{ key: 'a-analytics', group: 'admin', label: 'Analytics', hint: 'Administration · usage trends across the panel', icon: 'chart', href: '/admin/analytics', words: 'usage trends statistics stats reports top consumers deployments backups uptime accounts tickets csv export' },
	{ key: 'a-host', group: 'admin', label: 'Host', hint: 'Administration · CPU, memory, disk, network', icon: 'chart', href: '/admin/host', words: 'resources monitoring cpu memory ram disk network load swap capacity server machine usage' },
	{ key: 'a-host-bots', group: 'admin', label: 'Resource use per app', hint: 'Host · what each app consumes', icon: 'chart', href: '/admin/host?tab=bots', words: 'top cpu memory network pids consumers heavy' },
	{ key: 'a-host-capacity', group: 'admin', label: 'Capacity and budgets', hint: 'Host · memory reserved, limits', icon: 'chart', href: '/admin/host?tab=capacity', words: 'budget admission memory limit node reserved' },
	{ key: 'a-logs', group: 'admin', label: 'Panel logs', hint: 'Host · what RivetPanel itself logged', icon: 'terminal', href: '/admin/host?tab=logs', words: 'log errors warnings server output debug journal' },
	{ key: 'a-log-archive', group: 'admin', label: 'Logs and retention', hint: 'Administration · daily log archive, graph history', icon: 'archive', href: '/admin/logs', words: 'log archive retention gzip days history download console output graph telemetry metrics keep delete disk' },
	{ key: 'a-env', group: 'admin', label: 'Environment', hint: 'Administration · RIVET_ variables', icon: 'sliders', href: '/admin/environment', words: 'env variables configuration config settings limits restart environment file rivetpanel' },
	{ key: 'a-settings', group: 'admin', label: 'Panel settings', hint: 'Administration · address, sign-in, AI', icon: 'gear', href: '/admin/settings', words: 'public url oauth github discord registration signup' },
	{ key: 'a-modules', group: 'admin', label: 'Modules', hint: 'Administration · stable and preview capabilities', icon: 'layers', href: '/admin/modules', words: 'features preview experimental agents games identity support extensions virtual machines' },
	{ key: 'a-ai', group: 'admin', label: 'AI operator providers', hint: 'Panel settings · models and API keys', icon: 'sparkle', href: '/admin/settings#ai-providers', words: 'ai assistant llm deepseek openai provider model api key chat' },
	{ key: 'a-research', group: 'admin', label: 'AI web research', hint: 'Panel settings · SearxNG / Risa search', icon: 'search', href: '/admin/settings#ai-research', words: 'ai search searxng risa web fetch' },
	{ key: 'a-diag', group: 'admin', label: 'Diagnostics', hint: 'Administration · health checks', icon: 'shield', href: '/admin/diagnostics', words: 'doctor status checks problems docker keys backups report' }
];

/** Pages of the documentation, from the docs/guide manifest (headings are
 * searchable words, so "probe" finds Health and alerts). Loaded when the
 * palette opens, so the manifest stays out of every other page. */
export async function loadDocs(): Promise<Entry[]> {
	const { manifest } = await import('virtual:docs');
	return manifest.categories.flatMap((c) =>
		c.pages.map((p) => ({
			key: `d-${p.slug}`,
			group: 'docs',
			label: p.title,
			hint: `Documentation · ${c.title}`,
			icon: p.icon as IconName,
			href: `/docs/${p.slug}`,
			words: [p.lede, ...p.headings.map((h) => h.text)].join(' ')
		}))
	);
}

/** The sections an app of a kind can have, from the tab registry (labels and
 * search words stay in one place); BotView falls back to Overview for ones
 * the person may not open. */
export function appTabs(kind: AppKind): { id: string; label: string; icon: IconName; words: string }[] {
	return tabDefs
		.filter((d) => d.kinds.includes(kind))
		.map((d) => ({ id: d.id, label: typeof d.label === 'string' ? d.label : d.id === 'mods' ? 'Mods or plugins' : d.id, icon: d.icon, words: d.words ?? '' }));
}

const norm = (s: string) => s.toLowerCase();

/** Every word of the query must match; a hit in the label counts for more than
 * one in the hint or the extra words, and an earlier hit for more than a later
 * one. 0 means no match. */
export function score(e: Pick<Entry, 'label' | 'hint' | 'words'>, query: string): number {
	const terms = norm(query).split(/\s+/).filter(Boolean);
	if (!terms.length) return 1;
	const label = norm(e.label);
	const rest = norm(`${e.hint} ${e.words ?? ''}`);
	let total = 0;
	for (const t of terms) {
		let s = 0;
		const i = label.indexOf(t);
		if (i === 0) s = 120;
		else if (i > 0) s = label[i - 1] === ' ' || label[i - 1] === '_' ? 100 : 80 - Math.min(i, 30);
		else if (rest.includes(t)) s = 40;
		else {
			// Letters in order inside the label ("dgn" finds Diagnostics).
			let pos = 0;
			let ok = t.length >= 3;
			for (const ch of t) {
				pos = label.indexOf(ch, pos);
				if (pos < 0) { ok = false; break; }
				pos++;
			}
			s = ok ? 12 : 0;
		}
		if (!s) return 0;
		total += s;
	}
	return total;
}
