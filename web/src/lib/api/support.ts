import { SvelteMap } from 'svelte/reactivity';

export type Ticket = {
	id: string;
	number: number;
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
};

export type TicketView = { ticket: Ticket; messages: TicketMessage[]; staff: boolean; manage: boolean; requester: boolean };

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
