// Build-time reader for the documentation guide in ../docs/guide.
//
// docs/guide is the single source for the in-panel /docs pages, their search
// index and the Ctrl+K entries, and it reads as ordinary Markdown on GitHub.
// This module turns it into small JSON block trees; the Vite plugin in
// vite.config.ts serves them as virtual modules and scripts/docs-check.ts
// validates them. It runs under Vite (bundled) and under plain Node (type
// stripping), so it uses only relative imports and node: built-ins.
//
// Layout:  docs/guide/<NN-category>/_category.md   (title, blurb, icon)
//          docs/guide/<NN-category>/<NN-page>.md    (one page)
// The NN- prefixes order categories and pages and are dropped from URLs:
// docs/guide/05-observe/04-health.md is /docs/observe/health.
//
// Markdown conventions beyond CommonMark/GFM (all readable on GitHub):
//   > [!NOTE] / [!TIP] / [!IMPORTANT] / [!WARNING] / [!CAUTION]   callouts
//   **Administration → Nodes**     a strong span containing → is a menu path
//   [enforced: panel|docker|host]  [not enforced]   enforcement badges
//   <!-- figure: name -->          an interactive or drawn figure (hidden on GitHub)
//   Ordered lists render as numbered steps.

import { readFileSync, readdirSync, existsSync, statSync } from 'node:fs';
import { join, posix } from 'node:path';
import { Lexer, type Token, type Tokens } from 'marked';

export type Inline =
	| string
	| { t: 'code'; v: string }
	| { t: 'b' | 'i' | 'del'; c: Inline[] }
	| { t: 'a'; href: string; c: Inline[] }
	| { t: 'path'; parts: string[] }
	| { t: 'enf'; k: Enforcement }
	| { t: 'br' };

export type Enforcement = 'panel' | 'docker' | 'host' | 'none';
export type CalloutKind = 'note' | 'tip' | 'important' | 'warning' | 'caution';

export type Block =
	| { t: 'h'; d: number; id: string; c: Inline[] }
	| { t: 'p'; c: Inline[] }
	| { t: 'ul' | 'ol'; start?: number; items: Block[][] }
	| { t: 'code'; lang: string; text: string }
	| { t: 'table'; align: ('left' | 'center' | 'right' | null)[]; head: Inline[][]; rows: Inline[][][] }
	| { t: 'callout'; kind: CalloutKind; c: Block[] }
	| { t: 'quote'; c: Block[] }
	| { t: 'figure'; name: string; fallback?: string }
	| { t: 'hr' };

export type Heading = { id: string; text: string; d: number };

export type PageMeta = {
	slug: string; // "observe/health"
	category: string; // "observe"
	page: string; // "health"
	file: string; // repository-relative source path
	title: string;
	/** Plain text (search, meta description); ledeC/glanceC keep inline formatting. */
	lede: string;
	ledeC: Inline[];
	icon: string;
	audience: 'everyone' | 'admin';
	glance: string[];
	glanceC: Inline[][];
	related: string[];
	aliases: string[];
	headings: Heading[];
	figures: string[];
	words: number;
};

export type CategoryMeta = { slug: string; title: string; blurb: string; icon: string; file: string; pages: PageMeta[] };

export type Page = { meta: PageMeta; blocks: Block[] };

export type SearchEntry = { slug: string; id: string; heading: string; title: string; category: string; text: string };

export type Guide = {
	categories: CategoryMeta[];
	pages: Map<string, Page>;
	/** Old /docs#hash ids → "category/page" or "category/page#heading". */
	aliases: Record<string, string>;
	problems: string[];
};

const GITHUB = 'https://github.com/xenycx/rivetpanel/blob/main/';
const ORDER = /^\d+-/;

export const stripOrder = (name: string) => name.replace(ORDER, '').replace(/\.md$/, '');

// ---- frontmatter (a small YAML subset: scalars, [a, b] and "- item" lists) ----

type Front = Record<string, string | string[]>;

function unquote(s: string) {
	s = s.trim();
	if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) return s.slice(1, -1);
	return s;
}

export function frontmatter(src: string): { data: Front; body: string } {
	const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(src);
	if (!m) return { data: {}, body: src };
	const data: Front = {};
	let list: string | null = null;
	for (const raw of m[1].split(/\r?\n/)) {
		if (!raw.trim() || raw.trim().startsWith('#')) continue;
		const item = /^\s+-\s+(.*)$/.exec(raw);
		if (item && list) {
			(data[list] as string[]).push(unquote(item[1]));
			continue;
		}
		const kv = /^([A-Za-z_][\w-]*):\s*(.*)$/.exec(raw);
		if (!kv) continue;
		const [, key, value] = kv;
		list = null;
		if (value === '') {
			data[key] = [];
			list = key;
		} else if (value.startsWith('[') && value.endsWith(']')) {
			data[key] = value.slice(1, -1).split(',').map(unquote).filter(Boolean);
		} else data[key] = unquote(value);
	}
	return { data, body: src.slice(m[0].length) };
}

const str = (v: string | string[] | undefined, d = '') => (typeof v === 'string' ? v : d);
const arr = (v: string | string[] | undefined) => (Array.isArray(v) ? v : typeof v === 'string' && v ? [v] : []);

// ---- headings ----

/** GitHub-compatible heading id, so #anchors work in the panel and on GitHub. */
export function slugify(text: string) {
	return text
		.toLowerCase()
		.trim()
		.replace(/[^\p{L}\p{N}\s_-]/gu, '')
		.replace(/\s/g, '-');
}

// ---- inline ----

const ENF = /\[(?:enforced:\s*(panel|docker|host)|(not enforced))\]/g;

function textInline(text: string): Inline[] {
	const out: Inline[] = [];
	let last = 0;
	for (const m of text.matchAll(ENF)) {
		if (m.index! > last) out.push(text.slice(last, m.index));
		out.push({ t: 'enf', k: (m[1] as Enforcement) ?? 'none' });
		last = m.index! + m[0].length;
	}
	if (last < text.length) out.push(text.slice(last));
	return out;
}

function decode(s: string) {
	return s
		.replace(/&amp;/g, '&')
		.replace(/&lt;/g, '<')
		.replace(/&gt;/g, '>')
		.replace(/&quot;/g, '"')
		.replace(/&#39;/g, "'");
}

function plain(c: Inline[]): string {
	return c.map((x) => (typeof x === 'string' ? x : 'v' in x ? x.v : 'c' in x ? plain(x.c) : 'parts' in x ? x.parts.join(' → ') : '')).join('');
}

type Ctx = { file: string; problems: string[]; links: { href: string; file: string }[] };

function link(href: string, ctx: Ctx): string {
	if (/^[a-z]+:/i.test(href) || href.startsWith('#') || href.startsWith('/')) return href;
	const [path, hash] = href.split('#');
	const target = posix.normalize(posix.join(posix.dirname(ctx.file), path));
	const guide = /^docs\/guide\/([^/]+)\/([^/]+)\.md$/.exec(target);
	if (guide) {
		const slug = `${stripOrder(guide[1])}/${stripOrder(guide[2])}`;
		ctx.links.push({ href: slug + (hash ? '#' + hash : ''), file: ctx.file });
		return `/docs/${slug}${hash ? '#' + hash : ''}`;
	}
	const cat = /^docs\/guide\/([^/]+)\/?(?:_category\.md)?$/.exec(target);
	if (cat) return `/docs/${stripOrder(cat[1])}`;
	// Anything else in the repository opens on GitHub.
	return GITHUB + target + (hash ? '#' + hash : '');
}

function inline(tokens: Token[] | undefined, ctx: Ctx): Inline[] {
	const out: Inline[] = [];
	for (const tok of tokens ?? []) {
		switch (tok.type) {
			case 'text':
			case 'escape': {
				const t = tok as Tokens.Text;
				if (t.tokens?.length) out.push(...inline(t.tokens, ctx));
				else out.push(...textInline(decode(t.text)));
				break;
			}
			case 'codespan':
				out.push({ t: 'code', v: decode((tok as Tokens.Codespan).text) });
				break;
			case 'strong': {
				const c = inline((tok as Tokens.Strong).tokens, ctx);
				const s = plain(c);
				if (s.includes('→') && c.every((x) => typeof x === 'string')) out.push({ t: 'path', parts: s.split('→').map((p) => p.trim()).filter(Boolean) });
				else out.push({ t: 'b', c });
				break;
			}
			case 'em':
				out.push({ t: 'i', c: inline((tok as Tokens.Em).tokens, ctx) });
				break;
			case 'del':
				out.push({ t: 'del', c: inline((tok as Tokens.Del).tokens, ctx) });
				break;
			case 'link': {
				const l = tok as Tokens.Link;
				out.push({ t: 'a', href: link(l.href, ctx), c: inline(l.tokens, ctx) });
				break;
			}
			case 'br':
				out.push({ t: 'br' });
				break;
			case 'html':
				break; // raw HTML is not rendered in the panel
			default:
				if ('text' in tok) out.push(...textInline(decode(String(tok.text))));
		}
	}
	// Merge adjacent strings.
	const merged: Inline[] = [];
	for (const x of out) {
		const prev = merged[merged.length - 1];
		if (typeof x === 'string' && typeof prev === 'string') merged[merged.length - 1] = prev + x;
		else merged.push(x);
	}
	return merged;
}

// ---- blocks ----

const ALERT = /^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n?/i;
const FIGURE = /<!--\s*figure:\s*([a-z0-9-]+)\s*-->/g;

function blocks(tokens: Token[], ctx: Ctx, ids: Map<string, number>, headings: Heading[] | null, figures: string[]): Block[] {
	const out: Block[] = [];
	for (const tok of tokens) {
		switch (tok.type) {
			case 'heading': {
				const h = tok as Tokens.Heading;
				const c = inline(h.tokens, ctx);
				const text = plain(c);
				let id = slugify(text);
				const n = ids.get(id) ?? 0;
				ids.set(id, n + 1);
				if (n) id = `${id}-${n}`;
				if (h.depth === 1) {
					ctx.problems.push(`${ctx.file}: use the title frontmatter instead of a # heading`);
					break;
				}
				out.push({ t: 'h', d: h.depth, id, c });
				headings?.push({ id, text, d: h.depth });
				break;
			}
			case 'paragraph': {
				const p = tok as Tokens.Paragraph;
				const parts = p.tokens.filter((t) => !(t.type === 'text' && !t.raw.trim()));
				if (parts.length === 1 && parts[0].type === 'image') {
					// An image right after a figure is that figure's picture for
					// GitHub; the panel draws the figure instead.
					const prev = out[out.length - 1];
					const src = posix.normalize(posix.join(posix.dirname(ctx.file), (parts[0] as Tokens.Image).href));
					if (prev?.t === 'figure') prev.fallback = src;
					else ctx.problems.push(`${ctx.file}: images are only allowed right after a <!-- figure --> (as its picture for GitHub)`);
					break;
				}
				out.push({ t: 'p', c: inline(p.tokens, ctx) });
				break;
			}
			case 'text': {
				// Tight list items hold bare text tokens.
				const t = tok as Tokens.Text;
				out.push({ t: 'p', c: t.tokens ? inline(t.tokens, ctx) : textInline(decode(t.text)) });
				break;
			}
			case 'list': {
				const l = tok as Tokens.List;
				const items = l.items.map((it) => blocks(it.tokens, ctx, ids, null, figures));
				out.push(l.ordered ? { t: 'ol', start: typeof l.start === 'number' && l.start !== 1 ? l.start : undefined, items } : { t: 'ul', items });
				break;
			}
			case 'code': {
				const c = tok as Tokens.Code;
				out.push({ t: 'code', lang: (c.lang ?? '').split(/\s/)[0], text: c.text });
				break;
			}
			case 'table': {
				const tb = tok as Tokens.Table;
				out.push({
					t: 'table',
					align: tb.align,
					head: tb.header.map((c) => inline(c.tokens, ctx)),
					rows: tb.rows.map((r) => r.map((c) => inline(c.tokens, ctx)))
				});
				break;
			}
			case 'blockquote': {
				const q = tok as Tokens.Blockquote;
				const m = ALERT.exec(q.text);
				if (m) {
					const inner = new Lexer({ gfm: true }).lex(q.text.replace(ALERT, ''));
					out.push({ t: 'callout', kind: m[1].toLowerCase() as CalloutKind, c: blocks(inner, ctx, ids, null, figures) });
				} else out.push({ t: 'quote', c: blocks(q.tokens, ctx, ids, null, figures) });
				break;
			}
			case 'html': {
				const raw = (tok as Tokens.HTML).raw;
				for (const m of raw.matchAll(FIGURE)) {
					out.push({ t: 'figure', name: m[1] });
					figures.push(m[1]);
				}
				break;
			}
			case 'hr':
				out.push({ t: 'hr' });
				break;
			case 'space':
				break;
			default:
				ctx.problems.push(`${ctx.file}: unsupported Markdown block "${tok.type}"`);
		}
	}
	return out;
}

function words(b: Block[]): string {
	return b
		.map((x) => {
			switch (x.t) {
				case 'h':
				case 'p':
					return plain(x.c);
				case 'ul':
				case 'ol':
					return x.items.map(words).join(' ');
				case 'table':
					return [...x.head, ...x.rows.flat()].map(plain).join(' ');
				case 'callout':
				case 'quote':
					return words(x.c);
				case 'code':
					return x.text;
				default:
					return '';
			}
		})
		.join(' ');
}

// ---- the guide ----

export function readPage(root: string, rel: string, category: string, ctx: Omit<Ctx, 'file'>): Page {
	const src = readFileSync(join(root, rel), 'utf8');
	const { data, body } = frontmatter(src);
	const c: Ctx = { ...ctx, file: rel };
	const headings: Heading[] = [];
	const figures: string[] = [];
	const tree = blocks(new Lexer({ gfm: true }).lex(body), c, new Map(), headings, figures);
	const page = stripOrder(rel.split('/').pop()!);
	const audience = str(data.audience, 'everyone') === 'admin' ? 'admin' : 'everyone';
	const md = (text: string) => inline(Lexer.lexInline(text, { gfm: true }), c);
	const ledeC = md(str(data.lede));
	const glanceC = arr(data.glance).map(md);
	const meta: PageMeta = {
		slug: `${category}/${page}`,
		category,
		page,
		file: rel,
		title: str(data.title),
		lede: plain(ledeC),
		ledeC,
		icon: str(data.icon, 'book'),
		audience,
		glance: glanceC.map(plain),
		glanceC,
		related: arr(data.related),
		aliases: arr(data.aliases),
		headings,
		figures,
		words: words(tree).split(/\s+/).filter(Boolean).length
	};
	if (!meta.title) c.problems.push(`${rel}: missing title`);
	if (!meta.lede) c.problems.push(`${rel}: missing lede`);
	return { meta, blocks: tree };
}

/** Reads docs/guide under the repository root. */
export function readGuide(repo: string): Guide & { links: { href: string; file: string }[] } {
	const base = join(repo, 'docs/guide');
	const problems: string[] = [];
	const links: { href: string; file: string }[] = [];
	const categories: CategoryMeta[] = [];
	const pages = new Map<string, Page>();
	const aliases: Record<string, string> = {};
	if (!existsSync(base)) return { categories, pages, aliases, problems: ['docs/guide does not exist'], links };
	const dirs = readdirSync(base)
		.filter((d) => ORDER.test(d) && statSync(join(base, d)).isDirectory())
		.sort();
	for (const d of dirs) {
		const slug = stripOrder(d);
		const catFile = `docs/guide/${d}/_category.md`;
		const cf = existsSync(join(repo, catFile)) ? frontmatter(readFileSync(join(repo, catFile), 'utf8')).data : {};
		if (!cf.title) problems.push(`${catFile}: missing (needs title, blurb, icon)`);
		const cat: CategoryMeta = { slug, title: str(cf.title, slug), blurb: str(cf.blurb), icon: str(cf.icon, 'book'), file: catFile, pages: [] };
		const files = readdirSync(join(base, d))
			.filter((f) => ORDER.test(f) && f.endsWith('.md'))
			.sort();
		for (const f of files) {
			const p = readPage(repo, `docs/guide/${d}/${f}`, slug, { problems, links });
			if (pages.has(p.meta.slug)) problems.push(`${p.meta.file}: duplicate page slug ${p.meta.slug}`);
			pages.set(p.meta.slug, p);
			cat.pages.push(p.meta);
			for (const a of p.meta.aliases) {
				const [old, heading] = a.split('#');
				if (aliases[old]) problems.push(`${p.meta.file}: alias ${old} is also claimed by ${aliases[old]}`);
				aliases[old] = p.meta.slug + (heading ? '#' + heading : '');
			}
		}
		categories.push(cat);
	}
	return { categories, pages, aliases, problems, links };
}

/** Section-level search entries: one per H2/H3 plus the page introduction. */
export function searchIndex(g: Guide): SearchEntry[] {
	const out: SearchEntry[] = [];
	for (const cat of g.categories) {
		for (const meta of cat.pages) {
			const page = g.pages.get(meta.slug)!;
			let cur: SearchEntry = { slug: meta.slug, id: '', heading: '', title: meta.title, category: cat.title, text: meta.lede + ' ' + meta.glance.join(' ') };
			const flush = () => {
				cur.text = cur.text.replace(/\s+/g, ' ').trim().slice(0, 1200);
				out.push(cur);
			};
			for (const b of page.blocks) {
				if (b.t === 'h' && b.d <= 3) {
					flush();
					cur = { slug: meta.slug, id: b.id, heading: plain(b.c), title: meta.title, category: cat.title, text: '' };
				} else cur.text += ' ' + words([b]);
			}
			flush();
		}
	}
	return out;
}

/** Everything the client manifest needs (no block trees). */
export function manifest(g: Guide) {
	const order = g.categories.flatMap((c) => c.pages.map((p) => p.slug));
	return {
		categories: g.categories,
		order,
		aliases: g.aliases
	};
}

// ---- changelog ----

export type Release = { version: string; date: string; blocks: Block[] };

/** The Unreleased section (when it has entries) and the latest `count` releases. */
export function readChangelog(repo: string, count = 1): { releases: Release[]; problems: string[] } {
	const problems: string[] = [];
	const src = readFileSync(join(repo, 'CHANGELOG.md'), 'utf8');
	const parts = src.split(/^## /m).slice(1);
	const releases: Release[] = [];
	for (const part of parts) {
		const nl = part.indexOf('\n');
		const head = part.slice(0, nl).trim();
		const body = part.slice(nl + 1);
		const m = /^\[?([^\]\s]+)\]?(?:\s+-\s+(\S+))?/.exec(head);
		if (!m) continue;
		const ctx: Ctx = { file: 'CHANGELOG.md', problems, links: [] };
		const blocksOut = blocks(new Lexer({ gfm: true }).lex(body), ctx, new Map(), null, []);
		if (m[1].toLowerCase() === 'unreleased') {
			if (blocksOut.length) releases.push({ version: 'Unreleased', date: '', blocks: blocksOut });
			continue;
		}
		releases.push({ version: m[1], date: m[2] ?? '', blocks: blocksOut });
		if (releases.filter((r) => r.version !== 'Unreleased').length >= count) break;
	}
	return { releases, problems };
}
