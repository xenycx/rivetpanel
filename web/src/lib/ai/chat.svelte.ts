import { goto, invalidateAll } from '$app/navigation';
import { api, ApiError } from '$lib/api/client';
import { confirmDialog } from '$lib/ui/dialogs.svelte';
import { toast } from '$lib/ui/toast.svelte';
import { accents, radii, setAccent, setRadius, setTheme, type Accent, type Radius } from '$lib/ui/theme.svelte';
import { describeView, type ViewContext } from './context.svelte';

export type Provider = { id: string; name: string; default_model: string; default: boolean; pricing_configured: boolean };
export type Conversation = { id: string; title: string; provider_id: string | null; model: string | null; updated_at_ms: number; bot_id?: string | null; site_id?: string | null };
export type MessageContext = { kind?: string; label?: string; section?: string; detail?: string; path?: string };
export type Message = { id: string; role: 'user' | 'assistant'; content: string; created_at_ms: number; context?: MessageContext };
export type Step = {
	id: string;
	name: string;
	args?: string;
	output?: string;
	status: string;
	approval?: boolean;
	secure?: boolean;
	names?: string[];
	title?: string;
	exit?: number;
	duration?: number;
	detail?: any;
};
export type Change = { id: string; run_id?: string; status: string; summary: string; files: { path: string; operation: string; diff: string }[] };
export type RunBlock = {
	id: string;
	status: string;
	mode: string;
	created_at_ms: number;
	input_tokens: number;
	output_tokens: number;
	error_message?: string | null;
	steps: Step[];
	changes: Change[];
	citations: any[];
	streaming: string;
	/** The model's latest progress note, shown while it works. */
	note?: string;
	focus?: string;
};
type ToolCall = { id: string; name: string; arguments_json: string; output: string; approval_state: string; status: string; exit_code: number | null; duration_ms: number | null };
type RunDto = { id: string; status: string; mode: string; input_tokens: number; output_tokens: number; error_message?: string | null; created_at_ms: number; tool_calls?: ToolCall[]; change_sets?: Change[] };
type Event = { sequence: number; type: string; data?: any };

const ACTIVE = ['queued', 'running', 'waiting_approval'];
const approvalTitles: Record<string, string> = {
	request_environment_values: 'Secure environment input',
	run_diagnostic: 'Run isolated diagnostic',
	propose_file_change: 'Apply reviewed file change',
	restart_bot: 'Restart bot',
	deploy_github: 'Deploy from GitHub',
	deploy_site: 'Publish a site',
	deploy_image: 'Run a Docker image',
	deploy_compose: 'Import a compose stack',
	install_app_template: 'Install an app',
	set_environment: 'Set environment variables',
	delete_environment: 'Remove environment variables',
	power: 'Change power state',
	update_bot_settings: 'Change settings',
	redeploy: 'Redeploy',
	delete_bot: 'Delete permanently',
	console_command: 'Send a console command',
	panel_api: 'Change something in the panel'
};
/** Tools that only read: a run made of these leaves nothing to refresh. */
const READ_ONLY = new Set(['list_targets', 'focus_target', 'target_status', 'read_logs', 'build_output', 'list_files', 'read_file', 'search_files', 'environment_names', 'web_search', 'web_fetch', 'analyze_repo', 'list_app_templates', 'wait_for_ready', 'panel_routes', 'client_action']);
const MODE_KEY = 'rivetpanel.aiMode';
function readMode(): 'approval' | 'auto' {
	try {
		return localStorage.getItem(MODE_KEY) === 'auto' ? 'auto' : 'approval';
	} catch {
		return 'approval';
	}
}
const why = (e: unknown) => (e instanceof ApiError ? e.message : 'The assistant could not complete that request.');

function toStep(c: ToolCall, live: boolean): Step {
	const pending = live && c.approval_state === 'pending';
	const secureCall = c.name === 'request_environment_values';
	let names: string[] | undefined;
	if (secureCall) {
		try {
			names = JSON.parse(c.arguments_json).names;
		} catch {
			names = [];
		}
	}
	return {
		id: c.id,
		name: c.name,
		args: c.arguments_json,
		output: c.output || undefined,
		status: pending ? 'waiting' : c.status,
		approval: pending && !secureCall,
		secure: pending && secureCall,
		names,
		title: pending ? approvalTitles[c.name] : undefined,
		exit: c.exit_code ?? undefined,
		duration: c.duration_ms ?? undefined,
		// The Auto envelope is a tool call too; its arguments hold the limits.
		detail: c.name === 'auto_repair_envelope' ? safeJSON(c.arguments_json) : undefined
	};
}
function safeJSON(s?: string) {
	try {
		return s ? JSON.parse(s) : undefined;
	} catch {
		return undefined;
	}
}
function toBlock(r: RunDto): RunBlock {
	const live = ACTIVE.includes(r.status);
	return {
		id: r.id,
		status: r.status,
		mode: r.mode,
		created_at_ms: r.created_at_ms,
		input_tokens: r.input_tokens,
		output_tokens: r.output_tokens,
		error_message: r.error_message,
		steps: (r.tool_calls ?? []).map((c) => toStep(c, live)),
		changes: [...(r.change_sets ?? [])].reverse(),
		citations: [],
		streaming: ''
	};
}
const val = (v: any, low: string, high: string) => v?.[low] ?? v?.[high];

class Chat {
	open = $state(false);
	wide = $state(false);
	ready = $state(false);
	providers = $state<Provider[]>([]);
	conversations = $state<Conversation[]>([]);
	selected = $state<Conversation | null>(null);
	messages = $state<Message[]>([]);
	runs = $state<RunBlock[]>([]);
	prompt = $state('');
	#mode = $state<'approval' | 'auto'>(typeof localStorage === 'undefined' ? 'approval' : readMode());
	/** Ask first, or Autopilot. Remembered in this browser. */
	get mode() {
		return this.#mode;
	}
	set mode(m: 'approval' | 'auto') {
		this.#mode = m;
		try {
			localStorage.setItem(MODE_KEY, m);
		} catch {
			/* not remembered; still used for this page */
		}
	}
	error = $state('');
	busy = $state(false);
	secureValues = $state<Record<string, Record<string, string>>>({});
	/** Set by the chat window: the context of wherever the person is now. */
	currentView: () => ViewContext | null = () => null;

	private ws: WebSocket | null = null;
	private lastSequence = 0;

	get latest(): RunBlock | undefined {
		return this.runs[this.runs.length - 1];
	}
	get active(): boolean {
		return !!this.latest && ACTIVE.includes(this.latest.status);
	}
	get provider(): Provider | undefined {
		return this.providers.find((p) => p.id === this.selected?.provider_id) ?? this.providers.find((p) => p.default) ?? this.providers[0];
	}

	private starting: Promise<void> = Promise.resolve();
	show() {
		this.open = true;
		if (!this.ready) this.starting = this.init();
	}
	hide() {
		this.open = false;
	}
	toggle() {
		if (this.open) this.hide();
		else this.show();
	}
	/** Opens the chat with a question typed in, optionally sent right away. */
	async ask(text: string, send = false) {
		this.show();
		this.prompt = text;
		// Sending waits for the chat list: picking up an older chat must not
		// replace the one this message starts.
		if (send) {
			await this.starting;
			await this.send();
		}
	}

	async init() {
		this.ready = true;
		try {
			this.providers = (await api<{ providers: Provider[] }>('GET', '/ai/providers')).providers;
			this.conversations = (await api<{ conversations: Conversation[] }>('GET', '/ai/conversations')).conversations;
			// Pick up where the person left off if that was recent.
			const last = this.conversations[0];
			if (last && !this.selected && Date.now() - last.updated_at_ms < 6 * 3600_000) await this.openConversation(last);
		} catch (e) {
			this.ready = false;
			this.error = why(e);
		}
	}

	/** Forgets everything, for when another person signs in. */
	reset() {
		this.close();
		this.open = false;
		this.ready = false;
		this.providers = [];
		this.conversations = [];
		this.selected = null;
		this.messages = [];
		this.runs = [];
		this.prompt = '';
		this.error = '';
		this.secureValues = {};
	}

	newChat() {
		this.close();
		this.selected = null;
		this.messages = [];
		this.runs = [];
		this.error = '';
	}
	private close() {
		this.ws?.close();
		this.ws = null;
		this.lastSequence = 0;
	}

	async openConversation(c: Conversation) {
		this.close();
		this.selected = c;
		this.messages = [];
		this.runs = [];
		this.error = '';
		try {
			const d = await api<{ conversation: Conversation; messages: Message[] }>('GET', `/ai/conversations/${c.id}`);
			if (this.selected?.id !== c.id) return;
			this.selected = d.conversation;
			this.messages = d.messages;
		} catch (e) {
			this.error = why(e);
			return;
		}
		const latest = await this.restore(c.id);
		// An active run resumes its stream; replayed events update the restored
		// cards in place.
		if (latest && ACTIVE.includes(latest.status)) this.connect(latest.id);
	}

	/** Rebuilds activity from the server so approvals and Undo survive reloads. */
	private async restore(conversationId: string): Promise<RunBlock | null> {
		try {
			const runs = (await api<{ runs: RunDto[] }>('GET', `/ai/conversations/${conversationId}/runs`)).runs;
			if (this.selected?.id !== conversationId) return null;
			this.runs = runs.map(toBlock).reverse();
			return this.latest ?? null;
		} catch (e) {
			this.error = why(e);
			return null;
		}
	}

	async removeConversation(c: Conversation) {
		const ok = await confirmDialog({ title: 'Delete this chat?', body: `“${c.title}” and its retained messages are removed. File changes already applied stay in place.`, confirmLabel: 'Delete', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/ai/conversations/${c.id}`);
			this.conversations = this.conversations.filter((x) => x.id !== c.id);
			if (this.selected?.id === c.id) this.newChat();
		} catch (e) {
			this.error = why(e);
		}
	}

	async selectProvider(id: string) {
		const p = this.providers.find((x) => x.id === id);
		if (!p || !this.selected) return;
		try {
			this.selected = await api<Conversation>('PATCH', `/ai/conversations/${this.selected.id}`, { provider_id: id, model: p.default_model });
		} catch (e) {
			this.error = why(e);
		}
	}
	async saveModel(model: string) {
		if (!this.selected || !model.trim()) return;
		try {
			this.selected = await api<Conversation>('PATCH', `/ai/conversations/${this.selected.id}`, { model: model.trim() });
		} catch (e) {
			this.error = why(e);
		}
	}

	async send(view: ViewContext | null = this.currentView()) {
		const content = this.prompt.trim();
		if (!content || this.active || this.busy) return;
		const mode = this.mode;
		this.busy = true;
		this.error = '';
		try {
			if (!this.selected) {
				const c = await api<Conversation>('POST', '/ai/conversations', {});
				this.conversations = [c, ...this.conversations];
				this.selected = c;
			}
			const conv = this.selected;
			const run = await api<RunDto>('POST', `/ai/conversations/${conv.id}/messages`, { content, mode, context: view });
			this.prompt = '';
			this.messages = [
				...this.messages,
				{ id: crypto.randomUUID(), role: 'user', content, created_at_ms: run.created_at_ms, context: view ? { kind: view.kind, label: view.label, section: view.section, detail: view.detail, path: view.path } : undefined }
			];
			this.runs = [...this.runs, toBlock(run)];
			this.lastSequence = 0;
			this.connect(run.id);
		} catch (e) {
			this.error = why(e);
		} finally {
			this.busy = false;
		}
	}

	private connect(id: string) {
		this.ws?.close();
		const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
		const sock = new WebSocket(`${scheme}//${location.host}/api/v1/ai/runs/${id}/stream?after=${this.lastSequence}`);
		this.ws = sock;
		sock.onmessage = (m) => {
			const e = JSON.parse(m.data) as Event;
			this.lastSequence = Math.max(this.lastSequence, e.sequence);
			void this.consume(id, e);
		};
		sock.onclose = () => {
			if (this.ws === sock && this.latest?.id === id && ACTIVE.includes(this.latest.status)) setTimeout(() => this.ws === sock && this.connect(id), 1200);
		};
	}

	private patch(id: string, fn: (b: RunBlock) => RunBlock) {
		this.runs = this.runs.map((b) => (b.id === id ? fn(b) : b));
	}
	private upsertStep(runId: string, s: Partial<Step> & { id: string }) {
		this.patch(runId, (b) => {
			const i = b.steps.findIndex((t) => t.id === s.id);
			const steps = i < 0 ? [...b.steps, { name: '', status: 'running', ...s } as Step] : b.steps.map((t, n) => (n === i ? { ...t, ...s } : t));
			return { ...b, steps };
		});
	}

	private async consume(runId: string, e: Event) {
		const d = e.data ?? {};
		switch (e.type) {
			case 'delta':
				this.patch(runId, (b) => ({ ...b, streaming: b.streaming + (d.text ?? '') }));
				break;
			case 'start':
				this.patch(runId, (b) => ({ ...b, status: 'running' }));
				break;
			case 'tool_proposed':
				// Prose before a tool call is a progress note, not the answer.
				this.patch(runId, (b) => ({ ...b, note: b.streaming.trim() || b.note, streaming: '' }));
				this.upsertStep(runId, { id: val(d, 'id', 'ID'), name: val(d, 'name', 'Name'), args: val(d, 'arguments_json', 'ArgumentsJSON'), status: 'running' });
				break;
			case 'tool_output':
				this.upsertStep(runId, { id: d.id, name: d.name, output: d.output, status: d.status, exit: d.exit_code ?? undefined, duration: d.duration_ms ?? undefined });
				break;
			case 'approval_required': {
				const c = d.tool_call ?? {};
				this.upsertStep(runId, { id: val(c, 'id', 'ID'), name: val(c, 'name', 'Name'), args: val(c, 'arguments_json', 'ArgumentsJSON'), status: 'waiting', approval: !d.secure_input, secure: !!d.secure_input, names: d.names, title: d.title, detail: d.detail });
				this.patch(runId, (b) => ({ ...b, status: 'waiting_approval' }));
				break;
			}
			case 'status':
				if (d.tool_call_id) {
					this.upsertStep(runId, { id: d.tool_call_id, approval: false, secure: false, ...(d.approval ? { status: d.approval } : d.secure_names_configured ? { status: 'configured' } : {}) });
					this.patch(runId, (b) => (b.status === 'waiting_approval' ? { ...b, status: 'running' } : b));
				}
				break;
			case 'focus':
				this.patch(runId, (b) => ({ ...b, focus: d.name }));
				break;
			case 'client_action':
				this.clientAction(d.action, d.value);
				break;
			case 'change_set':
				this.patch(runId, (b) => {
					const i = b.changes.findIndex((x) => x.id === d.id);
					return { ...b, changes: i < 0 ? [d as Change, ...b.changes] : b.changes.map((x, n) => (n === i ? { ...x, ...d } : x)) };
				});
				break;
			case 'citation':
				this.patch(runId, (b) => {
					const add = (Array.isArray(d) ? d : [d]).filter((c: any) => !b.citations.some((x) => (x.URL ?? x.url) === (c.URL ?? c.url)));
					return { ...b, citations: [...b.citations, ...add] };
				});
				break;
			case 'usage':
				this.patch(runId, (b) => ({ ...b, input_tokens: d.input_tokens, output_tokens: d.output_tokens }));
				break;
			case 'done':
			case 'error':
			case 'cancelled':
				await this.finished(runId);
				break;
		}
	}

	/** Runs something the assistant asked for in this browser: the look of the panel, or a page to open. */
	private clientAction(action: string, value: string) {
		switch (action) {
			case 'set_theme':
				if (value === 'light' || value === 'dark' || value === 'system') setTheme(value);
				break;
			case 'set_accent':
				if (accents.some((a) => a.id === value)) setAccent(value as Accent);
				break;
			case 'set_radius':
				if (radii.some((r) => r.id === value)) setRadius(value as Radius);
				break;
			case 'navigate':
				if (value.startsWith('/') && !value.startsWith('//')) void goto(value);
				break;
		}
	}

	/** Reloads messages and activity from the server so Undo and the final answer are exact. */
	private async finished(runId: string) {
		const c = this.selected;
		if (!c || this.latest?.id !== runId) return;
		// Whatever the run created or changed shows up on the page behind the chat.
		if (this.latest.steps.some((s) => !READ_ONLY.has(s.name) && s.status === 'completed')) void invalidateAll();
		this.close();
		try {
			const d = await api<{ conversation: Conversation; messages: Message[] }>('GET', `/ai/conversations/${c.id}`);
			if (this.selected?.id === c.id) {
				this.messages = d.messages;
				this.selected = d.conversation;
				this.conversations = this.conversations.map((x) => (x.id === c.id ? d.conversation : x)).sort((a, b) => b.updated_at_ms - a.updated_at_ms);
			}
		} catch (e) {
			this.error = why(e);
		}
		await this.restore(c.id);
	}

	async decide(runId: string, s: Step, approve: boolean) {
		try {
			await api('POST', `/ai/tool-calls/${s.id}/decision`, { approve });
			this.upsertStep(runId, { id: s.id, approval: false, status: approve ? 'approved' : 'rejected' });
		} catch (e) {
			this.error = why(e);
		}
	}
	async secure(runId: string, s: Step) {
		try {
			await api('POST', `/ai/tool-calls/${s.id}/secure-input`, { values: this.secureValues[s.id] ?? {} });
			this.secureValues[s.id] = {};
			this.upsertStep(runId, { id: s.id, secure: false, status: 'configured' });
		} catch (e) {
			this.error = why(e);
		}
	}
	async cancel() {
		const r = this.latest;
		if (!r) return;
		try {
			await api('POST', `/ai/runs/${r.id}/cancel`);
			this.patch(r.id, (b) => ({ ...b, status: 'cancelled' }));
			await this.finished(r.id);
		} catch (e) {
			this.error = why(e);
		}
	}
	async undo(runId: string, id: string) {
		try {
			await api('POST', `/ai/change-sets/${id}/revert`);
			this.patch(runId, (b) => ({ ...b, changes: b.changes.map((x) => (x.id === id ? { ...x, status: 'reverted' } : x)) }));
			toast('Change reverted', 'success');
		} catch (e) {
			this.error = why(e);
		}
	}
}

export const chat = new Chat();
export { describeView };
