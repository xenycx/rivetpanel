export type User = {
	id: string;
	email: string;
	display_name: string;
	avatar_url: string;
	role: 'admin' | 'user';
	disabled: boolean;
	created_at_ms: number;
	/** Custom role id; '' means the built-in role in `role`. */
	role_id: string;
	role_name: string;
	email_verified: boolean;
};

export type Role = { id: string; name: string; description: string; permissions: string[]; system: boolean; users: number; created_at_ms: number; updated_at_ms: number };
export type PermissionInfo = { name: string; group: 'resources' | 'administration'; label: string; description: string };
export type EmailVerification = { verified: boolean; withheld: string[]; available: boolean; pending_email: string; sent_at_ms: number };

export type ModuleState = 'off' | 'preview' | 'stable';
export type Module = { key: string; label: string; description: string; state: ModuleState };

export type Bot = {
	id: string;
	owner_id: string;
	workspace_id: string;
	node_id: string;
	name: string;
	runtime: string;
	image_ref: string;
	argv: string[];
	memory_bytes: number;
	nano_cpus: number;
	pids_limit: number;
	desired_state: 'stopped' | 'running' | 'deleted';
	observed_state: 'unknown' | 'stopped' | 'building' | 'starting' | 'running' | 'stopping' | 'failed';
	generation: number;
	observed_generation: number;
	last_exit_code: number | null;
	last_error: string | null;
	created_at_ms: number;
	updated_at_ms: number;
	discord_user_id: string;
	discord_username: string;
	discord_avatar_url: string;
	permissions: number;
	shared: boolean;
	entrypoint: string[];
	source_type: 'manual' | 'template' | 'github';
	template_id: string | null;
	network_enabled: boolean;
	bandwidth_kbps: number | null;
	ports: Port[];
	auto_backup: boolean;
	/** Custom logo, else the Discord avatar ("" = neither). */
	logo_url: string;
	custom_logo: boolean;
	/** Custom build script; "" = the runtime's default build. */
	build_command: string;
	/** Attached add-on kinds (bot detail responses). */
	addons?: string[];
	restart_policy: 'never' | 'on_failure';
	restart_max_attempts: number;
	restart_backoff_initial_ms: number;
	restart_backoff_max_ms: number;
	phase: Phase;
	state_reason: string | null;
	restart_count: number;
	next_retry_at_ms: number | null;
	last_started_at_ms: number | null;
	tags: string[];
	favorite: boolean;
	/** "bot" (an application such as a Discord bot) or "game" (a game server). */
	kind: 'bot' | 'game';
	blueprint_id?: string;
	blueprint_revision?: number;
	/** Chosen image label; "" = automatic (decided when installing). */
	image_choice?: string;
	install_state?: 'pending' | 'installing' | 'installed' | 'failed';
	allocations?: Allocation[];
};

export type Allocation = { id: string; node_id: string; ip: string; port: number; alias: string; notes: string; bot_id: string | null; primary: boolean };

/** Server-derived lifecycle word (internal/api phaseOf). */
export type Phase =
	| 'deleting'
	| 'checking'
	| 'no_runner'
	| 'runner_offline'
	| 'queued'
	| 'building'
	| 'installing'
	| 'starting'
	| 'running'
	| 'restarting'
	| 'retrying'
	| 'failed'
	| 'exited'
	| 'stopping'
	| 'stopped';

export type Port = { container_port: number; host_port: number; protocol: 'tcp' | 'udp'; host_ip: string };

/** Sub-user permission bits (mirror internal/domain). */
export const Perm = { console: 1, power: 2, files: 4, env: 8, admin: 16 } as const;
export const can = (b: { permissions: number }, p: number) => (b.permissions & Perm.admin) !== 0 || (b.permissions & p) === p;

export type RuntimeInfo = {
	id: string;
	display_name: string;
	image_ref: string;
	default_argv: string[];
	default_memory_bytes: number;
	default_nano_cpus: number;
	default_pids_limit: number;
	min_memory_bytes: number;
	build_memory_bytes: number;
	has_build: boolean;
};

export type Limits = {
	min_memory_bytes: number;
	max_memory_bytes: number;
	min_nano_cpus: number;
	max_nano_cpus: number;
	port_min: number;
	port_max: number;
	port_public_bind: boolean;
};

export type EnvVar = { name: string; value: string; updated_at_ms: number };
export type FileEntry = { name: string; type: 'file' | 'dir' | 'symlink'; size: number };

export type Sample = {
	sampled_at_ms: number;
	cpu_percent: number;
	logical_cpus: number;
	memory_used_bytes: number;
	memory_total_bytes: number;
	disk_used_bytes: number;
	disk_total_bytes: number;
	running_bots: number;
	load1: number;
	swap_used_bytes: number;
	swap_total_bytes: number;
	net_rx_bps: number;
	net_tx_bps: number;
	disk_read_bps: number;
	disk_write_bps: number;
};
export type AgentInfo = {
	connected: boolean;
	protocol_version: number;
	agent_version: string;
	hostname: string;
	capabilities: Record<string, unknown>;
	certificate_serial: string | null;
	certificate_expires_at_ms: number | null;
	connected_at_ms: number | null;
	disconnected_at_ms: number | null;
};
export type NodeInfo = {
	id: string;
	location_id: string;
	name: string;
	transport: 'local' | 'agent' | 'https';
	enabled: boolean;
	draining: boolean;
	public_address: string;
	server_count: number;
	last_seen_at_ms: number | null;
	agent?: AgentInfo;
	latest?: Sample;
};
export type LocationInfo = { id: string; name: string; description: string; node_count: number; created_at_ms: number; updated_at_ms: number };

export type Provider = 'github' | 'discord';
export type Connection = {
	provider: Provider;
	configured: boolean;
	linked: boolean;
	username: string;
	avatar_url: string;
	notifications: boolean;
	repo_access: boolean;
	can_disconnect: boolean;
};

/** Human text for the `?error=` codes the OAuth callback redirects with. */
export const oauthErrors: Record<string, string> = {
	denied: 'Sign-in was cancelled at the provider.',
	state_invalid: 'That sign-in link expired or was already used. Please try again.',
	exchange_failed: 'The provider rejected the sign-in. Please try again.',
	signup_disabled: 'No account is linked to that identity. Ask an administrator for an account, then connect it in Settings.',
	email_in_use: 'An account with that email already exists. Sign in with your password, then connect the provider in Settings.',
	email_unverified: 'The provider did not share a verified email address.',
	already_linked: 'That account is already connected to a different RivetPanel user.',
	account_disabled: 'This account is disabled.',
	server_error: 'Something went wrong on the server. Please try again.',
	token_invalid: 'The sign-in provider sent an identity token this panel could not verify. Please try again; if it keeps failing, tell an administrator.',
	link_required: 'An account with that email address already exists. Sign in to it another way, then connect the provider under Settings → Connected accounts.',
	email_missing: 'The sign-in provider did not share an email address, which a new account needs.'
};

/** A linked OpenID Connect identity (Settings → Connected accounts). */
export type Identity = { provider_id: string; slug: string; name: string; email: string; email_verified: boolean; created_at_ms: number; last_login_at_ms: number | null; can_unlink: boolean };
/** An OpenID Connect provider as administrators configure it. */
export type OidcProvider = {
	id: string;
	slug: string;
	name: string;
	issuer: string;
	client_id: string;
	secret_set: boolean;
	scopes: string;
	enabled: boolean;
	allow_signup: boolean;
	link_by_email: boolean;
	default_role_id: string;
	redirect_uri: string;
	created_at_ms: number;
	updated_at_ms: number;
};

export type TemplateEnv = { name: string; label: string; description: string; secret: boolean; required: boolean; default?: string };
export type Template = {
	id: string;
	name: string;
	description: string;
	runtime: string;
	language: string;
	version: number;
	tested_with: string;
	first_start: string;
	privileged_intents: string[];
	env: TemplateEnv[];
	setup: string[];
	default_memory_bytes: number;
	build_memory_bytes: number;
	has_build: boolean;
};
export type GitHubRepo = { full_name: string; private: boolean; default_branch: string };
export type RepoLink = {
	full_name: string;
	branch: string;
	root_dir: string;
	private: boolean;
	auto_deploy: boolean;
	hook_created: boolean;
	webhook_url: string;
	secret?: string;
	last_sha: string;
	last_deployed_at_ms: number;
	last_error: string;
	deploying: boolean;
	/** Auto-deploy checks the branch periodically (no webhook). */
	polling?: boolean;
	/** A push that waits for the server's offline node to reconnect. */
	pending_push_at_ms?: number;
};
export type Backup = {
	id: string;
	kind: 'manual' | 'auto' | 'pre_restore';
	status: 'creating' | 'ready' | 'failed';
	size_bytes: number;
	sha256: string | null;
	includes_env: boolean;
	error: string | null;
	created_at_ms: number;
	label: string | null;
	verified_at_ms: number | null;
	verify_error: string | null;
	consistent: boolean;
};
export type BackupHealth = {
	interval_ms: number;
	keep: number;
	enabled: boolean;
	last_success_ms: number;
	last_scheduled_ms: number;
	next_due_ms: number;
	last_failure: Backup | null;
	total_bytes: number;
	count: number;
	limit: number;
	manual_limit: number;
};
export type SubUser = { user_id: string; email: string; permissions: number; created_at_ms: number };
export type Dep = { name: string; spec: string; group: string; editable: boolean };
export type Packages = { supported: boolean; ecosystem?: string; file?: string; exists: boolean; groups?: string[]; deps: Dep[] };
export type PkgResult = { name: string; version: string; description: string };
export type Series = { name: string; latest: number; points: { t: number; v: number }[] };
export type WidgetKind = 'metric'|'status'|'progress'|'text'|'chart'|'table'|'link'|'line'|'area'|'donut'|'gauge'|'heatmap'|'sparkline'|'kv'|'markdown'|'image'|'log'|'code';
export type DashboardWidget = { key: string; kind: WidgetKind; title: string; group: string; span: 1|2|3; min_height: number; position: number; data: Record<string, unknown>; updated_at_ms: number; expires_at_ms: number|null; stale: boolean };
export type Analytics = {
	key_set: boolean;
	window_ms: number;
	last_at_ms: number;
	stats: Series[];
	commands: { name: string; count: number }[];
	events: { t: number; name: string; data?: unknown }[];
	widgets: DashboardWidget[];
};
export type Gauge = {
	running: boolean;
	cpu_cores: number;
	cpu_limit_cores: number;
	cpu_percent: number;
	mem_used_bytes: number;
	mem_limit_bytes: number;
	pids: number;
	net_rx_bytes: number;
	net_tx_bytes: number;
	disk_used_bytes: number;
	disk_total_bytes: number;
	disk_free_bytes: number;
	/** 'node': a remote server; only its node's volume is known. */
	disk_scope?: 'workspace' | 'node';
};
export type ApiKey = { id: string; name: string; prefix: string; created_at_ms: number; last_used_at_ms: number | null; expires_at_ms: number | null };

export type OpKind = 'build' | 'deploy' | 'rollback' | 'backup' | 'restore' | 'publish';
export type OpStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'interrupted';
export type Operation = {
	id: string;
	bot_id: string;
	bot_name: string;
	kind: OpKind;
	trigger: 'manual' | 'push' | 'schedule' | 'initial' | 'start' | 'api' | 'system';
	actor: string | null;
	status: OpStatus;
	stage: string;
	source_ref: string | null;
	source_label: string | null;
	generation: number | null;
	result_code: string | null;
	message: string | null;
	detail?: Record<string, unknown>;
	log_bytes: number;
	created_at_ms: number;
	started_at_ms: number | null;
	finished_at_ms: number | null;
};
export type OpPage = { operations: Operation[]; next_before?: number };
export type OutputChunk = { text: string; next_offset: number; total: number; truncated: boolean; live: boolean };

export type ScheduleAction = 'backup' | 'start' | 'stop' | 'restart' | 'deploy' | 'chain';
export type TaskAction = 'command' | 'start' | 'stop' | 'restart' | 'kill' | 'backup';
export type ScheduleTask = { action: TaskAction; payload: string; delay_seconds: number; continue_on_failure: boolean };
export type Schedule = {
	id: string;
	action: ScheduleAction;
	spec: string;
	timezone: string;
	enabled: boolean;
	owner_email: string;
	next_run_at_ms: number | null;
	last_run_at_ms: number | null;
	last_status: 'ok' | 'failed' | 'skipped' | 'missed' | 'denied' | null;
	last_message: string | null;
	upcoming: number[];
	can_edit: boolean;
	created_at_ms: number;
	tasks: ScheduleTask[];
};

export type Capacity = {
	bots: number;
	max_bots: number;
	memory_bytes: number;
	max_memory_bytes: number;
	exempt: boolean;
	build_memory_bytes: number;
	node?: { running: number; reserved_bytes: number; budget_bytes: number };
};

export type WorkspaceRole = 'owner' | 'admin' | 'developer' | 'viewer';
export type Workspace = {
	id: string;
	name: string;
	owner_id: string;
	owner_email: string;
	personal: boolean;
	role: WorkspaceRole | '';
	members: number;
	bots: number;
	running_bots: number;
	memory_bytes: number;
	sites: number;
	last_active_at_ms: number;
	created_at_ms: number;
};
export type WorkspaceMember = { user_id: string; email: string; display_name: string; role: WorkspaceRole; created_at_ms: number };
export const roleRank: Record<WorkspaceRole | '', number> = { '': 0, viewer: 1, developer: 2, admin: 3, owner: 4 };
export const roleText: Record<WorkspaceRole, string> = {
	owner: 'Owner',
	admin: 'Admin',
	developer: 'Developer',
	viewer: 'Viewer'
};
export const roleHelp: Record<WorkspaceRole, string> = {
	owner: 'Everything, including deleting the workspace',
	admin: 'Manage members and every bot and site',
	developer: 'Create, deploy and operate bots and sites; no deleting or resource changes',
	viewer: 'Read-only: status and console output'
};

export type Site = {
	id: string;
	workspace_id: string;
	workspace_name: string;
	owner_id: string;
	owner_email: string;
	bot_id: string | null;
	name: string;
	slug: string;
	domain_id: string | null;
	base_domain: string;
	url: string;
	spa: boolean;
	clean_urls: boolean;
	mode: 'page' | 'files';
	page_title: string;
	page_description: string;
	page_theme: 'midnight' | 'daylight' | 'system';
	page_accent: string;
	page_html: string;
	page_css: string;
	widgets_public: boolean;
	current_release: string | null;
	release_bytes: number;
	disabled: boolean;
	domains: number;
	repo_full_name: string | null;
	repo_branch: string | null;
	repo_root: string;
	created_at_ms: number;
	updated_at_ms: number;
	/** Custom logo, release favicon or bot logo; 404 when none. */
	icon_url: string;
	custom_logo: boolean;
};
export type SiteDomain = {
	domain: string;
	url: string;
	verified: boolean;
	verified_at_ms: number | null;
	last_checked_at_ms: number | null;
	last_error: string | null;
	txt_name: string;
	txt_value: string;
	record_type: 'CNAME' | 'A' | 'AAAA';
	record_target: string;
};
export type SiteRelease = {
	id: string;
	source: 'upload' | 'github';
	source_label: string | null;
	files: number;
	bytes: number;
	actor: string | null;
	current: boolean;
	created_at_ms: number;
};
/** A domain sites can be placed under: a site's address is <slug>.<domain>. */
export type SiteBaseChoice = { id: string; domain: string; label: string; primary: boolean; example_url: string };
export type SitesInfo = {
	enabled: boolean;
	domain?: string;
	example_url?: string;
	domains?: SiteBaseChoice[];
	max_bytes?: number;
	max_domains?: number;
};
/** A sites domain as administrators manage it. */
export type SiteBaseDomain = {
	id: string;
	domain: string;
	label: string;
	enabled: boolean;
	primary: boolean;
	from_config: boolean;
	serving: boolean;
	verified: boolean;
	verified_at_ms: number | null;
	last_checked_at_ms: number | null;
	last_error: string | null;
	dns_target: string;
	sites: number;
	example_url: string;
	txt_name: string;
	txt_value: string;
	record_type: 'CNAME' | 'A' | 'AAAA';
	record_target: string;
	created_at_ms: number;
};

export type AddonKind = {
	id: string;
	display_name: string;
	description: string;
	image: string;
	port: number;
	variables: string[];
	default_memory_bytes: number;
	min_memory_bytes: number;
	password: boolean;
};
export type BotAddon = {
	kind: string;
	display_name: string;
	description: string;
	image: string;
	host: string;
	port: number;
	memory_bytes: number;
	variables: string[];
	data_bytes: number;
	created_at_ms: number;
	status: { state: string; health: string; exit_code: number };
};
export type PlanEnv = { name: string; description: string; required: boolean; secret: boolean; value: string };
export type Plan = {
	source: 'recipe' | 'detected' | 'ai';
	confidence: 'high' | 'medium' | 'low';
	summary: string;
	runtime: string;
	argv: string[] | null;
	build_command: string;
	env: PlanEnv[];
	addons: string[];
	memory_bytes: number;
	nano_cpus: number;
	pids_limit: number;
	ports: number[] | null;
	setup: string[] | null;
	notes: string[] | null;
	evidence: string[] | null;
};
export type RepoInfo = {
	full_name: string;
	private: boolean;
	default_branch: string;
	html_url: string;
	description?: string;
	language?: string;
	stargazers_count?: number;
	archived?: boolean;
	pushed_at?: string;
	license?: { spdx_id: string } | null;
};
export type RepoLookup = { repo: RepoInfo; branch: string; root_dir: string; branches: string[]; connected: boolean };
export type Analysis = {
	repo: RepoInfo;
	branch: string;
	root_dir: string;
	sha: string;
	plan: Plan;
	ai: { available: boolean; used: boolean; model?: string; error?: string };
	files: number;
	truncated: boolean;
};
export type Recipe = { repo: string; branch: string; name: string; description: string; language: string; plan: Plan };

/** A scoped application credential for the whole API (bearer "rvc_" token). */
export type ApiClient = {
	id: string;
	name: string;
	prefix: string;
	permissions: string[];
	bot_ids: string[] | null;
	workspace_ids: string[] | null;
	created_at_ms: number;
	last_used_at_ms: number | null;
	expires_at_ms: number | null;
	owner_id: string;
	owner_email: string;
};
