import { tick } from 'svelte';

// Shared-element motion inside a page (lib/ui/motion.ts covers navigation).
// An element travels from where it was to where the update puts it, through
// a same-document View Transition that leaves the rest of the page still
// (the root is not cross-faded, see :root[data-desk] in app.css). Without
// the View Transitions API, with reduced motion or in a hidden tab, the
// update simply happens.

const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
/**
 * A transition waits for a rendered frame before it runs its update; a tab
 * that is not painting (in the background, or a hidden pane) would hold the
 * change back. After a short wait the transition is skipped, which still
 * applies the update, just without motion.
 */
export function guard(vt: ViewTransition, ms = 400) {
	const t = setTimeout(() => vt.skipTransition(), ms);
	vt.updateCallbackDone.finally(() => clearTimeout(t)).catch(() => {});
	return vt;
}

export const canGlide = () => typeof document !== 'undefined' && !!document.startViewTransition && !reduced() && document.visibilityState === 'visible';

/**
 * Move one element: `from` (the card, or the drag ghost) morphs into what
 * `to()` returns after `update` ran. Resolves once the DOM is updated.
 */
export async function glide(name: string, from: HTMLElement | null, update: () => unknown, to: () => HTMLElement | null) {
	if (!from || !canGlide()) {
		await update();
		return;
	}
	const root = document.documentElement;
	root.dataset.desk = 'glide';
	from.style.viewTransitionName = name;
	let target: HTMLElement | null = null;
	const vt = guard(document.startViewTransition(async () => {
		from.style.viewTransitionName = '';
		await update();
		await tick();
		target = to();
		if (target) target.style.viewTransitionName = name;
	}));
	vt.ready.catch(() => {});
	vt.finished
		.finally(() => {
			if (target) target.style.viewTransitionName = '';
			if (root.dataset.desk === 'glide') delete root.dataset.desk;
		})
		.catch(() => {});
	await vt.updateCallbackDone.catch(() => {});
}

/**
 * Reorder a list: every row (selector, at most `limit`) keeps its identity
 * across the update, so rows slide to their new places.
 */
export async function glideList(container: HTMLElement | null, selector: string, key: (el: HTMLElement) => string, update: () => unknown, limit = 80) {
	if (!container || !canGlide()) {
		await update();
		return;
	}
	const root = document.documentElement;
	const tag = () => {
		const els = [...container.querySelectorAll<HTMLElement>(selector)].slice(0, limit);
		for (const el of els) {
			el.style.viewTransitionName = `vt-r-${key(el)}`;
			el.style.setProperty('view-transition-class', 'vt-row');
		}
		return els;
	};
	const untag = (els: HTMLElement[]) => els.forEach((el) => (el.style.viewTransitionName = ''));
	root.dataset.desk = 'list';
	let before = tag();
	let after: HTMLElement[] = [];
	const vt = guard(document.startViewTransition(async () => {
		untag(before);
		before = [];
		await update();
		await tick();
		after = tag();
	}));
	vt.ready.catch(() => {});
	vt.finished
		.finally(() => {
			untag(after);
			if (root.dataset.desk === 'list') delete root.dataset.desk;
		})
		.catch(() => {});
	await vt.updateCallbackDone.catch(() => {});
}

/** Switch views with the page's own fade-through (no shared elements). */
export async function crossfade(update: () => unknown) {
	if (!canGlide()) {
		await update();
		return;
	}
	const vt = guard(document.startViewTransition(async () => {
		await update();
		await tick();
	}));
	vt.ready.catch(() => {});
	vt.finished.catch(() => {});
	await vt.updateCallbackDone.catch(() => {});
}

/** A short scale pulse acknowledging a toggle (skipped with reduced motion). */
export function pulse(el: Element | null) {
	if (!el || reduced() || !(el instanceof HTMLElement)) return;
	el.animate([{ transform: 'scale(1)' }, { transform: 'scale(1.16)' }, { transform: 'scale(1)' }], { duration: 260, easing: 'cubic-bezier(0.16, 1, 0.3, 1)' });
}
