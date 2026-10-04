// What the person is looking at, for the AI chat. Pages that know more than
// their URL (a bot's name, the tab in view, the open file) publish it here; the
// chat adds the route itself. The server only trusts the bot or site id.

export type PageTarget = { kind: 'bot' | 'site'; id: string; label: string; section?: string };

export const aiPage = $state<{ target: PageTarget | null; detail: string }>({ target: null, detail: '' });

/** Publishes the bot or site in view. Returns a cleanup for $effect. */
export function publishTarget(t: PageTarget): () => void {
	aiPage.target = t;
	return () => {
		if (aiPage.target?.id === t.id) aiPage.target = null;
	};
}

/** Extra detail about the view, such as the open file. Empty clears it. */
export function publishDetail(detail: string): () => void {
	aiPage.detail = detail;
	return () => {
		if (aiPage.detail === detail) aiPage.detail = '';
	};
}

export type ViewContext = { kind: 'bot' | 'site' | 'page'; id?: string; label?: string; path: string; section?: string; detail?: string };

const pageNames: [RegExp, string][] = [
	[/^\/dashboard/, 'Dashboard'],
	[/^\/bots\/new/, 'New bot'],
	[/^\/templates/, 'Templates'],
	[/^\/sites/, 'Sites'],
	[/^\/activity/, 'Activity'],
	[/^\/settings/, 'Account settings'],
	[/^\/admin/, 'Administration'],
	[/^\/docs/, 'Documentation']
];

export function pageLabel(pathname: string): string {
	return pageNames.find(([re]) => re.test(pathname))?.[1] ?? 'RivetPanel';
}

/** The context to attach to a message sent from this URL. */
export function currentView(pathname: string, search: string): ViewContext {
	const path = pathname + search;
	const t = aiPage.target;
	if (t) return { kind: t.kind, id: t.id, label: t.label, path, section: t.section, detail: aiPage.detail || undefined };
	return { kind: 'page', label: pageLabel(pathname), path };
}

/** A short human description, shown on the context chip. */
export function describeView(v: ViewContext): string {
	const parts = [v.label ?? pageLabel(v.path.split('?')[0])];
	if (v.section) parts.push(v.section.replace(/[-_]/g, ' '));
	return parts.join(' · ');
}
