// How the support pages lay out tickets. A per-browser preference, like the
// theme (lib/ui/theme.svelte.ts): Settings → Appearance and the switcher on
// the Support page both write it. On narrow windows every layout becomes
// the classic list.

export type SupportLayout = 'split' | 'table' | 'board' | 'list';

export const supportLayouts: { id: SupportLayout; name: string; text: string }[] = [
	{ id: 'split', name: 'Split', text: 'Tickets on the left, the open ticket on the right.' },
	{ id: 'table', name: 'Table', text: 'Sortable columns; a ticket opens in a panel from the right.' },
	{ id: 'board', name: 'Board', text: 'A column per status; staff drag cards to change it.' },
	{ id: 'list', name: 'Classic list', text: 'A full-width list; a ticket opens on its own page.' }
];

const KEY = 'rivetpanel.supportLayout';
const SORT_KEY = 'rivetpanel.supportSort';

function read(): SupportLayout {
	try {
		const v = localStorage.getItem(KEY);
		if (supportLayouts.some((l) => l.id === v)) return v as SupportLayout;
	} catch {
		/* storage unavailable: the default */
	}
	return 'split';
}

export type SortKey = 'key' | 'subject' | 'status' | 'priority' | 'requester' | 'assignee' | 'updated';
function readSort(): { key: SortKey; desc: boolean } {
	try {
		const v = JSON.parse(localStorage.getItem(SORT_KEY) ?? 'null');
		if (v && typeof v.key === 'string' && typeof v.desc === 'boolean') return v;
	} catch {
		/* the default */
	}
	return { key: 'updated', desc: true };
}

export const supportView = $state<{ layout: SupportLayout; sort: { key: SortKey; desc: boolean } }>({
	layout: typeof localStorage === 'undefined' ? 'split' : read(),
	sort: typeof localStorage === 'undefined' ? { key: 'updated', desc: true } : readSort()
});

export function setSupportLayout(l: SupportLayout) {
	supportView.layout = l;
	try {
		localStorage.setItem(KEY, l);
	} catch {
		/* kept for this page only */
	}
}

export function setSupportSort(key: SortKey) {
	supportView.sort = supportView.sort.key === key ? { key, desc: !supportView.sort.desc } : { key, desc: key === 'updated' };
	try {
		localStorage.setItem(SORT_KEY, JSON.stringify(supportView.sort));
	} catch {
		/* kept for this page only */
	}
}
