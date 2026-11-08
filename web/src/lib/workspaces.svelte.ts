// The caller's workspaces and the one the overview is scoped to. The choice
// is a per-browser convenience ("all" shows every workspace at once). With a
// single workspace there is nothing to choose: the scope stays "all".
import { api, ApiError } from '$lib/api/client';
import type { Workspace } from '$lib/api/types';

const KEY = 'rivetpanel.workspace';

function readSelected(): string {
	try {
		return localStorage.getItem(KEY) || 'all';
	} catch {
		return 'all';
	}
}

export const workspaces = $state<{ list: Workspace[]; loaded: boolean; selected: string; error: string }>({ list: [], loaded: false, selected: readSelected(), error: '' });

let inflight: Promise<void> | null = null;

/** Loads (or reloads) the list; concurrent callers share one request. */
export function loadWorkspaces(): Promise<void> {
	inflight ??= api<{ workspaces: Workspace[] }>('GET', '/workspaces')
		.then((r) => {
			workspaces.list = r.workspaces;
			workspaces.loaded = true;
			workspaces.error = '';
			if (workspaces.selected !== 'all' && (r.workspaces.length < 2 || !r.workspaces.some((w) => w.id === workspaces.selected))) selectWorkspace('all');
		})
		.catch((e) => {
			// The switcher shows this with a retry; pages report their own errors.
			workspaces.error = e instanceof ApiError ? e.message : 'Workspaces could not be loaded.';
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
/** A workspace's display name: its stored name ("Personal" when a personal one has none). */
export function wsName(w: { name: string; personal: boolean }): string {
	return w.name || 'Personal';
}
export const workspaceName = (id: string) => {
	const w = workspaces.list.find((x) => x.id === id);
	return w ? wsName(w) : '';
};
/** Workspaces where the caller may create bots and sites. */
export const creatable = () => workspaces.list.filter((w) => w.role === 'owner' || w.role === 'admin' || w.role === 'developer');
