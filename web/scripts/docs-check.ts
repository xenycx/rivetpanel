// Checks docs/guide: frontmatter, icons, figures, links between pages and to
// headings, related pages, the old /docs#anchors, and /docs links in the UI.
//
//   node scripts/docs-check.ts          (make docs-check)
//   node scripts/docs-check.ts --index  (make docs-index: rewrites docs/README.md)
import { readFileSync, readdirSync, statSync, writeFileSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { readGuide } from './docs.ts';

const WEB = resolve(import.meta.dirname, '..');
const REPO = resolve(WEB, '..');
const g = readGuide(REPO);
const problems = [...g.problems];

const icons = new Set(
	[...readFileSync(join(WEB, 'src/lib/components/ui/Icon.svelte'), 'utf8').matchAll(/^\t\t([a-zA-Z]+):/gm)].map((m) => m[1])
);
const figures = new Set([...readFileSync(join(WEB, 'src/lib/docs/figures/index.ts'), 'utf8').matchAll(/^\t'([a-z0-9-]+)':/gm)].map((m) => m[1]));

// Every section id of the old single-page /docs; links to them must keep working.
const OLD = 'version quickstart remote-nodes game-servers containers deploy addons extensions build publish workspaces roles sign-in sites site-apps appearance ai widgets widget-data telemetry health usage support email security backups automation admin-tools workspace-limits log-archive operations'.split(' ');

const ids = (slug: string) => new Set(g.pages.get(slug)?.meta.headings.map((h) => h.id) ?? []);
function target(ref: string, from: string) {
	const [slug, hash] = ref.replace(/^\/docs\//, '').split('#');
	if (!slug) return;
	if (!slug.includes('/')) {
		if (!g.categories.some((c) => c.slug === slug)) problems.push(`${from}: link to unknown section /docs/${slug}`);
		return;
	}
	if (!g.pages.has(slug)) problems.push(`${from}: link to unknown page ${slug}`);
	else if (hash && !ids(slug).has(hash)) problems.push(`${from}: link to unknown heading ${slug}#${hash}`);
}

for (const c of g.categories) {
	if (!icons.has(c.icon)) problems.push(`${c.file}: unknown icon "${c.icon}"`);
	if (!c.blurb) problems.push(`${c.file}: missing blurb`);
	if (!c.pages.length) problems.push(`${c.file}: the section has no pages`);
	for (const p of c.pages) {
		if (!icons.has(p.icon)) problems.push(`${p.file}: unknown icon "${p.icon}"`);
		for (const f of p.figures) if (!figures.has(f)) problems.push(`${p.file}: unknown figure "${f}"`);
		for (const b of g.pages.get(p.slug)!.blocks)
			if (b.t === 'figure' && b.fallback && !existsSync(join(REPO, b.fallback))) problems.push(`${p.file}: figure picture ${b.fallback} is missing (make docs-figures)`);
		for (const r of p.related) if (!g.pages.has(r)) problems.push(`${p.file}: related page ${r} does not exist`);
		if (p.lede.length > 200) problems.push(`${p.file}: the lede is over 200 characters`);
		if (p.glance.length > 4) problems.push(`${p.file}: more than 4 glance facts`);
	}
}
for (const l of g.links) target(l.href, l.file);
for (const [old, to] of Object.entries(g.aliases)) target(to, `alias ${old}`);
for (const old of OLD) if (!g.aliases[old]) problems.push(`old /docs#${old} is not claimed by any page (add it to aliases)`);

// /docs/... links in the interface.
function walk(dir: string): string[] {
	return readdirSync(dir).flatMap((f) => {
		const p = join(dir, f);
		return statSync(p).isDirectory() ? walk(p) : /\.(svelte|ts)$/.test(f) ? [p] : [];
	});
}
for (const file of walk(join(WEB, 'src'))) {
	const src = readFileSync(file, 'utf8');
	for (const m of src.matchAll(/['"`(]\/docs\/([a-z0-9-]+(?:\/[a-z0-9-]+)?(?:#[a-z0-9-]+)?)['"`)]/g)) target(m[1], file.slice(REPO.length + 1));
	for (const m of src.matchAll(/['"`]\/docs#([a-z0-9-]+)['"`]/g)) problems.push(`${file.slice(REPO.length + 1)}: old link /docs#${m[1]}; link the page directly`);
}

// docs/README.md is the GitHub entry point; it is generated from the guide.
function index() {
	const out = [
		'# RivetPanel documentation',
		'',
		'<!-- Generated from docs/guide by `make docs-index`; edit the guide, not this file. -->',
		'',
		'RivetPanel runs Discord bots and other applications, Minecraft and Steam game',
		'servers, Docker containers and Compose stacks, and websites. This guide is the',
		'same one the panel shows under **Help → Documentation**. The authoritative',
		'version is in the root `VERSION` file; release notes are in',
		'[CHANGELOG.md](../CHANGELOG.md).',
		''
	];
	for (const c of g.categories) {
		out.push(`## ${c.title}`, '', c.blurb, '');
		for (const p of c.pages) out.push(`- [${p.title}](${p.file.replace(/^docs\//, '')})${p.audience === 'admin' ? ' (administrators)' : ''}: ${p.lede}`);
		out.push('');
	}
	out.push('## Project', '', '- [Implementation status](implementation-status.md): completed and remaining platform-overhaul work', '- [Writing the guide](guide/WRITING.md): layout and conventions for these pages', '');
	return out.join('\n');
}
const README = join(REPO, 'docs/README.md');
if (process.argv.includes('--index')) {
	writeFileSync(README, index());
	console.log('docs-index: wrote docs/README.md');
} else if (!existsSync(README) || readFileSync(README, 'utf8') !== index()) problems.push('docs/README.md is out of date; run make docs-index');

const pages = g.pages.size;
const words = [...g.pages.values()].reduce((n, p) => n + p.meta.words, 0);
if (problems.length) {
	for (const p of problems) console.error('docs: ' + p);
	console.error(`docs-check: ${problems.length} problem(s) in ${pages} pages`);
	process.exit(1);
}
console.log(`docs-check: ${g.categories.length} sections, ${pages} pages, ${words} words, no problems`);
