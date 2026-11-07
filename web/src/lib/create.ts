import type { IconName } from '$lib/components/ui/Icon.svelte';
import { can, session } from '$lib/session.svelte';

/** One thing the signed-in account can create, for the "New" menu, the overview and Go to. */
export type CreateOption = {
	key: string;
	label: string;
	hint: string;
	icon: IconName;
	href: string;
	group: string;
	words?: string;
	/** The Go to action name when "New <label>" does not read well. */
	action?: string;
	/** Only in Go to and the New page, not in the New menu (account and administration chores). */
	paletteOnly?: boolean;
};

/** The workload groups in the order the Apps list and the New page use. */
export const workloadGroups = ['Bot', 'Game server', 'Container', 'Website'] as const;

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
			{ key: 'bot-github', action: 'New bot from GitHub', group: 'Bot', label: 'From GitHub', hint: 'Deploy a public or your own repository', icon: 'github', href: '/bots/new?source=github', words: 'bot app worker repository deploy git' },
			{ key: 'bot-template', action: 'New bot from a template', group: 'Bot', label: 'From a template', hint: 'Working starters in seven languages', icon: 'layers', href: '/bots/new?source=template', words: 'bot starter discord.js discord.py' },
			{ key: 'bot-empty', action: 'New empty bot', group: 'Bot', label: 'Empty bot', hint: 'Choose a language, then upload a ZIP or edit in Files', icon: 'file', href: '/bots/new?source=blank', words: 'bot app worker blank language archive upload zip files' }
		);
		if (session.features.games)
			out.push(
				{ key: 'game-minecraft', group: 'Game server', label: 'Minecraft server', hint: 'Paper, Fabric, Forge, Vanilla and more', icon: 'cube', href: '/servers/new?family=minecraft', words: 'game server minecraft paper purpur fabric forge neoforge folia velocity vanilla' },
				{ key: 'game-steam', group: 'Game server', label: 'Steam game server', hint: 'Valheim, Rust, Project Zomboid', icon: 'gamepad', href: '/servers/new?family=steam', words: 'game server steam valheim rust zomboid steamcmd' },
				{ key: 'game-any', group: 'Game server', label: 'Browse all server types', hint: 'Every enabled game', icon: 'grid', href: '/servers/new', words: 'game server new' }
			);
	}
	if (session.features.containers && can('bots.create'))
		out.push(
			{ key: 'app-template', action: 'Install a one-click app', group: 'Container', label: 'One-click app', hint: 'Ghost, Vaultwarden, n8n, Gitea and more', icon: 'grid', href: '/apps?kind=container#catalog', words: 'one-click app template catalog self-hosted install ghost memos flarum vaultwarden n8n gitea uptime kuma umami miniflux wiki vikunja mealie ntfy searxng navidrome herdr zeroclaw' },
			{ key: 'container', action: 'New container from an image', group: 'Container', label: 'Docker image', hint: 'Run an image from Docker Hub, GHCR or Quay', icon: 'box', href: '/containers/new', words: 'container docker image run nginx redis postgres ghcr quay' },
			{ key: 'stack', action: 'Import a Compose file', group: 'Container', label: 'Compose stack', hint: 'Import docker-compose.yml as services', icon: 'layers', href: '/stacks/new', words: 'compose docker-compose stack services yaml import' }
		);
	if (session.features.sites && can('sites.create'))
		out.push(
			{ key: 'site', action: 'New website', group: 'Website', label: 'Upload files', hint: 'Static files from a ZIP or the editor', icon: 'globe', href: '/apps?kind=web&new=1', words: 'site static website host page upload zip' },
			...(session.features.site_builds
				? [{ key: 'site-github', action: 'New website from GitHub', group: 'Website', label: 'From GitHub', hint: 'Static files, a build or a running web app', icon: 'github' as IconName, href: '/sites/new', words: 'site website github build vite astro next nuxt app server node php laravel python go rust' }]
				: []),
			{ key: 'site-template', action: 'New website from a template', group: 'Website', label: 'From a template', hint: 'Landing page, docs, portfolio and more', icon: 'layers', href: '/templates?tab=sites', words: 'site template starter landing docs portfolio coming soon game community static website' }
		);
	if (session.features.workspaces && can('workspaces.create'))
		out.push({ key: 'workspace', group: 'More', paletteOnly: true, label: 'Workspace', hint: 'Share apps with a team', icon: 'building', href: '/settings/workspaces?new=1', words: 'team workspace members' });
	if (can('tickets.create'))
		out.push({ key: 'ticket', group: 'More', paletteOnly: true, label: 'Support ticket', hint: 'Ask the panel staff for help', icon: 'lifebuoy', href: '/support?new=1', words: 'help support ticket question' });
	if (can('api_keys.manage'))
		out.push({ key: 'api-client', action: 'New API client or key', group: 'More', paletteOnly: true, label: 'API client or key', hint: 'Tokens for scripts, CI and SFTP', icon: 'key', href: '/settings/sftp', words: 'api client token automation sftp key' });
	const admin = session.user.role === 'admin';
	if (admin || can('users.manage')) out.push({ key: 'a-invite', action: 'Invite a user', group: 'Administration', paletteOnly: true, label: 'Invite a user', hint: 'Create an account or invitation', icon: 'users', href: '/admin/users?invite=1', words: 'invite user account' });
	if (session.features.agents && (admin || can('nodes.manage'))) out.push({ key: 'a-node', action: 'Add a node', group: 'Administration', paletteOnly: true, label: 'Node', hint: 'Enroll a remote Docker host', icon: 'monitor', href: '/admin/nodes', words: 'node agent remote host enroll' });
	if (session.features.games && (admin || can('blueprints.manage'))) out.push({ key: 'a-egg', action: 'Import a server type', group: 'Administration', paletteOnly: true, label: 'Server type', hint: 'Import a Pterodactyl egg', icon: 'cube', href: '/admin/blueprints', words: 'blueprint egg import server type' });
	if (admin || can('kb.manage')) out.push({ key: 'a-kb', action: 'New help article', group: 'Administration', paletteOnly: true, label: 'Help article', hint: 'Write for the help center', icon: 'book', href: '/admin/kb', words: 'knowledgebase article help' });
	if (admin || can('status.manage')) out.push({ key: 'a-incident', action: 'Post a status incident', group: 'Administration', paletteOnly: true, label: 'Status incident', hint: 'Post to the status page', icon: 'activity', href: '/admin/status', words: 'status incident maintenance outage' });
	if (admin || can('mail.announce')) out.push({ key: 'a-announce', action: 'Send an announcement', group: 'Administration', paletteOnly: true, label: 'Announcement', hint: 'Notify or email accounts', icon: 'send', href: '/admin/mail', words: 'announcement news email notify' });
	return out;
}

/** The options grouped in display order (the New menu leaves out palette-only chores). */
export function groupedCreateOptions(menu = false): [string, CreateOption[]][] {
	const groups = new Map<string, CreateOption[]>();
	for (const o of createOptions()) {
		if (menu && o.paletteOnly) continue;
		if (!groups.has(o.group)) groups.set(o.group, []);
		groups.get(o.group)!.push(o);
	}
	return [...groups];
}
