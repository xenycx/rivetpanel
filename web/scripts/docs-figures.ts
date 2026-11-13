// Photographs every drawn figure in the guide for GitHub, where the panel's
// figures cannot render: docs/guide/figures/<name>.png, referenced by the
// image line under each <!-- figure: name --> comment.
//
//   make docs-figures                       (needs `npm run dev` on :5173)
//   DOCS_URL=http://127.0.0.1:8080 node scripts/docs-figures.ts
//   CHROMIUM=/path/to/chrome                (default: the Playwright cache)
import { mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { homedir } from 'node:os';
import { join, resolve, basename } from 'node:path';
import { chromium } from 'playwright-core';
import { readGuide } from './docs.ts';

const REPO = resolve(import.meta.dirname, '../..');
const BASE = process.env.DOCS_URL ?? 'http://localhost:5173';
const CHROME = process.env.CHROMIUM ?? join(homedir(), '.cache/ms-playwright/chromium-1243/chrome-linux64/chrome');

const g = readGuide(REPO);
const shots: { slug: string; name: string; out: string; index: number }[] = [];
for (const [slug, page] of g.pages) {
	let index = 0;
	for (const b of page.blocks) {
		if (b.t !== 'figure') continue;
		if (b.fallback) shots.push({ slug, name: b.name, out: join(REPO, b.fallback), index });
		index++;
	}
}
if (!shots.length) {
	console.log('docs-figures: no figure has a picture line');
	process.exit(0);
}
mkdirSync(join(REPO, 'docs/guide/figures'), { recursive: true });
const magick = (() => {
	try {
		execFileSync('magick', ['-version'], { stdio: 'ignore' });
		return true;
	} catch {
		return false;
	}
})();

const browser = await chromium.launch({ executablePath: CHROME });
// The article column at a desktop width, light theme, twice the pixels.
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2, colorScheme: 'light', reducedMotion: 'reduce' });
for (const s of shots) {
	const page = await ctx.newPage();
	await page.goto(`${BASE}/docs/${s.slug}`, { waitUntil: 'networkidle' });
	const fig = page.locator('.doc-figure').nth(s.index);
	await fig.scrollIntoViewIfNeeded();
	await fig.screenshot({ path: s.out });
	// Flat diagrams survive a 64-colour palette unchanged to the eye, at a third of the size.
	if (magick) execFileSync('magick', [s.out, '-strip', '-dither', 'None', '-colors', '64', '-define', 'png:compression-level=9', `PNG8:${s.out}`]);
	console.log(`docs-figures: ${s.slug} → docs/guide/figures/${basename(s.out)}`);
	await page.close();
}
await browser.close();
