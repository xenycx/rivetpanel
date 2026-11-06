import type { FileEntry } from '$lib/api/types';

/** How the file explorer orders a folder. */
export type SortKey = 'name' | 'size' | 'modified' | 'type';
export type FileSort = { key: SortKey; desc: boolean; foldersFirst: boolean };

export const DEFAULT_SORT: FileSort = { key: 'name', desc: false, foldersFirst: true };

export const SORT_LABELS: Record<SortKey, string> = {
	name: 'Name',
	size: 'Size',
	modified: 'Date modified',
	type: 'Type'
};

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

/** The extension used for "Type" ordering; dotfiles and extensionless names have none. */
export function extension(name: string): string {
	const i = name.lastIndexOf('.');
	return i > 0 ? name.slice(i + 1).toLowerCase() : '';
}

/**
 * Sorts a folder listing. Names compare naturally ("file2" before "file10").
 * Ties fall back to the name so the order is stable across refreshes. Entries
 * whose time is unknown (an older node agent) stay last whatever the direction.
 */
export function sortEntries(entries: readonly FileEntry[], s: FileSort): FileEntry[] {
	const dir = s.desc ? -1 : 1;
	const byName = (a: FileEntry, b: FileEntry) => collator.compare(a.name, b.name);
	const primary = (a: FileEntry, b: FileEntry): number => {
		switch (s.key) {
			case 'size':
				return a.size - b.size;
			case 'type':
				return collator.compare(extension(a.name), extension(b.name));
			case 'modified':
				return (a.modified_ms ?? 0) - (b.modified_ms ?? 0);
			default:
				return byName(a, b);
		}
	};
	return [...entries].sort((a, b) => {
		if (s.foldersFirst && (a.type === 'dir') !== (b.type === 'dir')) return a.type === 'dir' ? -1 : 1;
		if (s.key === 'modified' && !a.modified_ms !== !b.modified_ms) return a.modified_ms ? -1 : 1;
		return dir * primary(a, b) || dir * byName(a, b);
	});
}

/** Reads a stored sort, ignoring anything malformed. */
export function parseSort(raw: string | null): FileSort {
	try {
		const v = JSON.parse(raw ?? '');
		if (v && v.key in SORT_LABELS) return { key: v.key, desc: !!v.desc, foldersFirst: v.foldersFirst !== false };
	} catch {
		/* fall through */
	}
	return { ...DEFAULT_SORT };
}
