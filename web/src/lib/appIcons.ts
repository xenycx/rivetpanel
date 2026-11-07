// Icons of one-click apps, by template id. The images come from Dashboard
// Icons (Apache-2.0); see src/lib/assets/apps/NOTICE. Apps without an icon
// show their initial on a tinted tile.
const files = import.meta.glob('$lib/assets/apps/*.{svg,webp}', { eager: true, query: '?url', import: 'default' }) as Record<string, string>;

const byName: Record<string, string> = {};
for (const [path, url] of Object.entries(files)) {
	const name = path.split('/').pop()!.replace(/\.(svg|webp)$/, '');
	byName[name] = url;
}

// Template ids whose icon file has another name.
const alias: Record<string, string> = { actual: 'actual-budget', wikijs: 'wikijs' };

/** The icon URL of an app template, or '' when it has none. */
export function appIcon(id: string | undefined | null, dark = false): string {
	if (!id) return '';
	const name = alias[id] ?? id;
	return (dark && byName[name + '-light']) || byName[name] || '';
}
