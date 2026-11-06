import { onNavigate } from '$app/navigation';

// Navigation that keeps its place. Every client-side navigation runs inside a
// same-document View Transition: the sidebar and top bar hold still, the
// content fades through to the new page, the sidebar and
// section-tab indicators slide to their new item, and opening an app from a
// list morphs its row (icon and name) into the page header, and back again.
//
// Names are assigned only for the duration of one transition and only to
// the one element that should move, so duplicate names (which would abort a
// transition) cannot happen and long lists cost nothing. Without the View
// Transitions API navigation is instant, exactly as before; with reduced
// motion the pieces fade in place instead of travelling (see app.css).

const tagged: HTMLElement[] = [];
function tag(el: Element | null | undefined, name: string) {
	if (!(el instanceof HTMLElement) || tagged.some((t) => t.style.viewTransitionName === name)) return;
	el.style.viewTransitionName = name;
	tagged.push(el);
}
function untag() {
	for (const el of tagged.splice(0)) el.style.viewTransitionName = '';
}

function visible(el: Element) {
	const r = el.getBoundingClientRect();
	return r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth;
}

// The app a URL opens: /bots/<id> or /servers/<id>, not /bots/new.
function appId(url: URL | undefined) {
	const m = url?.pathname.match(/^\/(?:bots|servers)\/([^/]+)$/);
	return m && m[1] !== 'new' ? m[1] : null;
}

// The element for one app, preferring the row the user just activated (a
// list can show the same app in more than one place).
function find(attr: 'vt-name' | 'vt-icon', id: string, near: Element | null) {
	const sel = `[data-${attr}="${CSS.escape(id)}"]`;
	const row = near?.closest('[role="row"], li, [data-vt-row]');
	const own = row?.querySelector(sel);
	if (own && visible(own)) return own;
	return [...document.querySelectorAll(`#main ${sel}`)].find(visible) ?? null;
}

function tagSide(id: string | null, near: Element | null, sameSection: boolean) {
	if (id) {
		tag(find('vt-name', id, near), 'vt-name');
		tag(find('vt-icon', id, near), 'vt-icon');
	}
	// Tabs belong to one page: the underline slides only between tabs of the same page.
	if (sameSection) tag([...document.querySelectorAll('#main [data-tab-ink]')].find(visible), 'vt-tab-ink');
	tag([...document.querySelectorAll('aside .side-ink')].find(visible), 'vt-side-ink');
}

// Wait (briefly) for the page to render the element that should receive the
// morph: app pages draw their header once the app has loaded.
function arrival(id: string, ms = 350) {
	const sel = `#main [data-vt-name="${CSS.escape(id)}"]`;
	return new Promise<void>((done) => {
		const until = performance.now() + ms;
		// Timers, not animation frames: rendering is paused while a transition
		// waits for its new state, and frame callbacks may be held with it.
		const look = () => (document.querySelector(sel) || performance.now() > until ? done() : setTimeout(look, 16));
		look();
	});
}

export function fluidNavigation() {
	let near: Element | null = null;
	let running = 0;
	if (typeof document !== 'undefined') {
		// Remember what was activated last, so the morph starts from that row.
		document.addEventListener('click', (e) => (near = e.target instanceof Element ? e.target : null), true);
	}

	onNavigate((nav) => {
		if (!document.startViewTransition || nav.willUnload || !nav.from || !nav.to || document.visibilityState !== 'visible') return;
		const from = nav.from.url;
		const to = nav.to.url;
		if (from.pathname === to.pathname && from.search === to.search) return;

		untag();
		const sameSection = from.pathname === to.pathname;
		const outId = appId(from);
		const inId = appId(to);
		// Row → app page, or app page → back to a list (the same app either way).
		const id = inId && !sameSection ? inId : outId && !inId ? outId : null;
		tagSide(id, near, sameSection);
		near = null;

		const root = document.documentElement;
		running++;
		root.dataset.navigating = '';
		return new Promise<void>((resolve) => {
			const vt = document.startViewTransition(async () => {
				untag();
				resolve();
				await nav.complete;
				if (id) await arrival(id, inId ? 350 : 250);
				tagSide(id, null, sameSection);
			});
			// A transition interrupted by the next navigation is skipped; that is expected.
			vt.ready.catch(() => {});
			vt.updateCallbackDone.catch(() => {});
			vt.finished
				.finally(() => {
					untag();
					if (--running === 0) delete root.dataset.navigating;
				})
				.catch(() => {});
		});
	});
}
