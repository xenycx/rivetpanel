import type { Bot } from './types';

export type BlueprintImage = { label: string; ref: string; java?: number };
export type BlueprintVariable = {
	env: string;
	name: string;
	description?: string;
	default: string;
	editable: boolean;
	reinstall?: boolean;
	rules: { type?: 'string' | 'int' | 'bool' | 'enum'; pattern?: string; min?: number; max?: number; options?: string[]; max_len?: number };
	versions?: { provider: string; project?: string };
};
export type Agreement = { id: string; text: string; url?: string; file: string; content: string };
export type BlueprintSpec = {
	slug: string;
	name: string;
	category: string;
	description: string;
	runtime: string;
	images: BlueprintImage[];
	java_from?: string;
	startup: { command: string; stop?: string; stop_signal?: string; stop_timeout_seconds?: number; done?: string };
	variables: BlueprintVariable[];
	agreements: Agreement[];
	resources: { memory_mb: number; min_memory_mb: number; cpus: number; pids: number; heap_percent?: number };
	ports: { default?: number; extra?: number; container?: number; contiguous?: boolean };
	install?: { steamcmd?: { app_id: number; beta?: string; validate?: boolean } };
	query?: string;
	query_allocation?: number;
	features?: string[];
	addons?: { source: string; kind: 'plugin' | 'mod'; loaders: string[]; dir: string };
};
export type Blueprint = {
	id: string;
	slug: string;
	name: string;
	category: string;
	description: string;
	source: 'builtin' | 'custom';
	current_revision: number;
	enabled: boolean;
	servers: number;
	spec: BlueprintSpec;
};
export type GameVariable = BlueprintVariable & { value: string };
export type GameDetail = {
	blueprint: { id: string; slug: string; name: string; category: string; current_revision: number };
	spec: BlueprintSpec;
	variables: GameVariable[];
	update_available: boolean;
	startup: string;
	jvm: GameJVM;
};
export type JVMPreset = { id: string; name: string; args: string; min_java?: number; note?: string };
/** Extra JVM options of a Java server (SERVER_JVM_ARGS). */
export type GameJVM = {
	supported: boolean;
	args: string;
	default: string;
	/** Java major version of the server's image; 0 = decided at installation. */
	java: number;
	heap_mib: number;
	pending_restart: boolean;
	saved_generation: number;
	set: boolean;
	max_length: number;
	presets: JVMPreset[];
};

/** Quick client-side hints; the server validates every option strictly. */
export function checkJVMArgs(s: string, max: number): string {
	if (s.length > max) return `At most ${max} characters.`;
	for (const t of s.split(/\s+/).filter(Boolean)) {
		const bad = t.match(/[^A-Za-z0-9._:+=,/@%-]/);
		if (bad) return `${t} contains “${bad[0]}”. Only letters, digits and . _ : + = , / @ % - are allowed.`;
		if (!t.startsWith('-')) return `${t} is not a JVM option (options start with -).`;
		if (/^-Xm[xs]/.test(t)) return `${t}: the heap size is set by the panel from the memory limit. Change the memory instead.`;
		if (['-jar', '-cp', '-classpath', '--class-path', '-m', '--module', '-p', '--module-path'].includes(t))
			return `${t}: the server jar and class path are set by the server type.`;
	}
	return '';
}
export type GameStatus = {
	online: boolean;
	players: number;
	max_players: number;
	sample: string[];
	version?: string;
	motd?: string;
	map?: string;
	latency_ms: number;
};
/** A blueprint draft converted from a Pterodactyl egg, for review. */
export type EggDraft = { yaml: string; warnings: string[]; error?: string; format: string; name: string };
export type Version = { id: string; stable: boolean };

/** Where a bot or game server lives in the interface. */
export function resourceHref(b: Pick<Bot, 'id' | 'kind'>, tab?: string): string {
	const base = b.kind === 'game' ? `/servers/${b.id}` : `/bots/${b.id}`;
	return tab ? `${base}?tab=${tab}` : base;
}


/** A short client-side check mirroring the blueprint rules (the server re-checks). */
export function checkVariable(v: BlueprintVariable, value: string): string {
	const r = v.rules ?? {};
	if (value.length > (r.max_len || 256) || /[\n\r]/.test(value)) return 'Too long, or more than one line.';
	switch (r.type) {
		case 'int': {
			if (!/^-?\d+$/.test(value)) return 'Enter a whole number.';
			const n = Number(value);
			if (r.min !== undefined && n < r.min) return `At least ${r.min}.`;
			if (r.max !== undefined && n > r.max) return `At most ${r.max}.`;
			break;
		}
		case 'bool':
			if (value !== 'true' && value !== 'false') return 'Choose true or false.';
			break;
		case 'enum':
			if (!(r.options ?? []).includes(value)) return 'Choose one of the options.';
			break;
	}
	if (r.pattern) {
		try {
			if (!new RegExp(r.pattern).test(value)) return 'This format is not accepted.';
		} catch {
			/* the server validates */
		}
	}
	return '';
}
