import { manifest, pages } from 'virtual:docs';
import type { CategoryMeta, PageMeta, SearchEntry } from './types';

export { manifest };

const bySlug = new Map<string, PageMeta>(manifest.categories.flatMap((c) => c.pages.map((p) => [p.slug, p] as const)));

export const category = (slug: string): CategoryMeta | undefined => manifest.categories.find((c) => c.slug === slug);
export const pageMeta = (slug: string) => bySlug.get(slug);
export const href = (slug: string, hash = '') => `/docs/${slug}${hash ? '#' + hash : ''}`;
/** 1-based position of a category, used for its label in the rail. */
export const categoryIndex = (slug: string) => manifest.categories.findIndex((c) => c.slug === slug) + 1;

export async function loadPage(slug: string) {
	const load = pages[slug];
	if (!load) return null;
	return (await load()).default;
}

export function neighbours(slug: string) {
	const i = manifest.order.indexOf(slug);
	return {
		prev: i > 0 ? bySlug.get(manifest.order[i - 1]) : undefined,
		next: i >= 0 && i < manifest.order.length - 1 ? bySlug.get(manifest.order[i + 1]) : undefined
	};
}

/** Where an old single-page /docs#hash link points now. */
export function resolveAlias(hash: string): string | null {
	const id = hash.replace(/^#/, '');
	const target = manifest.aliases[id];
	if (!target) return null;
	const [slug, h] = target.split('#');
	return href(slug, h);
}

// ---- search ----

let index: Promise<SearchEntry[]> | null = null;
export const loadIndex = () => (index ??= import('virtual:docs/search').then((m) => m.default));

export type Hit = SearchEntry & { score: number; snippet: string };

const norm = (s: string) => s.toLowerCase().normalize('NFKD').replace(/[̀-ͯ]/g, '');

/** Prefix-matches every query word; titles and headings outrank body text. */
export function search(entries: SearchEntry[], query: string, limit = 24): Hit[] {
	const words = norm(query).split(/[^\p{L}\p{N}_.$-]+/u).filter(Boolean);
	if (!words.length) return [];
	const hits: Hit[] = [];
	for (const e of entries) {
		const title = norm(e.title);
		const heading = norm(e.heading);
		const text = norm(e.text);
		let score = 0;
		let ok = true;
		for (const w of words) {
			const re = new RegExp(`(^|[^\\p{L}\\p{N}])${w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`, 'u');
			let s = 0;
			if (re.test(heading)) s += 6;
			if (re.test(title)) s += e.id ? 3 : 8;
			if (re.test(text)) s += 1;
			if (!s) {
				ok = false;
				break;
			}
			score += s;
		}
		if (!ok) continue;
		hits.push({ ...e, score, snippet: snippet(e.text, words) });
	}
	return hits.sort((a, b) => b.score - a.score).slice(0, limit);
}

function snippet(text: string, words: string[]) {
	const t = norm(text);
	let at = -1;
	for (const w of words) {
		at = t.indexOf(w);
		if (at >= 0) break;
	}
	if (at < 0) return text.slice(0, 140);
	const start = Math.max(0, text.lastIndexOf(' ', Math.max(0, at - 50)) + 1);
	const s = text.slice(start, start + 160);
	return (start > 0 ? '…' : '') + s + (start + 160 < text.length ? '…' : '');
}
