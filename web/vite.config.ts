import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, type Plugin } from 'vite';
import { resolve } from 'node:path';
import { readGuide, manifest, searchIndex, readChangelog } from './scripts/docs';

const REPO = resolve(import.meta.dirname, '..');
const GUIDE = resolve(REPO, 'docs/guide');

/**
 * Serves docs/guide (the single source of the documentation) as virtual
 * modules: `virtual:docs` (manifest and lazy page loaders),
 * `virtual:docs/page/<category>/<page>` (one block tree each) and
 * `virtual:docs/search` (the search index, loaded on first search) and
 * `virtual:docs/changelog` (the latest release notes).
 */
function docs(): Plugin {
	const PREFIX = 'virtual:docs';
	let guide: ReturnType<typeof readGuide> | null = null;
	const load = () => (guide ??= readGuide(REPO));
	return {
		name: 'rivetpanel-docs',
		resolveId(id) {
			if (id === PREFIX || id.startsWith(PREFIX + '/')) return '\0' + id;
		},
		load(id) {
			if (!id.startsWith('\0' + PREFIX)) return;
			const g = load();
			const name = id.slice(1);
			if (name === PREFIX) {
				for (const p of g.problems) this.warn(p);
				const loaders = [...g.pages.keys()].map((s) => `${JSON.stringify(s)}: () => import(${JSON.stringify(`${PREFIX}/page/${s}`)})`).join(',\n');
				return `export const manifest = ${JSON.stringify(manifest(g))};\nexport const pages = {\n${loaders}\n};\n`;
			}
			if (name === `${PREFIX}/search`) return `export default ${JSON.stringify(searchIndex(g))};`;
			if (name === `${PREFIX}/changelog`) {
				return `export default ${JSON.stringify(readChangelog(REPO, 1).releases)};`;
			}
			const slug = name.slice(`${PREFIX}/page/`.length);
			const page = g.pages.get(slug);
			if (!page) this.error(`unknown docs page ${slug}`);
			return `export default ${JSON.stringify(page.blocks)};`;
		},
		configureServer(server) {
			server.watcher.add([GUIDE, resolve(REPO, 'CHANGELOG.md')]);
			server.watcher.on('all', (_event, file) => {
				// Only Markdown: figure pictures written into docs/guide must not reload.
				if (!file.endsWith('.md') || (!file.startsWith(GUIDE) && !file.endsWith('CHANGELOG.md'))) return;
				guide = null;
				for (const mod of server.moduleGraph.idToModuleMap.values()) if (mod.id?.startsWith('\0' + PREFIX)) server.moduleGraph.invalidateModule(mod);
				server.ws.send({ type: 'full-reload' });
			});
		},
		buildStart() {
			guide = null;
		}
	};
}

export default defineConfig({
	plugins: [docs(), tailwindcss(), sveltekit()],
	server: {
		// `npm run dev` proxies the API to a locally running `rivetpanel`.
		proxy: { '/api': 'http://127.0.0.1:8080' }
	}
});
