// Colour theme: "system" follows the operating system, "light"/"dark" pin it.
// The choice is a per-browser convenience kept in localStorage; app.html
// applies it before the first paint so the page never flashes the wrong theme.

export type ThemePref = 'system' | 'light' | 'dark';
export type Radius = (typeof radii)[number]['id'];

/** Corner styles; the id is written to <html data-radius>. */
export const radii = [
	{ id: 'none', name: 'Square', text: 'No rounding anywhere' },
	{ id: 'subtle', name: 'Subtle', text: 'Barely softened corners' },
	{ id: 'default', name: 'Default', text: 'The RivetPanel look' },
	{ id: 'round', name: 'Rounded', text: 'Soft, generous corners' }
] as const;
export type Accent = (typeof accents)[number]['id'];

export const accents = [
	{ id: 'ember', name: 'Ember', light: '#b44718', dark: '#f47a3d' },
	{ id: 'ocean', name: 'Ocean', light: '#2458d3', dark: '#6f9cff' },
	{ id: 'teal', name: 'Teal', light: '#087f6c', dark: '#3ecf9e' },
	{ id: 'violet', name: 'Violet', light: '#6945bd', dark: '#a98bff' },
	{ id: 'orchid', name: 'Orchid', light: '#9a3f7c', dark: '#ed7bc4' },
	{ id: 'rose', name: 'Rose', light: '#ad3b55', dark: '#ff758c' },
	{ id: 'amber', name: 'Amber', light: '#8c5a00', dark: '#f0ad3d' },
	{ id: 'lime', name: 'Lime', light: '#577515', dark: '#a4d65e' },
	{ id: 'cyan', name: 'Cyan', light: '#116f86', dark: '#4cc9e8' },
	{ id: 'indigo', name: 'Indigo', light: '#4652b5', dark: '#8792ff' },
	{ id: 'slate', name: 'Slate', light: '#486474', dark: '#8ba9bb' },
	{ id: 'clay', name: 'Clay', light: '#8d5142', dark: '#db8b76' }
] as const;

const KEY = 'rivetpanel.theme';
const ACCENT_KEY = 'rivetpanel.accent';
const RADIUS_KEY = 'rivetpanel.radius';

function read(): ThemePref {
	try {
		const v = localStorage.getItem(KEY);
		if (v === 'light' || v === 'dark') return v;
	} catch {
		/* storage unavailable: follow the system */
	}
	return 'system';
}

const media = typeof matchMedia === 'function' ? matchMedia('(prefers-color-scheme: dark)') : null;

function readAccent(): Accent {
	try {
		const value = localStorage.getItem(ACCENT_KEY);
		if (accents.some((accent) => accent.id === value)) return value as Accent;
	} catch {
		/* storage unavailable: use the RivetPanel accent */
	}
	return 'ember';
}

function readRadius(): Radius {
	try {
		const value = localStorage.getItem(RADIUS_KEY);
		if (radii.some((r) => r.id === value)) return value as Radius;
	} catch {
		/* storage unavailable: use the default corners */
	}
	return 'default';
}

export const theme = $state<{ pref: ThemePref; dark: boolean; accent: Accent; radius: Radius }>({ pref: read(), dark: false, accent: readAccent(), radius: readRadius() });

function apply() {
	const root = document.documentElement;
	// data-theme always holds the resolved theme, so CSS needs one selector.
	theme.dark = theme.pref === 'dark' || (theme.pref === 'system' && !!media?.matches);
	root.setAttribute('data-theme', theme.dark ? 'dark' : 'light');
	root.setAttribute('data-accent', theme.accent);
	root.setAttribute('data-radius', theme.radius);
	root.style.colorScheme = theme.dark ? 'dark' : 'light';
}

export function setTheme(p: ThemePref) {
	theme.pref = p;
	try {
		if (p === 'system') localStorage.removeItem(KEY);
		else localStorage.setItem(KEY, p);
	} catch {
		/* not persisted; still applied for this page */
	}
	apply();
}

export function setAccent(accent: Accent) {
	theme.accent = accent;
	try {
		localStorage.setItem(ACCENT_KEY, accent);
	} catch {
		/* not persisted; still applied for this page */
	}
	apply();
}

export function setRadius(radius: Radius) {
	theme.radius = radius;
	try {
		if (radius === 'default') localStorage.removeItem(RADIUS_KEY);
		else localStorage.setItem(RADIUS_KEY, radius);
	} catch {
		/* not persisted; still applied for this page */
	}
	apply();
}

/** Cycles light → dark → system, for the header toggle. */
export function cycleTheme() {
	setTheme(theme.pref === 'light' ? 'dark' : theme.pref === 'dark' ? 'system' : 'light');
}

if (typeof document !== 'undefined') {
	apply();
	media?.addEventListener('change', apply);
}
