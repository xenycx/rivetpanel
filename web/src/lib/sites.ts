import type { Site } from '$lib/api/types';

// Shared labels for site lists (the Websites list and the overview).

const appStates: Record<string, { tone?: string; label: string }> = {
	running: { tone: 'run', label: 'Running' },
	building: { tone: 'warn', label: 'Building' },
	starting: { tone: 'warn', label: 'Starting' },
	failed: { tone: 'fail', label: 'Failed' }
};

/** A site's state as a pill tone and label. */
export const siteStatus = (s: Site): { tone?: string; label: string } =>
	s.disabled
		? { tone: 'fail', label: 'Suspended' }
		: s.mode === 'app'
			? (appStates[s.app_state] ?? { tone: undefined, label: 'Stopped' })
			: s.current_release
				? { tone: 'run', label: 'Live' }
				: { tone: undefined, label: 'Empty' };

/** How a site is made: an app, a build, GitHub or uploaded files. */
export const siteKind = (s: Site) =>
	s.mode === 'app' ? `App${s.framework ? ` · ${s.framework}` : ''}` : s.app_id ? `Build${s.framework ? ` · ${s.framework}` : ''}` : s.repo_full_name ? 'GitHub' : s.mode === 'page' ? 'Bot page' : 'Upload';

/** An address without its scheme or trailing slash. */
export const hostOf = (url: string) => url.replace(/^https?:\/\//, '').replace(/\/$/, '');
