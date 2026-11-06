import { api } from '$lib/api/client';
import { session } from '$lib/session.svelte';
import { Perm } from '$lib/api/types';
import type { ExtContribution } from './types';

// What enabled extensions add to the interface for the signed-in account.
export const extensions = $state<{ list: ExtContribution[]; loaded: boolean }>({ list: [], loaded: false });

let loading: Promise<void> | null = null;

/** Loads (or reloads) the contributions; a no-op when the module is off. */
export function loadExtensions(force = false): Promise<void> {
	if (!session.user || !session.features.extensions) {
		extensions.list = [];
		extensions.loaded = true;
		return Promise.resolve();
	}
	if (loading && !force) return loading;
	loading = api<{ extensions: ExtContribution[] }>('GET', '/extensions/contributions')
		.then((r) => {
			extensions.list = r.extensions ?? [];
		})
		.catch(() => {
			extensions.list = [];
		})
		.finally(() => {
			extensions.loaded = true;
			loading = null;
		});
	return loading;
}

/** The per-bot permission bit a manifest's bot_perm names (0: any access). */
export function botPermBit(p?: string): number {
	switch (p) {
		case 'console':
			return Perm.console;
		case 'power':
			return Perm.power;
		case 'files':
			return Perm.files;
		case 'manage_env':
			return Perm.env;
	}
	return 0;
}

/** The interface path of an extension page. */
export const extPageHref = (ext: string, page: string) => `/x/${encodeURIComponent(ext)}/${encodeURIComponent(page)}`;
