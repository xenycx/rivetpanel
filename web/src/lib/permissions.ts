// Short names for role permissions, for places that cannot load the
// administration catalog (the verification banner, the profile page). The
// server's catalog (GET /admin/permissions) is the source of truth.
const labels: Record<string, string> = {
	'bots.create': 'creating bots and servers',
	'bots.delete': 'deleting bots and servers',
	'bots.console': 'the console',
	'bots.power': 'power actions and console input',
	'bots.files': 'files',
	'bots.env': 'environment variables',
	'bots.backups': 'backups',
	'bots.deploy': 'deployments',
	'bots.share': 'sharing',
	'sites.create': 'creating sites',
	'workspaces.create': 'creating workspaces',
	'api_keys.manage': 'API keys and tokens',
	'ai.use': 'the AI assistant',
	'tickets.create': 'support tickets',
	'users.view': 'viewing accounts',
	'users.manage': 'managing accounts',
	'roles.manage': 'managing roles',
	'nodes.manage': 'nodes',
	'blueprints.manage': 'server types',
	'allocations.manage': 'allocations',
	'settings.manage': 'panel settings',
	'mail.announce': 'announcements',
	'ai.manage': 'AI providers',
	'sites.manage': 'sites administration',
	'workspaces.view': 'viewing every workspace',
	'system.view': 'host monitoring and diagnostics',
	'tickets.view_all': 'viewing every ticket',
	'tickets.manage': 'answering tickets',
	'kb.manage': 'the knowledgebase',
	'status.manage': 'the status page',
	'analytics.view': 'usage analytics'
};

/** A readable list of permissions ("files, backups and sharing"). */
export function describePermissions(perms: string[]): string {
	const names = perms.map((p) => labels[p] ?? p);
	return names.length < 2 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/** Administration permissions (the server's catalog group "administration"). */
export const adminPermissions = [
	'users.view',
	'users.manage',
	'roles.manage',
	'nodes.manage',
	'blueprints.manage',
	'allocations.manage',
	'settings.manage',
	'mail.announce',
	'ai.manage',
	'sites.manage',
	'workspaces.view',
	'system.view',
	'tickets.view_all',
	'tickets.manage',
	'kb.manage',
	'status.manage',
	'analytics.view'
];
