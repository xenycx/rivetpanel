import type { IconName } from '$lib/components/ui/Icon.svelte';
import { can, session } from '$lib/session.svelte';

type Section = { href: string; label: string; icon: IconName; match?: string; perms: string[] };

/** Administration sections in four groups, and the permission each needs ('admin' = built-in administrators only). */
const groups: { label: string; sections: Section[] }[] = [
	{
		label: 'People',
		sections: [
			{ href: '/admin/users', label: 'Users', icon: 'users', match: '/admin/users/', perms: ['users.view'] },
			{ href: '/admin/workspaces', label: 'Workspaces', icon: 'building', match: '/admin/workspaces/', perms: ['workspaces.view'] },
			{ href: '/admin/roles', label: 'Roles', icon: 'key', perms: ['roles.manage', 'users.manage'] },
			{ href: '/admin/sign-in', label: 'Single sign-on', icon: 'lock', perms: ['settings.manage'] },
			{ href: '/admin/api-clients', label: 'API clients', icon: 'link', perms: ['users.view'] }
		]
	},
	{
		label: 'Hosting',
		sections: [
			{ href: '/admin/nodes', label: 'Nodes', icon: 'monitor', perms: ['nodes.manage'] },
			{ href: '/admin/sites', label: 'Sites and domains', icon: 'globe', perms: ['sites.manage'] },
			{ href: '/admin/blueprints', label: 'Server types', icon: 'cube', perms: ['blueprints.manage'] },
			{ href: '/admin/allocations', label: 'Allocations', icon: 'network', perms: ['allocations.manage'] }
		]
	},
	{
		label: 'Monitoring',
		sections: [
			{ href: '/admin/analytics', label: 'Analytics', icon: 'chart', perms: ['analytics.view'] },
			{ href: '/admin/host', label: 'Host', icon: 'server', perms: ['system.view'] },
			{ href: '/admin/logs', label: 'Logs and retention', icon: 'archive', perms: ['settings.manage', 'system.view'] },
			{ href: '/admin/status', label: 'Status page', icon: 'activity', perms: ['status.manage'] },
			{ href: '/admin/diagnostics', label: 'Diagnostics', icon: 'shield', perms: ['system.view'] }
		]
	},
	{
		label: 'Panel',
		sections: [
			{ href: '/admin/settings', label: 'Panel settings', icon: 'gear', perms: ['settings.manage', 'ai.manage'] },
			{ href: '/admin/modules', label: 'Modules', icon: 'layers', perms: ['admin'] },
			{ href: '/admin/mail', label: 'Announcements', icon: 'send', perms: ['mail.announce'] },
			{ href: '/support?queue=all', label: 'Support queue', icon: 'lifebuoy', perms: ['tickets.view_all', 'tickets.manage'] },
			{ href: '/admin/kb', label: 'Knowledgebase', icon: 'book', match: '/admin/kb/', perms: ['kb.manage'] },
			{ href: '/admin/environment', label: 'Environment', icon: 'sliders', perms: ['admin'] }
		]
	}
];

function allowed(s: Section): boolean {
	return session.user?.role === 'admin' || s.perms.some((p) => p !== 'admin' && can(p));
}

/** The administration groups the signed-in account may open (empty groups left out). */
export function adminGroups() {
	return groups
		.map((g) => ({ label: g.label, links: g.sections.filter(allowed).map(({ perms: _p, ...l }) => l) }))
		.filter((g) => g.links.length > 0);
}

/** The administration sections the signed-in account may open, in order. */
export function adminLinks() {
	return adminGroups().flatMap((g) => g.links);
}
