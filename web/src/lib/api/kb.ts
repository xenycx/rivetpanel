// Knowledgebase (help center) and public status page types.

export type KBCategory = { id: string; slug: string; name: string; description: string; position: number };
export type KBArticle = {
	id: string;
	slug: string;
	title: string;
	summary: string;
	body?: string;
	category_id: string | null;
	status: 'draft' | 'published';
	visibility: 'public' | 'users' | 'staff';
	position: number;
	updated_at_ms: number;
	published_at_ms: number | null;
	updated_by?: string;
	excerpt?: string;
};
export type KBIndex = { categories: KBCategory[]; articles: KBArticle[]; public: boolean; manage: boolean };

export const visibilityLabel: Record<string, string> = {
	public: 'Public',
	users: 'Signed-in accounts',
	staff: 'Support staff only'
};

export type StatusDay = { date: string; state: string; uptime: number | null };
export type StatusComponent = {
	id: string;
	name: string;
	description: string;
	state: string;
	uptime: number | null;
	days: StatusDay[];
	position?: number;
	source?: { kind: string; id: string; name: string; missing: boolean };
};
export type StatusUpdate = { status: string; body: string; created_at_ms: number };
export type StatusIncident = {
	id: string;
	kind: 'incident' | 'maintenance';
	title: string;
	impact: string;
	status: string;
	active: boolean;
	starts_at_ms: number | null;
	ends_at_ms: number | null;
	created_at_ms: number;
	updated_at_ms: number;
	resolved_at_ms: number | null;
	components: { id: string; name: string }[];
	updates: StatusUpdate[];
};
export type StatusPage = {
	title: string;
	intro: string;
	overall: string;
	components: StatusComponent[];
	incidents: StatusIncident[];
	updated_at_ms: number;
	retention_days: number;
	enabled?: boolean;
};

export const stateLabel: Record<string, string> = {
	operational: 'Operational',
	degraded: 'Degraded performance',
	partial_outage: 'Partial outage',
	major_outage: 'Major outage',
	maintenance: 'Under maintenance',
	unknown: 'Status unknown',
	none: 'No data'
};
export const overallLabel: Record<string, string> = {
	operational: 'All systems operational',
	degraded: 'Some systems are degraded',
	partial_outage: 'Partial outage',
	major_outage: 'Major outage',
	maintenance: 'Maintenance in progress',
	unknown: 'Status unknown'
};
/** Tailwind classes for a state: dot/bar background and text colour. */
export const stateTone: Record<string, { bg: string; text: string }> = {
	operational: { bg: 'bg-run', text: 'text-run' },
	degraded: { bg: 'bg-warn', text: 'text-warn' },
	partial_outage: { bg: 'bg-warn', text: 'text-warn' },
	major_outage: { bg: 'bg-fail', text: 'text-fail' },
	maintenance: { bg: 'bg-action', text: 'text-action' },
	unknown: { bg: 'bg-rule', text: 'text-muted' },
	none: { bg: 'bg-rule-soft', text: 'text-muted' }
};
export const incidentStatuses = ['investigating', 'identified', 'monitoring', 'resolved'];
export const maintenanceStatuses = ['scheduled', 'in_progress', 'completed'];
export const impacts = ['none', 'minor', 'major', 'critical'];
export const words = (s: string) => s.replaceAll('_', ' ');
