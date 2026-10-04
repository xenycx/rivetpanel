// The caller's workspaces and the one the overview is scoped to. The choice
// is a per-browser convenience ("all" shows every workspace at once).
import { api } from '$lib/api/client';
import type { Workspace } from '$lib/api/types';

const KEY = 'rivetpanel.workspace';

function readSelected(): string {
	try {
		return localStorage.getItem(KEY) || 'all';
	} catch {
		return 'all';
	}
}

export const workspaces = $state<{ list: Workspace[]; loaded: boolean; selected: string }>({ list: [], loaded: false, selected: readSelected() });

let inflight: Promise<void> | null = null;

/** Loads (or reloads) the list; concurrent callers share one request. */
export function loadWorkspaces(): Promise<void> {
	inflight ??= api<{ workspaces: Workspace[] }>('GET', '/workspaces')
		.then((r) => {
			workspaces.list = r.workspaces;
			workspaces.loaded = true;
			if (workspaces.selected !== 'all' && !r.workspaces.some((w) => w.id === workspaces.selected)) selectWorkspace('all');
		})
		.catch(() => {
			/* the switcher is a convenience; pages report their own errors */
		})
		.finally(() => (inflight = null));
	return inflight;
}

export function selectWorkspace(id: string) {
	workspaces.selected = id;
	try {
		if (id === 'all') localStorage.removeItem(KEY);
		else localStorage.setItem(KEY, id);
	} catch {
		/* not remembered */
	}
}

export const currentWorkspace = () => workspaces.list.find((w) => w.id === workspaces.selected) ?? null;
export const personalWorkspace = () => workspaces.list.find((w) => w.personal) ?? null;
export const workspaceName = (id: string) => {
	const w = workspaces.list.find((x) => x.id === id);
	return w ? (w.personal ? 'Personal' : w.name) : '';
};
/** Workspaces where the caller may create bots and sites. */
export const creatable = () => workspaces.list.filter((w) => w.role === 'owner' || w.role === 'admin' || w.role === 'developer');
