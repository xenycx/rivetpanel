import type { IconName } from '$lib/components/ui/Icon.svelte';
import { can, session } from '$lib/session.svelte';

/** Administration sections and the permission each needs ('admin' = built-in administrators only). */
const sections: { href: string; label: string; icon: IconName; match?: string; perms: string[] }[] = [
	{ href: '/admin/users', label: 'Users', icon: 'users', match: '/admin/users/', perms: ['users.view'] },
	{ href: '/admin/roles', label: 'Roles', icon: 'key', perms: ['roles.manage', 'users.manage'] },
	{ href: '/admin/api-clients', label: 'API clients', icon: 'link', perms: ['users.view'] },
	{ href: '/admin/workspaces', label: 'Workspaces', icon: 'building', match: '/admin/workspaces/', perms: ['workspaces.view'] },
	{ href: '/admin/sites', label: 'Sites and domains', icon: 'globe', perms: ['sites.manage'] },
	{ href: '/admin/blueprints', label: 'Server types', icon: 'cube', perms: ['blueprints.manage'] },
	{ href: '/admin/allocations', label: 'Allocations', icon: 'network', perms: ['allocations.manage'] },
	{ href: '/admin/nodes', label: 'Nodes', icon: 'monitor', perms: ['nodes.manage'] },
	{ href: '/admin/analytics', label: 'Analytics', icon: 'chart', perms: ['analytics.view'] },
	{ href: '/admin/host', label: 'Host', icon: 'chart', perms: ['system.view'] },
	{ href: '/admin/settings', label: 'Panel settings', icon: 'gear', perms: ['settings.manage', 'ai.manage'] },
	{ href: '/admin/sign-in', label: 'Single sign-on', icon: 'key', perms: ['settings.manage'] },
	{ href: '/admin/modules', label: 'Modules', icon: 'layers', perms: ['admin'] },
	{ href: '/admin/mail', label: 'Announcements', icon: 'send', perms: ['mail.announce'] },
	{ href: '/support?queue=all', label: 'Support queue', icon: 'lifebuoy', perms: ['tickets.view_all', 'tickets.manage'] },
	{ href: '/admin/kb', label: 'Knowledgebase', icon: 'book', match: '/admin/kb/', perms: ['kb.manage'] },
	{ href: '/admin/status', label: 'Status page', icon: 'activity', perms: ['status.manage'] },
	{ href: '/admin/environment', label: 'Environment', icon: 'sliders', perms: ['admin'] },
	{ href: '/admin/diagnostics', label: 'Diagnostics', icon: 'shield', perms: ['system.view'] }
];

/** The administration sections the signed-in account may open. */
export function adminLinks() {
	const admin = session.user?.role === 'admin';
	return sections.filter((s) => admin || s.perms.some((p) => p !== 'admin' && can(p))).map(({ perms: _p, ...l }) => l);
}
