// Path rules for moving things around the file explorer. Paths the person
// types start from the current folder; a leading slash starts from the
// workspace root instead, and . and .. work as they do in a shell. Results are
// workspace-relative with no leading slash; '' is the root itself.

export type Resolved = { path: string } | { error: string };

/** Resolves a typed folder path against cwd. '' and '/' name the root. */
export function resolveFolder(cwd: string, input: string, rootLabel = 'workspace'): Resolved {
	const v = input.trim();
	if (/[\\\0]/.test(v)) return { error: 'Use forward slashes (/) to separate folders.' };
	if (/[^/]\/\/|^\/\/./.test(v)) return { error: 'Remove the doubled slash.' };
	const out = v.startsWith('/') || !cwd ? [] : cwd.split('/');
	for (const part of v.split('/')) {
		if (part === '' || part === '.') continue;
		if (part === '..') {
			if (!out.length) return { error: `That goes above the ${rootLabel} folder.` };
			out.pop();
		} else out.push(part);
	}
	return { path: out.join('/') };
}

/**
 * Resolves what someone typed in Rename: a plain name renames in place, a path
 * moves (data/.env.example), and a trailing slash keeps the current name
 * (data/ moves .env.example into data).
 */
export function resolveTarget(cwd: string, input: string, name: string, rootLabel = 'workspace'): Resolved {
	const v = input.trim();
	const intoFolder = v.endsWith('/');
	const dir = intoFolder ? v : v.slice(0, v.lastIndexOf('/') + 1);
	const base = intoFolder ? name : v.slice(v.lastIndexOf('/') + 1);
	if (base === '.' || base === '..') return { error: 'End the path with a name, not . or ..' };
	const folder = dir ? resolveFolder(cwd, dir, rootLabel) : { path: cwd };
	if ('error' in folder) return folder;
	return { path: join(folder.path, base) };
}

export const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);
export const parentOf = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '');
export const baseOf = (p: string) => p.slice(p.lastIndexOf('/') + 1);
/** How a workspace path is shown to people: always from the root. */
export const shown = (p: string) => `/${p}`;
/** True when p is the folder `dir` or anything inside it. */
export const within = (p: string, dir: string) => p === dir || p.startsWith(dir + '/');
