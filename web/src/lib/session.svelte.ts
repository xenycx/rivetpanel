import { goto } from '$app/navigation';
import { api, setCsrf, setUnauthorizedHandler } from '$lib/api/client';
import type { EmailVerification, User } from '$lib/api/types';

export type Features = { runner: boolean; console: boolean; stats: boolean; files: boolean; deploy: boolean; backups: boolean; analytics: boolean; sftp: boolean; operations: boolean; oauth: boolean; schedules: boolean; mfa: boolean; automation: boolean; health: boolean; sites: boolean; workspaces: boolean; ai: boolean; mail: boolean; public_repos: boolean; addons: boolean; games: boolean; agents: boolean; api_clients: boolean; passkeys: boolean; usage: boolean };
const allOn: Features = { runner: true, console: true, stats: true, files: true, deploy: true, backups: true, analytics: true, sftp: true, operations: true, oauth: true, schedules: true, mfa: true, automation: true, health: true, sites: false, workspaces: true, ai: true, mail: false, public_repos: true, addons: true, games: false, agents: false, api_clients: true, passkeys: true, usage: true };
export const session = $state<{
	user: User | null;
	loaded: boolean;
	hasPassword: boolean;
	emailAlerts: boolean;
	emailNews: boolean;
	features: Features;
	/** Effective role permissions; the server enforces them, the interface only follows. */
	permissions: string[];
	verification: EmailVerification;
}>({
	user: null,
	loaded: false,
	hasPassword: true,
	emailAlerts: true,
	emailNews: true,
	features: allOn,
	permissions: [],
	verification: { verified: true, withheld: [], available: false, pending_email: '', sent_at_ms: 0 }
});

/** Whether the signed-in account holds a permission (administrators hold all). */
export function can(p: string): boolean {
	return session.user?.role === 'admin' || session.permissions.includes(p);
}

/** Administration permissions, in the order of the administration sections. */
export const adminPermissions = ['users.view', 'users.manage', 'roles.manage', 'workspaces.view', 'sites.manage', 'blueprints.manage', 'allocations.manage', 'nodes.manage', 'system.view', 'settings.manage', 'mail.announce', 'ai.manage'];

/** Whether the account may open any part of the administration area. */
export function canAdminister(): boolean {
	return session.user?.role === 'admin' || adminPermissions.some((p) => session.permissions.includes(p));
}

setUnauthorizedHandler(() => {
	if (session.user) {
		session.user = null;
		goto('/login');
	}
});

export async function loadSession() {
	try {
		const r = await api<{ user: User; csrf_token: string; has_password: boolean; email_alerts: boolean; email_news: boolean; features: Features; permissions?: string[]; email_verification?: EmailVerification }>('GET', '/auth/me');
		setCsrf(r.csrf_token);
		session.user = r.user;
		session.hasPassword = r.has_password;
		session.emailAlerts = r.email_alerts !== false;
		session.emailNews = r.email_news !== false;
		session.features = { ...allOn, ...r.features };
		session.permissions = r.permissions ?? [];
		if (r.email_verification) session.verification = r.email_verification;
	} catch {
		session.user = null;
	} finally {
		session.loaded = true;
	}
}

/** Signs in with a password. Returns 'mfa' when a second step is needed. */
export async function login(email: string, password: string): Promise<'ok' | 'mfa'> {
	const r = await api<{ user?: User; csrf_token?: string; mfa_required?: boolean }>('POST', '/auth/login', { email, password });
	if (r.mfa_required) return 'mfa';
	await loadSession(); // picks up features and account details
	return 'ok';
}

/** Second step of a sign-in: an authenticator code or a recovery code. */
export async function verifyMFA(code: string) {
	await api('POST', '/auth/mfa', { code });
	await loadSession();
}

export async function logout() {
	try {
		await api('POST', '/auth/logout');
	} finally {
		session.user = null;
		setCsrf('');
		goto('/login');
	}
}

// Where to go after signing in (an invitation link, a deep link). Kept in
// sessionStorage so tokens in URL fragments never reach a server or a query.
const NEXT = 'rivetpanel.next';
export function rememberNext(path: string) {
	try {
		if (path.startsWith('/') && !path.startsWith('//') && !path.startsWith('/login')) sessionStorage.setItem(NEXT, path);
	} catch {
		/* storage unavailable: land on the home page */
	}
}
export function takeNext(): string {
	try {
		const p = sessionStorage.getItem(NEXT);
		sessionStorage.removeItem(NEXT);
		if (p && p.startsWith('/') && !p.startsWith('//')) return p;
	} catch {
		/* ignore */
	}
	return '/dashboard';
}
