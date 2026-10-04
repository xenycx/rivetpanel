import type { IconName } from '$lib/components/ui/Icon.svelte';
import { can, session } from '$lib/session.svelte';

/** One thing the signed-in account can create, for the "New" menu, the overview and Go to. */
export type CreateOption = { key: string; label: string; hint: string; icon: IconName; href: string; group: string; words?: string; /** The Go to action name when "New <label>" does not read well. */ action?: string };

/**
 * What this account can create, grouped, following its role permissions and
 * the panel's enabled features. The server enforces every one of these; the
 * list only avoids offering what would be refused.
 */
export function createOptions(): CreateOption[] {
	if (!session.user) return [];
	const out: CreateOption[] = [];
	if (can('bots.create')) {
		out.push(
			{ key: 'bot-github', group: 'Discord bot', label: 'From GitHub', hint: 'Deploy a public or your own repository', icon: 'github', href: '/bots/new?source=github', words: 'bot repository deploy git' },
			{ key: 'bot-template', group: 'Discord bot', label: 'From a template', hint: 'Working starters in seven languages', icon: 'layers', href: '/bots/new?source=template', words: 'bot starter discord.js discord.py' },
			{ key: 'bot-zip', group: 'Discord bot', label: 'Upload a ZIP', hint: 'Empty bot, then upload under Files', icon: 'upload', href: '/bots/new?source=blank', words: 'bot archive upload zip files' },
			{ key: 'bot-empty', group: 'Discord bot', label: 'Empty bot', hint: 'Choose a language, add code later', icon: 'file', href: '/bots/new?source=blank', words: 'bot blank language' }
		);
		if (session.features.games)
			out.push(
				{ key: 'game-minecraft', group: 'Game server', label: 'Minecraft server', hint: 'Paper, Fabric, Forge, Vanilla and more', icon: 'cube', href: '/servers/new?family=minecraft', words: 'game server minecraft paper purpur fabric forge neoforge folia velocity vanilla' },
				{ key: 'game-steam', group: 'Game server', label: 'Steam game server', hint: 'Valheim, Rust, Project Zomboid', icon: 'gamepad', href: '/servers/new?family=steam', words: 'game server steam valheim rust zomboid steamcmd' },
				{ key: 'game-any', group: 'Game server', label: 'Browse all server types', hint: 'Every enabled game', icon: 'grid', href: '/servers/new', words: 'game server new' }
			);
	}
	if (session.features.sites && can('sites.create'))
		out.push(
			{ key: 'site', action: 'New site', group: 'Site', label: 'Empty site', hint: 'Host your own static files', icon: 'globe', href: '/sites?new=1', words: 'site static website host page' },
			{ key: 'site-template', action: 'New site from a template', group: 'Site', label: 'From a template', hint: 'Landing page, docs, portfolio and more', icon: 'layers', href: '/templates?tab=sites', words: 'site template starter landing docs portfolio coming soon game community static website' }
		);
	if (session.features.workspaces && can('workspaces.create'))
		out.push({ key: 'workspace', group: 'More', label: 'Workspace', hint: 'Share bots and servers with a team', icon: 'building', href: '/settings/workspaces?new=1', words: 'team workspace members' });
	if (can('tickets.create'))
		out.push({ key: 'ticket', group: 'More', label: 'Support ticket', hint: 'Ask the panel staff for help', icon: 'lifebuoy', href: '/support?new=1', words: 'help support ticket question' });
	if (can('api_keys.manage'))
		out.push({ key: 'api-client', action: 'New API client or key', group: 'More', label: 'API client or key', hint: 'Tokens for scripts, CI and SFTP', icon: 'key', href: '/settings/sftp', words: 'api client token automation sftp key' });
	const admin = session.user.role === 'admin';
	if (admin || can('users.manage')) out.push({ key: 'a-invite', action: 'Invite a user', group: 'Administration', label: 'Invite a user', hint: 'Create an account or invitation', icon: 'users', href: '/admin/users?invite=1', words: 'invite user account' });
	if (session.features.agents && (admin || can('nodes.manage'))) out.push({ key: 'a-node', action: 'Add a node', group: 'Administration', label: 'Node', hint: 'Enroll a remote Docker host', icon: 'monitor', href: '/admin/nodes', words: 'node agent remote host enroll' });
	if (session.features.games && (admin || can('blueprints.manage'))) out.push({ key: 'a-egg', action: 'Import a server type', group: 'Administration', label: 'Server type', hint: 'Import a Pterodactyl egg', icon: 'cube', href: '/admin/blueprints', words: 'blueprint egg import server type' });
	if (admin || can('kb.manage')) out.push({ key: 'a-kb', action: 'New help article', group: 'Administration', label: 'Help article', hint: 'Write for the help center', icon: 'book', href: '/admin/kb', words: 'knowledgebase article help' });
	if (admin || can('status.manage')) out.push({ key: 'a-incident', action: 'Post a status incident', group: 'Administration', label: 'Status incident', hint: 'Post to the status page', icon: 'activity', href: '/admin/status', words: 'status incident maintenance outage' });
	if (admin || can('mail.announce')) out.push({ key: 'a-announce', action: 'Send an announcement', group: 'Administration', label: 'Announcement', hint: 'Notify or email accounts', icon: 'send', href: '/admin/mail', words: 'announcement news email notify' });
	return out;
}

/** The options grouped in display order. */
export function groupedCreateOptions(): [string, CreateOption[]][] {
	const groups = new Map<string, CreateOption[]>();
	for (const o of createOptions()) {
		if (!groups.has(o.group)) groups.set(o.group, []);
		groups.get(o.group)!.push(o);
	}
	return [...groups];
}
