import { api } from '$lib/api/client';

export type Notification = {
	id: string;
	category: string;
	title: string;
	body: string;
	link: string;
	created_at_ms: number;
	read_at_ms: number | null;
};

/** The signed-in account's unread count, shared by the bell and the inbox page. */
export const inbox = $state({ unread: 0 });

export async function refreshUnread() {
	try {
		inbox.unread = (await api<{ unread: number }>('GET', '/notifications/unread')).unread;
	} catch {
		/* the badge is a convenience */
	}
}

export async function listNotifications(opts: { before?: number; unread?: boolean; limit?: number } = {}) {
	const q = new URLSearchParams();
	if (opts.before) q.set('before', String(opts.before));
	if (opts.unread) q.set('unread', '1');
	q.set('limit', String(opts.limit ?? 30));
	const r = await api<{ notifications: Notification[]; unread: number }>('GET', `/notifications?${q}`);
	inbox.unread = r.unread;
	return r.notifications;
}

export async function markRead(ids: string[] | 'all') {
	await api('POST', '/notifications/read', ids === 'all' ? { all: true } : { ids });
	await refreshUnread();
}

export async function deleteNotification(id: string) {
	await api('DELETE', `/notifications/${id}`);
	await refreshUnread();
}

export const categoryLabels: Record<string, string> = {
	bot_alerts: 'Alert',
	deploys: 'Deployment',
	backups: 'Backup',
	nodes: 'Node',
	access: 'Access',
	announcements: 'Announcement',
	tickets: 'Support'
};
