// Plain-language descriptions of what an account invitation sets up, shared
// by the invitation dialog's result view and the pending invitations list.
import { fmtBytes, fmtCpu } from '$lib/api/client';
import { roleText, type AccountInvite, type WorkspaceQuota } from '$lib/api/types';

const counts: { key: 'max_bots' | 'max_games' | 'max_web_apps' | 'max_apps' | 'max_sites'; one: string; many: string }[] = [
	{ key: 'max_bots', one: 'bot', many: 'bots' },
	{ key: 'max_games', one: 'game server', many: 'game servers' },
	{ key: 'max_web_apps', one: 'web app', many: 'web apps' },
	{ key: 'max_apps', one: 'app in total', many: 'apps in total' },
	{ key: 'max_sites', one: 'site', many: 'sites' }
];

/** "1 bot · 2 sites · 1 GiB · 0.5 CPU"; "" when every limit is the panel default. */
export function limitsText(q: WorkspaceQuota | null | undefined): string {
	if (!q) return '';
	const parts: string[] = [];
	for (const c of counts) {
		const n = q[c.key];
		if (n == null) continue;
		parts.push(n === 0 ? `no ${c.many}` : `${n} ${n === 1 ? c.one : c.many}`);
	}
	if (q.memory_bytes != null) parts.push(`${fmtBytes(q.memory_bytes).replace('.0 ', ' ')} memory`);
	if (q.nano_cpus != null) parts.push(fmtCpu(q.nano_cpus));
	return parts.join(' · ');
}

/** The role an invitation gives, as shown to people. */
export const inviteRoleName = (v: Pick<AccountInvite, 'role' | 'role_name'>) => (v.role === 'admin' ? 'Administrator' : v.role_name || 'User');

/** "joins Team as Developer", or "" without a team workspace. */
export function workspaceText(v: Pick<AccountInvite, 'workspace_id' | 'workspace_name' | 'workspace_role'>): string {
	if (!v.workspace_id) return '';
	const role = v.workspace_role ? roleText[v.workspace_role] : 'Member';
	return `joins ${v.workspace_name || 'a team workspace'} as ${role}`;
}
