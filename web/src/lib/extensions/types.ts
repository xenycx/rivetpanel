// Panel extensions: what the API returns (see internal/service/extensions.go).

export type ExtNavItem = { label: string; page: string; perm?: string };
export type ExtBotTab = { id: string; label: string; page: string; bot_perm?: string };
export type ExtBotCard = { id: string; title: string; route: string; path: string };
export type ExtConsentScope = { id: string; label: string; description: string };

export type ExtContribution = {
	id: string;
	name: string;
	publisher: string;
	version: string;
	source: 'builtin' | 'upload' | 'dev';
	nav: ExtNavItem[];
	bot_tabs: ExtBotTab[];
	bot_cards: ExtBotCard[];
	consent?: { explanation: string; scopes: ExtConsentScope[] };
	consented: string[];
	bot_enablement?: string;
};

/** A capability as the review screen shows it: what it allows, the part of
 * the panel it touches and the safeguard the panel enforces on every use. */
export type ExtCapabilityArea = 'bots' | 'people' | 'internet' | 'own';
export type ExtCapability = { name: string; label: string; area: ExtCapabilityArea; guard: string; reason: string; optional: boolean; hosts?: string[]; granted: boolean };
export type ExtSetting = {
	key: string;
	label: string;
	type: 'string' | 'number' | 'boolean' | 'enum' | 'secret';
	description?: string;
	required?: boolean;
	options?: string[];
	default?: unknown;
	inject_hosts?: string[];
	value?: unknown;
	set: boolean;
};
export type ExtVersion = {
	version: string;
	source: string;
	key_id?: string;
	publisher_key_name?: string;
	sha256: string;
	created_at_ms: number;
	active: boolean;
	compatible: boolean;
	new_capabilities?: string[];
};
export type ExtRoute = { id: string; method: string; path: string; auth: 'user' | 'bot' | 'public'; perm?: string; bot_perm?: string };
export type ExtJob = { job: string; next_run_ms: number; last_run_ms: number; last_status: string; last_error: string };

export type ExtensionView = {
	id: string;
	name: string;
	description: string;
	publisher: string;
	homepage?: string;
	version: string;
	source: 'builtin' | 'upload' | 'dev';
	installed: boolean;
	state: 'enabled' | 'disabled' | 'failed' | 'available';
	last_error?: string;
	capabilities: ExtCapability[];
	settings: ExtSetting[];
	versions: ExtVersion[];
	contributes: { nav?: ExtNavItem[]; bot_tabs?: ExtBotTab[]; bot_cards?: ExtBotCard[]; user_consent?: unknown; bot_enablement?: string };
	routes: ExtRoute[];
	jobs: ExtJob[];
	events: string[];
	bots: number;
	memory_mb: number;
	timeout_ms: number;
	update_version?: string;
};

export type ExtPublisher = { key_id: string; name: string; public_key: string; created_at_ms: number; revoked_at_ms?: number };
export type BotExtension = { id: string; name: string; explanation: string; enabled: boolean };

/** A widget in the docs/widgets.md format, as extension cards return them. */
export type ExtWidget = { key: string; kind: string; title: string; data: Record<string, unknown> };
