// The unified activity timeline: what people changed and what the panel did
// on its own (schedules, GitHub pushes, crash handling, the AI operator), in
// one stream. Wording lives here so the page, rows and report agree.
import type { Operation } from '$lib/api/types';
import type { Person } from '$lib/api/support';
import { actionText } from '$lib/audit';
import type { IconName } from '$lib/components/ui/Icon.svelte';

export type ActivitySource = 'person' | 'schedule' | 'push' | 'system' | 'ai';
export type ActivityOutcome = 'ok' | 'failed' | 'denied' | 'running' | 'cancelled';
export type ActivityCategory = 'power' | 'deploys' | 'backups' | 'files' | 'variables' | 'settings' | 'access' | 'account' | 'admin' | 'ai' | 'extensions' | 'other';
export type ActivityApp = { id: string; name: string; kind: 'bot' | 'game' | 'web' | 'site'; logo_url: string; logo_variant: number };

export type ActivityItem = {
	key: string;
	at_ms: number;
	type: 'change' | 'work';
	source: ActivitySource;
	category: ActivityCategory;
	outcome: ActivityOutcome;
	actor: Person | null;
	actor_label: string | null;
	app: ActivityApp | null;
	action: string;
	target: string | null;
	op?: Operation;
};
export type ActivityPage = { items: ActivityItem[]; next?: { before: number; before_key: string } };

export type ActivityStats = {
	from: number;
	to: number;
	bucket_ms: number;
	buckets: { t: number; people: number; automatic: number; failed: number }[];
	total: number;
	by_source: Partial<Record<ActivitySource, number>>;
	by_outcome: Partial<Record<ActivityOutcome, number>>;
	by_category: Partial<Record<ActivityCategory, number>>;
	people: { id: string; label: string; person: Person | null; count: number; failed: number }[];
	apps: { app: ActivityApp; count: number; failed: number }[];
	/** Counts by local weekday (0 = Monday) and hour. */
	week: number[][];
};

export const categories: { id: ActivityCategory; label: string; icon: IconName }[] = [
	{ id: 'power', label: 'Power and crashes', icon: 'bolt' },
	{ id: 'deploys', label: 'Builds and deployments', icon: 'rocket' },
	{ id: 'backups', label: 'Backups and restores', icon: 'archive' },
	{ id: 'files', label: 'Files and packages', icon: 'file' },
	{ id: 'variables', label: 'Variables', icon: 'key' },
	{ id: 'settings', label: 'App and site settings', icon: 'sliders' },
	{ id: 'access', label: 'Sharing and workspaces', icon: 'users' },
	{ id: 'account', label: 'Accounts and sign-ins', icon: 'shield' },
	{ id: 'admin', label: 'Administration', icon: 'gear' },
	{ id: 'ai', label: 'AI operator', icon: 'sparkle' },
	{ id: 'extensions', label: 'Extensions and add-ons', icon: 'package' },
	{ id: 'other', label: 'Other', icon: 'activity' }
];
export const categoryOf = Object.fromEntries(categories.map((c) => [c.id, c])) as Record<ActivityCategory, (typeof categories)[number]>;

/** Who or what acts when no person did: the name and icon of the agent. */
export const agents: Record<Exclude<ActivitySource, 'person'>, { name: string; icon: IconName; text: string }> = {
	schedule: { name: 'Schedule', icon: 'clock', text: 'Scheduled actions run on their owner’s behalf' },
	push: { name: 'GitHub push', icon: 'github', text: 'Deployments started by a push to the linked repository' },
	system: { name: 'Panel', icon: 'server', text: 'Things the panel did or noticed by itself: builds on start, crashes, restarts' },
	ai: { name: 'AI operator', icon: 'sparkle', text: 'Live actions the AI operator took for someone' }
};
export const sourceText: Record<ActivitySource, string> = {
	person: 'People',
	schedule: 'Schedules',
	push: 'GitHub pushes',
	system: 'The panel',
	ai: 'AI operator'
};

export const outcomes: { id: ActivityOutcome; label: string }[] = [
	{ id: 'ok', label: 'Succeeded' },
	{ id: 'failed', label: 'Failed' },
	{ id: 'denied', label: 'Refused' },
	{ id: 'running', label: 'In progress' },
	{ id: 'cancelled', label: 'Cancelled' }
];

// Wording for actions the timeline adds to the change record.
const extraText: Record<string, string> = {
	'bot.crash': 'crashed',
	'bot.gave_up': 'stopped after crashing and will not restart on its own',
	'scheduled.start': 'started',
	'scheduled.stop': 'stopped',
	'scheduled.restart': 'restarted',
	'scheduled.backup': 'started a backup of',
	'scheduled.deploy': 'started a deployment of',
	'scheduled.chain': 'ran a task chain on'
};
// The AI operator is the actor of its own live actions.
const aiText: Record<string, string> = {
	'ai.file_apply': 'changed a file on',
	'ai.diagnostic': 'ran a diagnostic on',
	'ai.restart': 'restarted',
	'ai.env_input': 'set variables on'
};

const workVerb: Record<Operation['kind'], [done: string, active: string]> = {
	build: ['built', 'is building'],
	deploy: ['deployed', 'is deploying'],
	rollback: ['rolled back', 'is rolling back'],
	backup: ['backed up', 'is backing up'],
	restore: ['restored', 'is restoring'],
	publish: ['pushed to GitHub', 'is pushing to GitHub']
};

/** The display name of whoever performed an item. */
export function actorName(it: ActivityItem): string {
	if (it.source === 'person') return it.actor?.name || it.actor_label || 'Someone';
	if (it.source === 'system' && it.type === 'change' && it.app) return it.app.name;
	return agents[it.source].name;
}

/** The account an automatic item acted for (schedule owner, AI requester). */
export function onBehalfOf(it: ActivityItem): string {
	if (it.source !== 'schedule' && it.source !== 'ai') return '';
	if (it.actor) return it.actor.name;
	return (it.actor_label ?? '').replace(/ via AI operator.*$/, '');
}

/**
 * How an item's sentence reads around the app: `pre` and `post` surround the
 * app's name. `place` says where the app goes: inline (between pre and post),
 * on (after the phrase: "set variables on music-bot"), subject (the app is
 * who: "music-bot crashed") or none (no app).
 */
export type Phrase = { pre: string; post: string; place: 'inline' | 'on' | 'subject' | 'none' };

export function phraseOf(it: ActivityItem): Phrase {
	const app = !!it.app;
	if (it.type === 'work' && it.op) {
		const o = it.op;
		const [done, active] = workVerb[o.kind];
		const gameBuild = o.kind === 'build' && o.bot_kind === 'game';
		let v = gameBuild ? (it.outcome === 'running' ? 'is installing' : 'installed') : it.outcome === 'running' ? active : done;
		if (it.outcome === 'failed') v = `could not ${gameBuild ? 'install' : baseVerb(o.kind)}`;
		if (it.outcome === 'cancelled') v = `cancelled ${gameBuild ? 'installing' : workNoun[o.kind] + ' of'}`;
		if (o.kind === 'publish' && it.outcome === 'ok') return { pre: 'pushed', post: 'to GitHub', place: 'inline' };
		return { pre: v.replace(/ to GitHub$/, ''), post: v.endsWith(' to GitHub') ? 'to GitHub' : '', place: 'inline' };
	}
	if (it.source === 'system' && (it.action === 'bot.crash' || it.action === 'bot.gave_up')) return { pre: '', post: extraText[it.action], place: app ? 'subject' : 'none' };
	if (it.action.startsWith('scheduled.')) return { pre: extraText[it.action] ?? 'ran a scheduled action on', post: '', place: app ? 'inline' : 'none' };
	if (it.source === 'ai' && aiText[it.action]) {
		const t = aiText[it.action];
		return t.endsWith(' on') ? { pre: t.slice(0, -3), post: '', place: app ? 'on' : 'none' } : { pre: t, post: '', place: app ? 'inline' : 'none' };
	}
	if (it.action === 'account.sign_in') return { pre: 'signed in' + (it.target ? ` with ${it.target}` : ''), post: '', place: 'none' };
	const t = actionText[it.action] ?? extraText[it.action] ?? it.action.replace(/[._]/g, ' ');
	if (!app) return { pre: t, post: '', place: 'none' };
	// Recorded wording names the app as "it", "its" or "the app": the app's
	// name takes that place.
	let m = t.match(/^(.*?) (?:it|the app)(?: (.*))?$/);
	if (m) return { pre: m[1], post: m[2] ?? '', place: 'inline' };
	m = t.match(/^(.*?) its (.*)$/);
	if (m) return { pre: m[1], post: `’s ${m[2]}`, place: 'inline' };
	return { pre: t, post: '', place: 'on' };
}

const workNoun: Record<Operation['kind'], string> = {
	build: 'a build',
	deploy: 'a deployment',
	rollback: 'a rollback',
	backup: 'a backup',
	restore: 'a restore',
	publish: 'a push'
};

function baseVerb(k: Operation['kind']): string {
	return { build: 'build', deploy: 'deploy', rollback: 'roll back', backup: 'back up', restore: 'restore', publish: 'push' }[k];
}

/** A scheduled run's target is "status: message"; split it. */
export function scheduledDetail(it: ActivityItem): { status: string; message: string } | null {
	if (!it.action.startsWith('scheduled.') || !it.target) return null;
	const i = it.target.indexOf(': ');
	return i < 0 ? { status: it.target, message: '' } : { status: it.target.slice(0, i), message: it.target.slice(i + 2) };
}

/** Where an app's page is. */
export function appHref(a: ActivityApp): string {
	switch (a.kind) {
		case 'game':
			return `/servers/${a.id}`;
		case 'web':
			return `/sites/app/${a.id}`;
		case 'site':
			return `/sites/${a.id}`;
		default:
			return `/bots/${a.id}`;
	}
}

export type Tone = 'run' | 'warn' | 'fail' | 'idle';
export function toneOf(it: ActivityItem): Tone {
	switch (it.outcome) {
		case 'failed':
		case 'denied':
			return 'fail';
		case 'running':
			return 'warn';
		case 'cancelled':
			return 'idle';
		default:
			return it.type === 'work' ? 'run' : 'idle';
	}
}

// ---- Time ranges -------------------------------------------------------------------

export const presets = [
	{ id: '1h', label: '1h', ms: 3600_000, text: 'Last hour' },
	{ id: '24h', label: '24h', ms: 86400_000, text: 'Last 24 hours' },
	{ id: '7d', label: '7d', ms: 7 * 86400_000, text: 'Last 7 days' },
	{ id: '30d', label: '30d', ms: 30 * 86400_000, text: 'Last 30 days' },
	{ id: '90d', label: '90d', ms: 90 * 86400_000, text: 'Last 90 days' }
] as const;
export type PresetId = (typeof presets)[number]['id'];

/** A time window: a rolling preset, or fixed bounds (from a brush or the picker). */
export type Range = { preset: PresetId } | { from: number; to: number };

export function resolveRange(r: Range, now = Date.now()): { from: number; to: number } {
	if ('preset' in r) {
		const p = presets.find((x) => x.id === r.preset) ?? presets[2];
		return { from: now - p.ms, to: now };
	}
	return r;
}

export function rangeText(r: Range, now = Date.now()): string {
	if ('preset' in r) return presets.find((x) => x.id === r.preset)?.text ?? '';
	const { from, to } = r;
	const sameDay = new Date(from).toDateString() === new Date(Math.max(from, to - 1)).toDateString();
	const d = (ms: number) => new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: new Date(ms).getFullYear() === new Date(now).getFullYear() ? undefined : 'numeric' });
	const t = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
	return sameDay ? `${d(from)}, ${t(from)} – ${t(to)}` : `${d(from)} ${t(from)} – ${d(to)} ${t(to)}`;
}

/** "Today", "Yesterday", "Tuesday", then a date: the timeline's day headings. */
export function dayHeading(ms: number, now = Date.now()): string {
	const d = new Date(ms);
	const today = new Date(now);
	today.setHours(0, 0, 0, 0);
	const day = new Date(d);
	day.setHours(0, 0, 0, 0);
	const diff = Math.round((today.getTime() - day.getTime()) / 86400_000);
	if (diff === 0) return 'Today';
	if (diff === 1) return 'Yesterday';
	if (diff < 7) return d.toLocaleDateString(undefined, { weekday: 'long' });
	return d.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric' });
}
export const dayKey = (ms: number) => new Date(ms).toDateString();

export function clockTime(ms: number): string {
	return new Date(ms).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

// ---- Filters and the URL --------------------------------------------------------------

export type Filters = {
	who: 'all' | 'people' | 'automatic';
	actors: string[];
	apps: string[];
	categories: ActivityCategory[];
	outcomes: ActivityOutcome[];
	q: string;
	range: Range;
};

export const emptyFilters = (): Filters => ({ who: 'all', actors: [], apps: [], categories: [], outcomes: [], q: '', range: { preset: '7d' } });

const list = (v: string | null) => (v ? v.split(',').filter(Boolean) : []);

export function filtersFromURL(u: URLSearchParams): Filters {
	const f = emptyFilters();
	const who = u.get('who');
	if (who === 'people' || who === 'automatic') f.who = who;
	f.actors = list(u.get('people'));
	f.apps = list(u.get('apps') ?? u.get('bot'));
	f.categories = list(u.get('cat')).filter((c): c is ActivityCategory => c in categoryOf);
	f.outcomes = list(u.get('outcome')).filter((o): o is ActivityOutcome => outcomes.some((x) => x.id === o));
	f.q = u.get('q') ?? '';
	const from = Number(u.get('from'));
	const to = Number(u.get('to'));
	const preset = u.get('range');
	if (from > 0 && to > from) f.range = { from, to };
	else if (presets.some((p) => p.id === preset)) f.range = { preset: preset as PresetId };
	// Links from before the timeline existed.
	if (u.get('view') === 'changes') f.who = 'all';
	const kind = u.get('kind');
	if (kind && !f.categories.length) f.categories = kind === 'backup' || kind === 'restore' ? ['backups'] : ['deploys'];
	return f;
}

export function filtersToURL(f: Filters, u: URL) {
	const set = (k: string, v: string) => (v ? u.searchParams.set(k, v) : u.searchParams.delete(k));
	for (const k of ['view', 'kind', 'bot']) u.searchParams.delete(k);
	set('who', f.who === 'all' ? '' : f.who);
	set('people', f.actors.join(','));
	set('apps', f.apps.join(','));
	set('cat', f.categories.join(','));
	set('outcome', f.outcomes.join(','));
	set('q', f.q.trim());
	if ('preset' in f.range) {
		set('range', f.range.preset === '7d' ? '' : f.range.preset);
		set('from', '');
		set('to', '');
	} else {
		set('range', '');
		set('from', String(f.range.from));
		set('to', String(f.range.to));
	}
}

/** The API query for a filter set (without the cursor). */
export function filterQuery(f: Filters, now = Date.now()): URLSearchParams {
	const q = new URLSearchParams();
	if (f.who === 'people') q.set('sources', 'person');
	if (f.who === 'automatic') q.set('sources', 'schedule,push,system,ai');
	if (f.actors.length) q.set('actors', f.actors.join(','));
	if (f.apps.length) q.set('apps', f.apps.join(','));
	if (f.categories.length) q.set('categories', f.categories.join(','));
	if (f.outcomes.length) q.set('outcomes', f.outcomes.join(','));
	if (f.q.trim()) q.set('q', f.q.trim());
	const { from, to } = resolveRange(f.range, now);
	q.set('from', String(Math.floor(from)));
	if ('from' in f.range) q.set('to', String(Math.ceil(to)));
	return q;
}

export const activeFilterCount = (f: Filters) =>
	(f.who !== 'all' ? 1 : 0) + f.actors.length + f.apps.length + f.categories.length + f.outcomes.length + (f.q.trim() ? 1 : 0);
