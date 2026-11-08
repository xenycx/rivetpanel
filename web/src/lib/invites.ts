// Plain-language descriptions of what an account invitation sets up, shared
// by the invitation dialog's result view and the pending invitations list.
import { fmtBytes, fmtCpu } from '$lib/api/client';
import { roleText, type AccountInvite, type WorkspaceQuota } from '$lib/api/types';
import { countFields } from '$lib/limits';

/** "3 apps in total · 1 bot or container · 2 sites · 1 GiB memory · 0.5 cores"; "" when every value inherits the default allowance. */
export function limitsText(q: WorkspaceQuota | null | undefined): string {
	if (!q) return '';
	const parts: string[] = [];
	for (const c of countFields) {
		const n = q[c.quota];
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
