// Chart colours come from the panel's CSS custom properties, so a chart
// follows light/dark and the accent choice. Canvas needs concrete colours:
// each token is read from the root element and normalised to rgb.

export type Tone = 'action' | 'data' | 'run' | 'warn' | 'fail' | 'muted' | 'ink';

/**
 * Categorical colours for "per app" charts (light, dark). The accent pairs of
 * Appearance, tuned for pale and dark surfaces, in an order where
 * neighbours differ in hue.
 */
const categorical: [string, string][] = [
	['#2458d3', '#6f9cff'], // ocean
	['#076c5c', '#3ecf9e'], // teal
	['#8c5a00', '#f0ad3d'], // amber
	['#6945bd', '#a98bff'], // violet
	['#ad3b55', '#ff758c'], // rose
	['#116f86', '#4cc9e8'], // cyan
	['#577515', '#a4d65e'], // lime
	['#9a3f7c', '#ed7bc4'], // orchid
	['#8d5142', '#db8b76'], // clay
	['#4652b5', '#8792ff'] // indigo
];

let probe: CanvasRenderingContext2D | null = null;
/** Normalises any CSS colour to [r, g, b]. */
function rgbOf(color: string): [number, number, number] {
	probe ??= document.createElement('canvas').getContext('2d');
	if (!probe) return [128, 128, 128];
	probe.fillStyle = '#808080';
	probe.fillStyle = color.trim() || '#808080';
	const v = String(probe.fillStyle);
	if (v.startsWith('#')) return [parseInt(v.slice(1, 3), 16), parseInt(v.slice(3, 5), 16), parseInt(v.slice(5, 7), 16)];
	const m = v.match(/[\d.]+/g);
	return m ? [+m[0], +m[1], +m[2]] : [128, 128, 128];
}

export type Palette = {
	dark: boolean;
	panel: [number, number, number];
	ink: string;
	muted: string;
	rule: string;
	ruleSoft: string;
	font: string;
	/** Solid colour of a token, 'cat:<n>' or a CSS colour. */
	color: (c: string | undefined) => string;
	/** The same colour with alpha. */
	alpha: (c: string | undefined, a: number) => string;
	/** The colour blended into the panel background (opaque, for stacked bands). */
	tint: (c: string | undefined, amount: number) => string;
};

export function readPalette(): Palette {
	const cs = getComputedStyle(document.documentElement);
	const v = (n: string) => cs.getPropertyValue(n).trim();
	const dark = document.documentElement.getAttribute('data-theme') === 'dark';
	const cache = new Map<string, [number, number, number]>();
	const rgb = (c: string | undefined): [number, number, number] => {
		const key = c ?? 'data';
		let hit = cache.get(key);
		if (hit) return hit;
		let css: string;
		if (key.startsWith('cat:')) {
			const pair = categorical[(Number(key.slice(4)) || 0) % categorical.length];
			css = dark ? pair[1] : pair[0];
		} else if (/^[a-z-]+$/.test(key)) css = v(`--color-${key}`) || '#808080';
		else css = key;
		hit = rgbOf(css);
		cache.set(key, hit);
		return hit;
	};
	const panel = rgbOf(v('--color-panel') || (dark ? '#151516' : '#ffffff'));
	const str = ([r, g, b]: number[]) => `rgb(${r},${g},${b})`;
	return {
		dark,
		panel,
		ink: str(rgb('ink')),
		muted: str(rgb('muted')),
		rule: str(rgb('rule')),
		ruleSoft: str(rgb('rule-soft')),
		font: v('--font-mono') || 'monospace',
		color: (c) => str(rgb(c)),
		alpha: (c, a) => {
			const [r, g, b] = rgb(c);
			return `rgba(${r},${g},${b},${a})`;
		},
		tint: (c, amount) => {
			const x = rgb(c);
			return str(x.map((n, i) => Math.round(n * amount + panel[i] * (1 - amount))));
		}
	};
}

/** The CSS colour of categorical colour n (for HTML swatches). */
export function categoricalCss(n: number, dark: boolean): string {
	const pair = categorical[n % categorical.length];
	return dark ? pair[1] : pair[0];
}

/** Categorical colour key for the n-th app. */
export const cat = (n: number) => `cat:${n}`;
