// A2UI v1.0 (https://a2ui.org) for the assistant's chat: the state of each
// surface the agent shows, its data model, and the renderer-side functions.
// The server checks every message against the RivetPanel catalog before the
// browser sees it; this side still refuses unsafe paths, links and images,
// and never runs anything the catalog does not name.
import { tick } from 'svelte';

export const VERSION = '1.0';
export const BASIC_CATALOG = 'https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json';
export const PANEL_CATALOG = 'urn:rivetpanel:a2ui:catalog:v1';
export const ACTION_PREFIX = '[A2UI action] ';

export type Component = { id: string; component: string; [prop: string]: any };
export type Action = { name: string; surfaceId: string; sourceComponentId: string; timestamp: string; context: Record<string, unknown>; userMessage?: string };
/** Where a component is drawn: its data scope (a template item) and index. */
export type Scope = { path: string; index: number | null };

export class Surface {
	readonly id: string;
	readonly catalogId: string;
	components = $state<Record<string, Component>>({});
	data = $state<Record<string, any>>({});
	/** The run that last changed it: the surface is drawn in that turn. */
	owner = $state('');
	/** Just created live: its parts cascade in once. */
	fresh = $state(false);
	constructor(id: string, catalogId: string) {
		this.id = id;
		this.catalogId = catalogId;
	}
}

// ---------------------------------------------------------------- pointers

const BLOCKED = new Set(['__proto__', 'prototype', 'constructor']);
function tokens(path: string): string[] | null {
	if (path === '' || path === '/') return [];
	const t = path.replace(/^\//, '').split('/').map((x) => x.replace(/~1/g, '/').replace(/~0/g, '~'));
	return t.some((x) => BLOCKED.has(x)) ? null : t;
}
/** Relative paths ("name") read the template item; absolute ones the root. */
export function absolute(path: string, scope: Scope): string {
	if (typeof path !== 'string') return '/';
	if (path.startsWith('/')) return path;
	return (scope.path === '/' ? '' : scope.path) + '/' + path;
}
export function getAt(root: unknown, path: string): any {
	const ts = tokens(path);
	if (!ts) return undefined;
	let v: any = root;
	for (const t of ts) {
		if (v == null || typeof v !== 'object') return undefined;
		v = v[t];
	}
	return v;
}
/** Writes a value; null deletes it. Returns the new root when path is "/". */
export function setAt(root: Record<string, any>, path: string, value: unknown): Record<string, any> {
	const ts = tokens(path);
	if (!ts) return root;
	if (!ts.length) return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, any>) : {};
	let v: any = root;
	for (let i = 0; i < ts.length - 1; i++) {
		if (v[ts[i]] == null || typeof v[ts[i]] !== 'object') v[ts[i]] = /^\d+$/.test(ts[i + 1]) ? [] : {};
		v = v[ts[i]];
	}
	const last = ts[ts.length - 1];
	if (value === null) {
		if (Array.isArray(v) && /^\d+$/.test(last)) v.splice(Number(last), 1);
		else delete v[last];
	} else v[last] = value;
	return root;
}

// ---------------------------------------------------------------- addresses

/** A panel path such as /bots/abc (never //host or /\host). */
export const samePath = (s: unknown): s is string => typeof s === 'string' && s.startsWith('/') && !s.startsWith('//') && !s.startsWith('/\\') && !/[\s\\]/.test(s) && s.length <= 500;
/** A panel path or an https:// address. */
export const safeHref = (s: unknown): s is string => samePath(s) || (typeof s === 'string' && /^https:\/\/[^\s\\]+$/.test(s) && s.length <= 2000);

// ---------------------------------------------------------------- functions

const isObj = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v);
/** Booleans, and validation results through their "valid" flag. */
export function truthy(v: unknown): boolean {
	if (isObj(v) && 'valid' in v) return !!v.valid;
	return !!v;
}
const text = (v: unknown): string => (v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v));
const num = (v: unknown): number => (typeof v === 'number' ? v : Number(v));
const valid = (ok: boolean) => ({ valid: ok });

/** Resolves a literal, a {"@path"} binding or a {"@call"} function call. */
export function evaluate(v: unknown, s: Surface, scope: Scope): any {
	if (isObj(v)) {
		if ('@path' in v) return getAt(s.data, absolute(v['@path'], scope));
		if ('@call' in v) return call(String(v['@call']), isObj(v.args) ? v.args : {}, s, scope);
	}
	return v;
}

function call(name: string, raw: Record<string, any>, s: Surface, scope: Scope): any {
	const a = (k: string) => evaluate(raw[k], s, scope);
	switch (name) {
		case 'required': {
			const v = a('value');
			return valid(!(v == null || v === '' || (Array.isArray(v) && !v.length)));
		}
		case 'regex': {
			const pattern = String(raw.pattern ?? '');
			if (pattern.length > 256) return valid(false);
			try {
				return valid(new RegExp(pattern, 'u').test(text(a('value')).slice(0, 4000)));
			} catch {
				return valid(false);
			}
		}
		case 'length': {
			const n = [...text(a('value'))].length;
			return valid((raw.min == null || n >= num(raw.min)) && (raw.max == null || n <= num(raw.max)));
		}
		case 'numeric': {
			const v = a('value');
			const n = num(v);
			return valid(v !== '' && v != null && Number.isFinite(n) && (raw.min == null || n >= num(raw.min)) && (raw.max == null || n <= num(raw.max)));
		}
		case 'email':
			return valid(/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(text(a('value'))));
		case 'formatString':
			return interpolate(text(a('value')), s, scope);
		case 'formatNumber': {
			const n = num(a('value'));
			if (!Number.isFinite(n)) return '';
			const d = raw.decimals == null ? undefined : Math.max(0, Math.min(10, num(a('decimals'))));
			return new Intl.NumberFormat(undefined, { minimumFractionDigits: d, maximumFractionDigits: d ?? 3, useGrouping: raw.grouping == null ? true : truthy(a('grouping')) }).format(n);
		}
		case 'formatCurrency': {
			const n = num(a('value'));
			const d = raw.decimals == null ? undefined : Math.max(0, Math.min(10, num(a('decimals'))));
			try {
				return new Intl.NumberFormat(undefined, { style: 'currency', currency: text(a('currency')) || 'USD', minimumFractionDigits: d, maximumFractionDigits: d, useGrouping: raw.grouping == null ? true : truthy(a('grouping')) }).format(n);
			} catch {
				return Number.isFinite(n) ? String(n) : '';
			}
		}
		case 'formatDate':
			return formatDate(a('value'), text(a('format')) || 'yyyy-MM-dd HH:mm');
		case 'pluralize': {
			const n = num(a('value'));
			if (n === 0 && raw.zero != null) return text(a('zero'));
			const cat = new Intl.PluralRules().select(Number.isFinite(n) ? n : 0);
			return text(raw[cat] != null ? a(cat) : a('other'));
		}
		case 'and':
			return Array.isArray(raw.values) && raw.values.every((x) => truthy(evaluate(x, s, scope)));
		case 'or':
			return Array.isArray(raw.values) && raw.values.some((x) => truthy(evaluate(x, s, scope)));
		case 'not':
			return !truthy(a('value'));
		case '@index':
			return (scope.index ?? 0) + (raw.offset == null ? 0 : num(a('offset')) || 0);
	}
	// openUrl only runs from a button; anything else is not in the catalog.
	return undefined;
}

/** formatString: ${/abs}, ${rel}, ${fn(arg:${/x}, other:'text')}; \${ is literal. */
export function interpolate(src: string, s: Surface, scope: Scope): string {
	let out = '';
	for (let i = 0; i < src.length; ) {
		if (src.startsWith('\\${', i)) {
			out += '${';
			i += 3;
			continue;
		}
		if (src.startsWith('${', i)) {
			const end = closing(src, i + 2);
			if (end < 0) {
				out += src.slice(i);
				break;
			}
			out += text(expression(src.slice(i + 2, end).trim(), s, scope));
			i = end + 1;
			continue;
		}
		out += src[i++];
	}
	return out;
}
function closing(src: string, from: number): number {
	let depth = 1;
	for (let i = from; i < src.length; i++) {
		if (src.startsWith('${', i)) {
			depth++;
			i++;
		} else if (src[i] === '}' && --depth === 0) return i;
	}
	return -1;
}
function expression(e: string, s: Surface, scope: Scope): any {
	const fn = /^(@?[A-Za-z_]\w*)\s*\((.*)\)$/s.exec(e);
	if (fn || e === '@index') {
		const args: Record<string, any> = {};
		for (const part of fn ? split(fn[2]) : []) {
			const at = part.indexOf(':');
			if (at < 0) continue;
			args[part.slice(0, at).trim()] = literal(part.slice(at + 1).trim(), s, scope);
		}
		return call(fn ? fn[1] : '@index', args, s, scope);
	}
	return getAt(s.data, absolute(e, scope));
}
function split(src: string): string[] {
	const parts: string[] = [];
	let depth = 0;
	let quote = false;
	let cur = '';
	for (let i = 0; i < src.length; i++) {
		const c = src[i];
		if (c === "'" && src[i - 1] !== '\\') quote = !quote;
		if (!quote && (c === '(' || src.startsWith('${', i))) depth++;
		if (!quote && (c === ')' || c === '}') && depth > 0) depth--;
		if (!quote && depth === 0 && c === ',') {
			parts.push(cur);
			cur = '';
			continue;
		}
		cur += c;
	}
	if (cur.trim()) parts.push(cur);
	return parts;
}
function literal(v: string, s: Surface, scope: Scope): any {
	if (v.startsWith('${') && v.endsWith('}')) return expression(v.slice(2, -1).trim(), s, scope);
	if (/^'.*'$/s.test(v)) return v.slice(1, -1).replace(/\\'/g, "'");
	if (v === 'true' || v === 'false') return v === 'true';
	if (v !== '' && Number.isFinite(Number(v))) return Number(v);
	return getAt(s.data, absolute(v, scope));
}

/** The common Unicode TR35 date fields. */
export function formatDate(value: unknown, pattern: string): string {
	const d = value instanceof Date ? value : new Date(typeof value === 'number' ? value : text(value));
	if (Number.isNaN(d.getTime())) return text(value);
	const pad = (n: number, w = 2) => String(n).padStart(w, '0');
	const month = (style: 'long' | 'short') => d.toLocaleString(undefined, { month: style });
	const day = (style: 'long' | 'short') => d.toLocaleString(undefined, { weekday: style });
	const h12 = d.getHours() % 12 || 12;
	const fields: Record<string, () => string> = {
		yyyy: () => String(d.getFullYear()),
		yy: () => pad(d.getFullYear() % 100),
		y: () => String(d.getFullYear()),
		MMMM: () => month('long'),
		MMM: () => month('short'),
		MM: () => pad(d.getMonth() + 1),
		M: () => String(d.getMonth() + 1),
		dd: () => pad(d.getDate()),
		d: () => String(d.getDate()),
		EEEE: () => day('long'),
		EEE: () => day('short'),
		E: () => day('short'),
		HH: () => pad(d.getHours()),
		H: () => String(d.getHours()),
		hh: () => pad(h12),
		h: () => String(h12),
		mm: () => pad(d.getMinutes()),
		m: () => String(d.getMinutes()),
		ss: () => pad(d.getSeconds()),
		s: () => String(d.getSeconds()),
		a: () => (d.getHours() < 12 ? 'AM' : 'PM')
	};
	return pattern.replace(/'([^']*)'|yyyy|yy|y|MMMM|MMM|MM|M|dd|d|EEEE|EEE|E|HH|H|hh|h|mm|m|ss|s|a/g, (m, quoted) => (quoted !== undefined ? quoted : (fields[m]?.() ?? m)));
}

/** Every @checks rule that fails, with its message. */
export function failing(c: Component, s: Surface, scope: Scope): string[] {
	const rules = Array.isArray(c['@checks']) ? c['@checks'] : [];
	return rules.filter((r: any) => !truthy(evaluate(r?.condition, s, scope))).map((r: any) => String(r?.message ?? 'Check this value'));
}

/** The ids a component draws, in order (templates excluded). */
export function childIds(c: Component): string[] {
	const out: string[] = [];
	for (const k of ['child', 'trigger', 'content']) if (typeof c[k] === 'string') out.push(c[k]);
	if (Array.isArray(c.children)) out.push(...c.children.filter((x: unknown) => typeof x === 'string'));
	else if (isObj(c.children) && typeof c.children.componentId === 'string') out.push(c.children.componentId);
	if (Array.isArray(c.tabs)) for (const t of c.tabs) if (typeof t?.child === 'string') out.push(t.child);
	return out;
}

// ---------------------------------------------------------------- surfaces

function messagesOf(raw: unknown): any[] {
	let v: any = raw;
	if (typeof v === 'string') {
		try {
			v = JSON.parse(v);
		} catch {
			return [];
		}
	}
	if (isObj(v)) v = v.messages;
	if (typeof v === 'string') {
		try {
			v = JSON.parse(v);
		} catch {
			return [];
		}
	}
	return Array.isArray(v) ? v.filter(isObj) : [];
}
const surfaceOf = (m: Record<string, any>): string | undefined => {
	for (const k of ['createSurface', 'updateComponents', 'updateDataModel', 'deleteSurface']) if (isObj(m[k]) && typeof m[k].surfaceId === 'string') return m[k].surfaceId;
	return undefined;
};

/** Every surface of one chat, in the order they were created. */
export class Surfaces {
	all = $state<Surface[]>([]);

	clear() {
		this.all = [];
	}
	get(id: string) {
		return this.all.find((s) => s.id === id);
	}
	ownedBy(runId: string) {
		return this.all.filter((s) => s.owner === runId);
	}

	/** Applies one accepted show_ui call. Live updates to what is on screen morph in place. */
	async apply(runId: string, args: unknown, live: boolean) {
		const msgs = messagesOf(args);
		if (!msgs.length) return;
		const run = () => {
			for (const m of msgs) this.one(runId, m, live);
		};
		const shown = [...new Set(msgs.map(surfaceOf).filter((x): x is string => !!x && !!this.get(x)))];
		if (live && shown.length) await morph(shown, run);
		else run();
	}

	private one(runId: string, m: Record<string, any>, live: boolean) {
		if (m.version != null && m.version !== VERSION) return;
		if (isObj(m.createSurface)) {
			const b = m.createSurface;
			if (typeof b.surfaceId !== 'string' || this.get(b.surfaceId)) return;
			const s = new Surface(b.surfaceId, typeof b.catalogId === 'string' ? b.catalogId : PANEL_CATALOG);
			if (isObj(b.dataModel)) s.data = structuredClone(b.dataModel);
			if (Array.isArray(b.components)) for (const c of b.components) if (isObj(c) && typeof c.id === 'string') s.components[c.id] = c as Component;
			s.owner = runId;
			if (live) {
				s.fresh = true;
				setTimeout(() => (s.fresh = false), 1600);
			}
			this.all = [...this.all, s];
		} else if (isObj(m.updateComponents)) {
			const s = this.get(m.updateComponents.surfaceId);
			if (!s || !Array.isArray(m.updateComponents.components)) return;
			for (const c of m.updateComponents.components) if (isObj(c) && typeof c.id === 'string') s.components[c.id] = c as Component;
			s.owner = runId;
		} else if (isObj(m.updateDataModel)) {
			const b = m.updateDataModel;
			const s = this.get(b.surfaceId);
			if (!s || !('value' in b)) return;
			const path = typeof b.path === 'string' ? b.path : '/';
			const value = b.value != null && typeof b.value === 'object' ? structuredClone(b.value) : b.value;
			const root = setAt(s.data, path, value);
			if (root !== s.data) s.data = root;
			s.owner = runId;
		} else if (isObj(m.deleteSurface)) {
			this.all = this.all.filter((s) => s.id !== m.deleteSurface.surfaceId);
		}
	}
}

// ---------------------------------------------------------------- motion

// An update the person can see morphs in place: the surface and its cards,
// metrics, badges and buttons are named for one View Transition, so each
// part travels to its new size and position (even into a later turn of the
// chat) while the rest of the page holds still. Names live only for that one
// transition. Without the API, or with reduced motion, updates are instant.
const tagged: HTMLElement[] = [];
function untag() {
	for (const el of tagged.splice(0)) {
		el.style.viewTransitionName = '';
		el.style.removeProperty('view-transition-class');
	}
}
function tag(ids: string[], names: Map<string, string>, create: boolean) {
	let n = names.size;
	for (const id of ids) {
		const root = document.querySelector<HTMLElement>(`[data-a2-surface="${CSS.escape(id)}"]`);
		if (!root) continue;
		const parts: [string, HTMLElement][] = [[`${id}|`, root]];
		for (const el of root.querySelectorAll<HTMLElement>('[data-a2-morph]')) parts.push([`${id}|${el.dataset.a2Morph}`, el]);
		for (const [key, el] of parts.slice(0, 48)) {
			let name = names.get(key);
			if (!name) {
				if (!create) continue;
				name = `a2ui-${++n}`;
				names.set(key, name);
			}
			el.style.viewTransitionName = name;
			el.style.setProperty('view-transition-class', 'a2ui');
			tagged.push(el);
		}
	}
}
async function morph(ids: string[], mutate: () => void) {
	const doc = document as Document & { startViewTransition?: (cb: () => Promise<void>) => { finished: Promise<void> } };
	if (!doc.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches || document.visibilityState !== 'visible') {
		mutate();
		return;
	}
	const names = new Map<string, string>();
	tag(ids, names, true);
	if (!names.size) {
		untag();
		mutate();
		return;
	}
	const html = document.documentElement;
	html.dataset.a2uiMorph = '';
	try {
		const t = doc.startViewTransition(async () => {
			untag();
			mutate();
			await tick();
			tag(ids, names, false);
		});
		await t.finished;
	} catch {
		/* skipped transitions still applied the update */
	} finally {
		untag();
		delete html.dataset.a2uiMorph;
	}
}
