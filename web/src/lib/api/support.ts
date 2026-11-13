import { SvelteMap } from 'svelte/reactivity';

/** How an account appears on a ticket. */
export type Person = { id: string; name: string; email: string; avatar_url: string; logo_variant: number };

export type Ticket = {
	id: string;
	number: number;
	/** RT-000042 */
	key: string;
	subject: string;
	category: string;
	priority: string;
	status: string;
	bot_id: string | null;
	bot_name: string;
	created_at_ms: number;
	updated_at_ms: number;
	closed_at_ms: number | null;
	messages: number;
	requester_id?: string;
	requester_email?: string;
	assignee_id?: string | null;
	assignee_email?: string;
	requester?: Person | null;
	assignee?: Person | null;
	/** Staff view only: label ids. */
	labels?: string[];
	/** Staff view only: the reader follows the ticket. */
	watching?: boolean;
};

export type TicketMessage = {
	id: string;
	author: string;
	mine: boolean;
	kind: 'message' | 'event';
	staff: boolean;
	internal: boolean;
	body: string;
	created_at_ms: number;
	/** The author's picture and name; null for staff seen by a requester. */
	person?: Person | null;
	edited_at_ms?: number | null;
	deleted?: boolean;
	deleted_by?: string;
	mentions?: string[];
	reactions?: Reaction[];
	/** The automatic reply sent on the support team's behalf. */
	auto?: boolean;
	attachments?: Attachment[];
};

/** A file sent with a message; url downloads it (pictures open inline). */
export type Attachment = { id: string; name: string; content_type: string; size: number; image: boolean; url: string };

/** How the desk answers: the usual response time, upload limits and (managers) the automatic reply. */
export type DeskSettings = {
	auto_reply: boolean;
	auto_reply_body?: string;
	response_hours: number;
	/** "within 2 days" */
	response_time: string;
	max_files: number;
	max_file_bytes: number;
};

export type Reaction = { kind: ReactionKind; count: number; mine: boolean; names: string[] };
export type ReactionKind = 'ack' | 'thanks' | 'seen' | 'solved';
export const reactionKinds: { kind: ReactionKind; label: string; icon: 'check' | 'star' | 'eye' | 'sparkle' }[] = [
	{ kind: 'ack', label: 'Agree', icon: 'check' },
	{ kind: 'thanks', label: 'Thanks', icon: 'star' },
	{ kind: 'seen', label: 'Seen', icon: 'eye' },
	{ kind: 'solved', label: 'Solved it', icon: 'sparkle' }
];

export type TicketView = {
	ticket: Ticket;
	messages: TicketMessage[];
	staff: boolean;
	manage: boolean;
	requester: boolean;
	/** Staff view only. */
	watchers?: Person[];
};

export type Label = { id: string; name: string; color: LabelColor; tickets: number };
export type LabelColor = 'action' | 'data' | 'run' | 'warn' | 'fail' | 'violet' | 'cyan' | 'slate';
export const labelColors: { id: LabelColor; name: string }[] = [
	{ id: 'action', name: 'Accent' },
	{ id: 'data', name: 'Blue' },
	{ id: 'run', name: 'Green' },
	{ id: 'warn', name: 'Amber' },
	{ id: 'fail', name: 'Red' },
	{ id: 'violet', name: 'Violet' },
	{ id: 'cyan', name: 'Cyan' },
	{ id: 'slate', name: 'Slate' }
];
export type SavedReply = { id: string; title: string; body: string; updated_at_ms: number };
export type StaffMember = { id: string; email: string; display_name: string; avatar_url: string; logo_variant: number };

/** A ticket's reference: RT-000042. */
export const ticketKey = (t: Pick<Ticket, 'number'> & { key?: string }) => t.key || `RT-${String(t.number).padStart(6, '0')}`;

export const priorityRank: Record<string, number> = { urgent: 0, high: 1, normal: 2, low: 3 };
export const statusRank: Record<string, number> = { open: 0, pending: 1, resolved: 2, closed: 3 };

export const ticketCategories = [
	{ value: 'general', label: 'General question' },
	{ value: 'technical', label: 'Technical problem' },
	{ value: 'billing', label: 'Billing' },
	{ value: 'account', label: 'Account' },
	{ value: 'abuse', label: 'Report abuse' }
];
export const ticketPriorities = ['low', 'normal', 'high', 'urgent'];
/** Priority as a menu label ("Normal"); sentences keep the lowercase value ("normal priority"). */
export const priorityLabel = (p: string) => p.charAt(0).toUpperCase() + p.slice(1);
export const ticketStatuses = ['open', 'pending', 'resolved', 'closed'];

export const statusLabel: Record<string, string> = {
	open: 'Open',
	pending: 'Waiting for you',
	resolved: 'Resolved',
	closed: 'Closed'
};
export const staffStatusLabel: Record<string, string> = {
	open: 'Open',
	pending: 'Waiting for requester',
	resolved: 'Resolved',
	closed: 'Closed'
};
export const statusTone: Record<string, string> = {
	open: 'text-action border-action/30 bg-action/7',
	pending: 'text-warn border-warn/30 bg-warn/7',
	resolved: 'text-run border-run/30 bg-run/7',
	closed: 'text-muted border-rule bg-paper'
};
export const categoryLabel = (c: string) => ticketCategories.find((x) => x.value === c)?.label ?? c;

/** The keys 1–4 set these statuses, in this order (the triage desk and ticket page). */
export const statusKeys = ['open', 'pending', 'resolved', 'closed'];

/** Short status names for tight places (queue tabs, the status switch). */
export const staffStatusShort: Record<string, string> = {
	open: 'Open',
	pending: 'Waiting',
	resolved: 'Resolved',
	closed: 'Closed'
};

/**
 * How long an open ticket (waiting for staff) has had no activity, as a
 * tone: calm under four hours, warn under a day, fail after that. Other
 * statuses are not waiting for staff and stay calm. Activity is any change
 * to the thread, internal notes included.
 */
export function idleTone(t: Pick<Ticket, 'status' | 'updated_at_ms'>, now = Date.now()): 'calm' | 'warn' | 'fail' {
	if (t.status !== 'open') return 'calm';
	const h = (now - t.updated_at_ms) / 3_600_000;
	return h >= 24 ? 'fail' : h >= 4 ? 'warn' : 'calm';
}

/** Compact age: "now", "12m", "5h", "3d", then a date. */
export function fmtIdle(ms: number, now = Date.now()): string {
	const s = Math.max(0, Math.round((now - ms) / 1000));
	if (s < 60) return 'now';
	if (s < 3600) return `${Math.floor(s / 60)}m`;
	if (s < 86400) return `${Math.floor(s / 3600)}h`;
	if (s < 86400 * 30) return `${Math.floor(s / 86400)}d`;
	return new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/** When a ticket was opened, compact: "14:05" today, "Nov 5" this year, then "Nov 5, 2025". */
export function fmtOpened(ms: number, now = Date.now()): string {
	const d = new Date(ms);
	const n = new Date(now);
	if (d.toDateString() === n.toDateString()) return d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
	if (d.getFullYear() === n.getFullYear()) return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

/** True when a key event comes from a place that takes typing. */
export function typingIn(e: KeyboardEvent): boolean {
	const el = e.target;
	if (!(el instanceof HTMLElement)) return false;
	return el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName);
}

/** True when a single-key shortcut may run: no modifier, nothing typed into, no dialog open. */
export function shortcutAllowed(e: KeyboardEvent): boolean {
	return !e.defaultPrevented && !e.ctrlKey && !e.metaKey && !e.altKey && !typingIn(e) && !document.querySelector('dialog[open]');
}

/** Unsent replies by ticket id, kept while the panel stays open (switching tickets keeps them). */
export const drafts = new SvelteMap<string, { body: string; internal: boolean }>();

/** Tickets ordered by a table column. */
export function sortTickets(list: Ticket[], key: string, desc: boolean): Ticket[] {
	const name = (p?: Person | null) => (p?.name ?? '￿').toLowerCase();
	const val = (t: Ticket): string | number => {
		switch (key) {
			case 'key':
				return t.number;
			case 'subject':
				return t.subject.toLowerCase();
			case 'status':
				return statusRank[t.status] ?? 9;
			case 'priority':
				return priorityRank[t.priority] ?? 9;
			case 'requester':
				return name(t.requester) || (t.requester_email ?? '');
			case 'assignee':
				return name(t.assignee);
			case 'created':
				return t.created_at_ms;
			default:
				return t.updated_at_ms;
		}
	};
	return [...list].sort((a, b) => {
		const x = val(a);
		const y = val(b);
		const c = x < y ? -1 : x > y ? 1 : b.updated_at_ms - a.updated_at_ms;
		return desc ? -c : c;
	});
}
