// Virtual modules generated from docs/guide by the plugin in vite.config.ts.
declare module 'virtual:docs' {
	import type { DocsManifest, Block } from '$lib/docs/types';
	export const manifest: DocsManifest;
	export const pages: Record<string, () => Promise<{ default: Block[] }>>;
}
declare module 'virtual:docs/search' {
	import type { SearchEntry } from '$lib/docs/types';
	const index: SearchEntry[];
	export default index;
}
declare module 'virtual:docs/changelog' {
	import type { Release } from '$lib/docs/types';
	const releases: Release[];
	export default releases;
}
