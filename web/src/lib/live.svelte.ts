import { tabId } from '$lib/api/client';

// Live updates: one Server-Sent Events stream per tab (GET /api/v1/live)
// carries "something changed" hints by topic — "tickets", "bots",
// "notifications", "admin/users"… — and views re-fetch what they show.
// Hints carry no data. A tab ignores hints about its own changes (it
// already shows them). After the stream drops and comes back, every
// listener refreshes once, since hints may have been missed meanwhile.

type Listener = { topics: string[] | null; fn: () => void; wait: number; timer?: ReturnType<typeof setTimeout> };

const listeners = new Set<Listener>();
let source: EventSource | null = null;
let dropped = false;

export const live = $state({ connected: false });

const matches = (l: Listener, topic: string) => !l.topics || l.topics.some((t) => t === topic || (t.endsWith('/*') && topic.startsWith(t.slice(0, -1))));

function fire(l: Listener) {
	clearTimeout(l.timer);
	// Changes arrive in bursts (a reply also notifies, assigns…): one re-fetch.
	l.timer = setTimeout(() => {
		if (document.visibilityState === 'visible') l.fn();
		else pendingWhileHidden.add(l);
	}, l.wait);
}
const pendingWhileHidden = new Set<Listener>();

function open() {
	if (source || typeof EventSource === 'undefined') return;
	source = new EventSource('/api/v1/live');
	source.addEventListener('ready', () => {
		live.connected = true;
		if (dropped) {
			dropped = false;
			for (const l of listeners) fire(l);
		}
	});
	source.addEventListener('change', (e) => {
		let d: { topic?: string; origin?: string } = {};
		try {
			d = JSON.parse((e as MessageEvent).data);
		} catch {
			return;
		}
		if (!d.topic || (d.origin && d.origin === tabId)) return;
		for (const l of listeners) if (matches(l, d.topic)) fire(l);
	});
	source.onerror = () => {
		live.connected = false;
		dropped = true;
		// The browser reconnects by itself; a closed stream (signed out,
		// refused) is reopened later by the next listener.
		if (source?.readyState === EventSource.CLOSED) {
			source = null;
			setTimeout(() => listeners.size && open(), 10_000);
		}
	};
}

function close() {
	source?.close();
	source = null;
	live.connected = false;
}

if (typeof document !== 'undefined') {
	document.addEventListener('visibilitychange', () => {
		if (document.visibilityState !== 'visible') return;
		for (const l of pendingWhileHidden) l.fn();
		pendingWhileHidden.clear();
	});
}

/**
 * Call fn when one of the topics changes (null or ['*']: any change).
 * A topic ending in "/*" matches a family ("admin/*"). Returns the
 * unsubscribe function, for onMount's or $effect's cleanup.
 */
export function onLive(topics: string[] | null, fn: () => void, wait = 250): () => void {
	const l: Listener = { topics: topics && !topics.includes('*') ? topics : null, fn, wait };
	listeners.add(l);
	open();
	return () => {
		clearTimeout(l.timer);
		listeners.delete(l);
		pendingWhileHidden.delete(l);
		if (!listeners.size) close();
	};
}
