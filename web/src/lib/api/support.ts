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
