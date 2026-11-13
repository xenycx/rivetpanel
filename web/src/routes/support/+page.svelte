<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { can, session } from '$lib/session.svelte';
	import { fmtWhen } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import TicketThread from '$lib/components/TicketThread.svelte';
	import type { KBArticle } from '$lib/api/kb';
	import {
		type Ticket,
		type TicketView,
		ticketCategories,
		ticketPriorities,
		priorityLabel,
		statusLabel,
		staffStatusLabel,
		staffStatusShort,
		statusTone,
		categoryLabel,
		drafts,
		idleTone,
		fmtIdle,
		shortcutAllowed
	} from '$lib/api/support';

	const isStaff = $derived(can('tickets.view_all') || can('tickets.manage'));
	const canOpen = $derived(can('tickets.create'));
	const queue = $derived(page.url.searchParams.get('queue') === 'all' && isStaff);
	const me = $derived(session.user?.id);

	let tickets = $state<Ticket[]>([]);
	let loaded = $state(false);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	// The whole list (open and closed, up to the server's limit) loads once;
	// tabs, search and the assignee filter narrow it here, so they answer at
	// once and every tab can show its count.
	async function load() {
		error = '';
		try {
			const q = new URLSearchParams({ scope: queue ? 'all' : 'mine' });
			tickets = (await api<{ tickets: Ticket[] }>('GET', `/tickets?${q}`)).tickets;
		} catch (e) {
			error = msg(e);
			tickets = [];
		} finally {
			loaded = true;
		}
	}
	$effect(() => {
		void queue;
		untrack(() => {
			loaded = false;
			kept.clear();
			load();
		});
	});

	// ---- Filters -------------------------------------------------------------
	type Tab = 'active' | 'open' | 'pending' | 'resolved' | 'closed' | 'any';
	const tabs: Tab[] = ['active', 'open', 'pending', 'resolved', 'closed', 'any'];
	let tab = $state<Tab>('active');
	let assignee = $state<'any' | 'me' | 'none'>('any');
	let query = $state('');
	// Tickets changed from the desk stay in view until the filter changes,
	// so resolving one does not pull the row from under the cursor.
	const kept = new SvelteSet<string>();
	const statusNames = $derived(queue ? staffStatusShort : statusLabel);
	const tabLabel = (k: Tab) => (k === 'active' ? 'Active' : k === 'any' ? 'All' : statusNames[k]);
	const inTab = (t: Ticket, k: Tab) => k === 'any' || (k === 'active' ? t.status === 'open' || t.status === 'pending' : t.status === k);

	const narrowed = $derived.by(() => {
		const q = query.trim().toLowerCase().replace(/^#/, '');
		return tickets.filter((t) => {
			if (assignee === 'me' && t.assignee_id !== me) return false;
			if (assignee === 'none' && t.assignee_id) return false;
			if (!q) return true;
			return [String(t.number), t.subject, t.requester_email, t.bot_name, t.assignee_email, categoryLabel(t.category)].some((f) => f?.toLowerCase().includes(q));
		});
	});
	const counts = $derived(Object.fromEntries(tabs.map((k) => [k, narrowed.filter((t) => inTab(t, k)).length])) as Record<Tab, number>);
	const shown = $derived(narrowed.filter((t) => inTab(t, tab) || kept.has(t.id)));
	function setTab(k: Tab) {
		tab = k;
		kept.clear();
	}

	// ---- The triage desk (staff queue on a wide pane) --------------------------
	let deskWidth = $state(0);
	const split = $derived(queue && deskWidth >= 880);
	let selected = $state<string | null>(null);
	const cache = new SvelteMap<string, TicketView>();
	const inflight = new Map<string, Promise<TicketView | null>>();
	let listEl = $state<HTMLElement>();
	// Where the desk starts on the page, so it can fill the rest of the window.
	let deskBody = $state<HTMLElement>();
	let deskTop = $state(0);
	$effect(() => {
		if (!deskBody) return;
		const el = deskBody;
		const measure = () => (deskTop = Math.round(el.getBoundingClientRect().top + scrollY));
		measure();
		const ro = new ResizeObserver(measure);
		ro.observe(document.body);
		return () => ro.disconnect();
	});
	let searchEl = $state<HTMLInputElement>();
	let now = $state(Date.now());

	function fetchView(id: string) {
		let p = inflight.get(id);
		if (!p) {
			p = api<TicketView>('GET', `/tickets/${id}`)
				.then((v) => (cache.set(id, v), v))
				.catch(() => null)
				.finally(() => inflight.delete(id));
			inflight.set(id, p);
		}
		return p;
	}

	// The thread reports every change: keep the row and the cache in step.
	function onview(v: TicketView) {
		cache.set(v.ticket.id, v);
		const i = tickets.findIndex((t) => t.id === v.ticket.id);
		if (i < 0) return;
		const prev = tickets[i];
		if (!inTab(v.ticket, tab)) kept.add(v.ticket.id);
		tickets[i] = { ...prev, ...v.ticket, messages: v.messages.filter((m) => m.kind === 'message').length };
	}

	function syncUrl(id: string | null) {
		// Keep the open ticket in the address (reload and back land on it)
		// without a navigation, which would fade the page.
		const u = new URL(location.href);
		if (id) u.searchParams.set('t', id);
		else u.searchParams.delete('t');
		if (u.href !== location.href) history.replaceState(history.state, '', u);
	}

	const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
	let want: string | null = null;
	/** Show a ticket in the pane; dir is the direction of travel in the list. */
	async function select(id: string, dir = 0, focus = false) {
		if (id === selected) return;
		want = id;
		// Wait briefly for the thread so the pane swaps once, to the real thing.
		if (!cache.has(id)) await Promise.race([fetchView(id), new Promise((r) => setTimeout(r, 280))]);
		if (want !== id) return;
		const swap = async () => {
			selected = id;
			await tick();
			if (focus) rowEl(id)?.focus({ preventScroll: true });
			rowEl(id)?.scrollIntoView({ block: 'nearest' });
		};
		const root = document.documentElement;
		// The first ticket simply appears; only moving between tickets travels.
		if (!selected || !document.startViewTransition || reduced() || document.visibilityState !== 'visible') {
			await swap();
		} else {
			root.dataset.desk = dir > 0 ? 'down' : dir < 0 ? 'up' : 'swap';
			tagDesk();
			const vt = document.startViewTransition(async () => {
				untagDesk();
				await swap();
				tagDesk();
			});
			vt.ready.catch(() => {});
			vt.updateCallbackDone.catch(() => {});
			vt.finished
				.finally(() => {
					untagDesk();
					if (want === id) delete root.dataset.desk;
				})
				.catch(() => {});
		}
		syncUrl(id);
		// Warm the neighbours so the next j or k swaps instantly.
		const i = shown.findIndex((t) => t.id === id);
		for (const n of [shown[i + 1], shown[i - 1]]) if (n && !cache.has(n.id)) fetchView(n.id);
	}
	const rowEl = (id: string) => listEl?.querySelector<HTMLElement>(`[data-ticket="${CSS.escape(id)}"]`);
	// Names live only for one transition: the selection slab and the pane.
	function tagDesk() {
		const ink = listEl?.querySelector<HTMLElement>('.ticket-ink');
		const pane = document.querySelector<HTMLElement>('.desk-pane');
		if (ink) ink.style.viewTransitionName = 'vt-ticket-ink';
		if (pane) pane.style.viewTransitionName = 'vt-ticket-pane';
	}
	function untagDesk() {
		document.querySelectorAll<HTMLElement>('.ticket-ink, .desk-pane').forEach((el) => (el.style.viewTransitionName = ''));
	}

	function step(dir: 1 | -1) {
		if (!shown.length) return;
		const i = shown.findIndex((t) => t.id === selected);
		const next = i < 0 ? shown[0] : shown[Math.min(shown.length - 1, Math.max(0, i + dir))];
		if (next.id !== selected) select(next.id, i < 0 ? 0 : dir, true);
	}

	// Open the ticket named in the address, or the first one, once the list is in.
	$effect(() => {
		if (!split || !loaded) return;
		untrack(() => {
			if (selected && shown.some((t) => t.id === selected)) return;
			const fromUrl = page.url.searchParams.get('t');
			const pick = (fromUrl && tickets.find((t) => t.id === fromUrl)?.id) || shown[0]?.id || null;
			if (pick) select(pick);
			else selected = null;
		});
	});

	function key(e: KeyboardEvent) {
		if (e.key === '/' && shortcutAllowed(e)) {
			e.preventDefault();
			searchEl?.focus();
			searchEl?.select();
			return;
		}
		if (!split || !shortcutAllowed(e)) return;
		const onRow = e.target instanceof HTMLElement && !!e.target.closest('[data-ticket]');
		if (e.key === 'j' || (onRow && e.key === 'ArrowDown')) {
			e.preventDefault();
			step(1);
		} else if (e.key === 'k' || (onRow && e.key === 'ArrowUp')) {
			e.preventDefault();
			step(-1);
		} else if (e.key === 'Enter' && onRow && selected) {
			e.preventDefault();
			goto(`/support/${selected}`);
		}
	}
	function searchKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			if (query) query = '';
			else backToList();
		} else if (e.key === 'Enter' || e.key === 'ArrowDown') {
			e.preventDefault();
			if (split && shown[0]) select(shown[0].id, 0, true);
			else backToList();
		}
	}
	function backToList() {
		const r = selected ? rowEl(selected) : null;
		if (r) r.focus();
		else searchEl?.blur();
	}

	onMount(() => {
		const clock = setInterval(() => (now = Date.now()), 30_000);
		const back = () => document.visibilityState === 'visible' && (now = Date.now(), load());
		document.addEventListener('visibilitychange', back);
		return () => {
			clearInterval(clock);
			document.removeEventListener('visibilitychange', back);
		};
	});

	// ---- New ticket ------------------------------------------------------------
	let composing = $state(false);
	let bots = $state<Bot[]>([]);
	let form = $state({ subject: '', category: 'general', priority: 'normal', bot_id: '', body: '' });
	let sending = $state(false);
	// Opened from the "New" menu: start with the new-ticket form.
	$effect(() => {
		if (canOpen && !queue && untrack(() => !composing) && page.url.searchParams.get('new') === '1') untrack(() => startNew());
	});
	async function startNew() {
		composing = true;
		try {
			bots = (await api<{ bots: Bot[] }>('GET', '/bots')).bots;
		} catch {
			bots = [];
		}
	}
	async function submit(e: SubmitEvent) {
		e.preventDefault();
		sending = true;
		error = '';
		try {
			const t = await api<Ticket>('POST', '/tickets', form);
			goto(`/support/${t.id}`);
		} catch (err) {
			error = msg(err);
		} finally {
			sending = false;
		}
	}

	// Related help articles while the subject is typed (the help center's
	// own visibility rules apply; nothing is shown when it has no match).
	let related = $state<KBArticle[]>([]);
	let relatedTimer: ReturnType<typeof setTimeout> | undefined;
	function suggest() {
		clearTimeout(relatedTimer);
		const q = form.subject.trim();
		if (q.length < 4) {
			related = [];
			return;
		}
		relatedTimer = setTimeout(async () => {
			try {
				related = (await api<{ results: KBArticle[] }>('GET', `/kb/search?suggest=1&limit=5&q=${encodeURIComponent(q)}`)).results;
			} catch {
				related = []; // the help center is optional
			}
		}, 350);
	}

	const idleTitle = (t: Ticket) =>
		t.status === 'open' ? `Waiting for staff; no activity for ${fmtIdle(t.updated_at_ms, now)} (since ${fmtWhen(t.updated_at_ms)})` : `Last activity ${fmtWhen(t.updated_at_ms)}`;
	const who = (t: Ticket) => (t.assignee_id ? (t.assignee_id === me ? 'You' : t.assignee_email) : 'Unassigned');
</script>

<svelte:window onkeydown={key} />
<svelte:head><title>{queue ? 'Support queue' : 'Support'} · RivetPanel</title></svelte:head>

<header class="flex flex-wrap items-end gap-3 border-b border-rule-soft pb-5">
	<div class="min-w-0 flex-[1_1_18rem]">
		<h1 class="text-page">{queue ? 'Support queue' : 'Support'}</h1>
		<p class="mt-0.5 text-muted">{queue ? 'Every ticket you may answer or read.' : 'Ask the people who run this panel for help. Only you and the support team see your tickets.'}</p>
	</div>
	{#if isStaff}
		<nav class="seg" aria-label="Ticket views">
			{#if canOpen}<a class="seg-item" href="/support" aria-current={!queue ? 'page' : undefined}>My tickets</a>{/if}
			<a class="seg-item" href="/support?queue=all" aria-current={queue ? 'page' : undefined}>Queue</a>
		</nav>
	{/if}
	{#if canOpen && !queue}<button class="btn btn-primary" onclick={startNew}><Icon name="plus" />New ticket</button>{/if}
</header>

{#if composing}
	<form class="card mt-6 grid gap-4 p-5" onsubmit={submit}>
		<h2 class="text-title font-semibold">New ticket</h2>
		<label class="block"><span class="label">Subject</span><input class="field" required maxlength="150" bind:value={form.subject} oninput={suggest} placeholder="My server does not start after the update" /></label>
		{#if related.length}
			<aside class="rounded-tile border border-action/25 bg-action/6 px-4 py-3" aria-live="polite">
				<p class="text-small font-medium">These help articles may answer it</p>
				<ul class="mt-1.5 grid gap-1">
					{#each related as a (a.id)}<li class="text-small"><a class="link" href="/help/{a.slug}" target="_blank" rel="noopener">{a.title}</a>{#if a.excerpt}<span class="text-muted"> — {a.excerpt}</span>{/if}</li>{/each}
				</ul>
			</aside>
		{/if}
		<div class="grid gap-4 sm:grid-cols-3">
			<label class="block"><span class="label">Category</span>
				<select class="field" bind:value={form.category}>{#each ticketCategories as c (c.value)}<option value={c.value}>{c.label}</option>{/each}</select>
			</label>
			<label class="block"><span class="label">Priority</span>
				<select class="field" bind:value={form.priority}>{#each ticketPriorities as p (p)}<option value={p}>{priorityLabel(p)}</option>{/each}</select>
			</label>
			<label class="block"><span class="label">About (optional)</span>
				<select class="field" bind:value={form.bot_id}>
					<option value="">Nothing specific</option>
					{#each bots as b (b.id)}<option value={b.id}>{b.name}</option>{/each}
				</select>
			</label>
		</div>
		<label class="block"><span class="label">Message</span><textarea class="field min-h-36" required maxlength="10000" bind:value={form.body} placeholder="What happened, what you expected, and what you already tried."></textarea></label>
		<p class="text-small text-muted">Do not paste passwords, tokens or other secrets. Staff can see the linked bot's name, not its files.</p>
		<div class="flex gap-2">
			<button class="btn btn-primary" disabled={sending}>{sending ? 'Sending…' : 'Open ticket'}</button>
			<button type="button" class="btn btn-quiet" onclick={() => (composing = false)}>Cancel</button>
		</div>
	</form>
{/if}

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

<div class="desk" bind:clientWidth={deskWidth}>
	<div class="mt-6 flex flex-wrap items-center gap-x-3 gap-y-2">
		<div role="tablist" aria-label="Status" class="seg">
			{#each tabs as k (k)}
				<button role="tab" type="button" class="seg-item" aria-selected={tab === k} onclick={() => setTab(k)}>
					{tabLabel(k)}<span class="tab-count tabular-nums" class:zero={!counts[k]}>{counts[k]}</span>
				</button>
			{/each}
		</div>
		<label class="search relative ml-auto flex min-w-48 flex-[0_1_18rem] items-center">
			<span class="sr-only">Search tickets</span>
			<Icon name="search" size={14} class="pointer-events-none absolute left-2.5 text-muted" />
			<input bind:this={searchEl} bind:value={query} onkeydown={searchKey} type="search" class="field h-8 w-full py-0 pr-8 pl-8" placeholder={queue ? 'Subject, #number, email, app' : 'Subject, #number, app'} autocomplete="off" />
			<kbd class="slash-hint pointer-events-none absolute right-2" aria-hidden="true">/</kbd>
		</label>
		{#if queue}
			<label class="flex items-center gap-2 text-small"><span class="text-muted">Assignee</span>
				<select class="field h-8 w-auto py-0" bind:value={assignee} onchange={() => kept.clear()}>
					<option value="any">Anyone</option>
					<option value="me">Me</option>
					<option value="none">Nobody</option>
				</select>
			</label>
		{/if}
	</div>

	{#snippet row(t: Ticket)}
		{@const tone = idleTone(t, now)}
		{@const draft = drafts.get(t.id)}
		<a
			class="ticket-row relative block px-4 py-3"
			href="/support/{t.id}"
			data-ticket={t.id}
			aria-current={split && selected === t.id ? 'true' : undefined}
			class:dim={kept.has(t.id) && !inTab(t, tab)}
			onclick={(e) => {
				if (!split || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
				e.preventDefault();
				const i = shown.findIndex((x) => x.id === selected);
				const j = shown.findIndex((x) => x.id === t.id);
				select(t.id, i < 0 ? 0 : Math.sign(j - i));
			}}
		>
			{#if split && selected === t.id}<span class="ticket-ink" aria-hidden="true"></span>{/if}
			<span class="relative flex items-baseline gap-2">
				<span class="shrink-0 font-mono text-small text-muted tabular-nums">#{t.number}</span>
				<span class="min-w-0 flex-1 truncate font-medium" title={t.subject}>{t.subject}</span>
				<time class="idle shrink-0 text-small tabular-nums" data-tone={tone} datetime={new Date(t.updated_at_ms).toISOString()} title={idleTitle(t)}>
					{#if tone !== 'calm'}<Icon name="clock" size={12} class="mr-0.5 inline align-[-1px]" />{/if}{fmtIdle(t.updated_at_ms, now)}
				</time>
			</span>
			<span class="relative mt-1 flex items-center gap-2 text-small text-muted">
				{#if t.priority === 'urgent' || t.priority === 'high'}<span class="prio shrink-0 font-medium" data-p={t.priority}>{priorityLabel(t.priority)}</span>{/if}
				<span class="min-w-0 flex-1 truncate">
					{[queue ? t.requester_email : categoryLabel(t.category), t.bot_name, queue && who(t)].filter(Boolean).join(' · ')}
				</span>
				{#if draft}<span class="flex shrink-0 items-center gap-1 text-action" title="You have an unsent {draft.internal ? 'note' : 'reply'}"><Icon name="pencil" size={11} />Draft</span>{/if}
				{#if tab === 'any' || tab === 'active' || kept.has(t.id)}
					<span class="shrink-0 rounded-pill border px-1.5 text-label {statusTone[t.status]}">{(queue ? staffStatusShort : statusLabel)[t.status]}</span>
				{/if}
			</span>
		</a>
	{/snippet}

	{#if loaded && !tickets.length}
		<div class="mt-4">
			<EmptyState title="No tickets">
				{#if queue}Nobody has asked for help yet.{:else if canOpen}Open a ticket when you need help; answers also arrive under the bell in the top bar.{:else}Your role cannot open support tickets.{/if}
			</EmptyState>
		</div>
	{:else if split}
		<div bind:this={deskBody} style:--desk-top="{deskTop}px" class="desk-body mt-4 grid grid-cols-[minmax(18rem,23rem)_minmax(0,1fr)] gap-4">
			<div class="list-col card flex min-h-0 flex-col overflow-hidden">
				<ul bind:this={listEl} class="list-scroll min-h-0 flex-1 divide-y divide-rule-soft overflow-y-auto" aria-label="Tickets">
					{#each shown as t (t.id)}<li>{@render row(t)}</li>{/each}
					{#if loaded && !shown.length}<li class="px-4 py-10 text-center text-small text-muted">{query ? `Nothing matches “${query}”.` : 'Nothing here. Clear the filters or pick another tab.'}</li>{/if}
				</ul>
				<p class="legend border-t border-rule-soft px-4 py-2 text-label text-muted" aria-label="Keyboard shortcuts">
					<span><kbd>J</kbd> <kbd>K</kbd> move</span><span><kbd>R</kbd> reply</span><span><kbd>N</kbd> note</span><span><kbd>1</kbd>–<kbd>4</kbd> status</span><span><kbd>A</kbd> take</span><span><kbd>/</kbd> search</span>
				</p>
			</div>
			<section class="desk-pane card min-h-0 overflow-hidden" aria-label="Selected ticket">
				{#if selected}
					{#key selected}
						<TicketThread id={selected} embedded initial={cache.get(selected) ?? null} {onview} onescape={backToList} />
					{/key}
				{:else}
					<div class="grid h-full place-items-center p-8 text-center text-muted">
						<p class="max-w-[32ch]">{loaded ? 'Pick a ticket on the left, or press J to start at the top.' : 'Loading the queue…'}</p>
					</div>
				{/if}
			</section>
		</div>
	{:else}
		<ul class="list-card mt-4" aria-label="Tickets">
			{#each shown as t (t.id)}<li>{@render row(t)}</li>{/each}
			{#if loaded && !shown.length}<li class="px-4 py-10 text-center text-small text-muted">{query ? `Nothing matches “${query}”.` : 'Nothing here. Pick another tab.'}</li>{/if}
		</ul>
	{/if}
</div>

<style>
	.tab-count {
		margin-left: 0.125rem;
		min-width: 1.25rem;
		border-radius: var(--radius-pill);
		padding: 0 0.3rem;
		font-size: 11px;
		line-height: 1rem;
		text-align: center;
		background: color-mix(in srgb, var(--color-ink) 7%, transparent);
	}
	.tab-count.zero {
		opacity: 0.55;
	}
	.slash-hint,
	.legend kbd {
		display: inline-block;
		min-width: 1.1rem;
		border: 1px solid var(--color-rule);
		border-radius: var(--radius-inner);
		padding: 0 0.25rem;
		font-family: var(--font-mono);
		font-size: 10.5px;
		line-height: 1rem;
		text-align: center;
		color: var(--color-muted);
	}
	.search:focus-within .slash-hint {
		opacity: 0;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
	}
	.legend > span {
		white-space: nowrap;
	}
	@media (pointer: coarse) {
		.legend,
		.slash-hint {
			display: none;
		}
	}

	/* Rows */
	.ticket-row {
		outline: none;
		transition: background-color 120ms;
	}
	.ticket-row:hover {
		background: color-mix(in srgb, var(--color-ink) 3.5%, transparent);
	}
	.ticket-row:focus-visible:not([aria-current]) {
		box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--color-action) 60%, transparent);
	}
	.ticket-row.dim {
		opacity: 0.62;
	}
	/* The selection is a slab with the sidebar's accent tick; a View
	   Transition slides it from row to row (see app.css, vt-ticket-ink).
	   It is translucent, so rows stay legible under it while it travels. */
	.ticket-ink {
		position: absolute;
		inset: 0;
		background: color-mix(in srgb, var(--color-action) 9%, transparent);
		pointer-events: none;
	}
	.ticket-ink::before {
		content: '';
		position: absolute;
		inset: 0.6rem auto 0.6rem 0.3rem;
		width: 3px;
		border-radius: var(--radius-pill);
		background: var(--color-action);
	}
	.idle {
		color: var(--color-muted);
	}
	.idle[data-tone='warn'] {
		color: var(--color-warn);
		font-weight: 500;
	}
	.idle[data-tone='fail'] {
		color: var(--color-fail);
		font-weight: 600;
	}
	.prio[data-p='high'] {
		color: var(--color-warn);
	}
	.prio[data-p='urgent'] {
		color: var(--color-fail);
	}

	/* The desk fills the window under the top bar once scrolled to it: the
	   list and the open thread scroll inside, the composer stays in reach. */
	.desk-body {
		position: sticky;
		top: calc(3.5rem + 1px + 1rem);
		/* Fill the window below where the desk starts; on a short window the
		   page scrolls until the desk docks under the top bar. */
		height: max(30rem, min(calc(100dvh - var(--desk-top, 0px) - 1.5rem), calc(100dvh - 3.5rem - 1px - 2rem)));
	}
	.list-scroll {
		scrollbar-width: thin;
		scrollbar-color: var(--color-rule) transparent;
		overscroll-behavior: contain;
	}
</style>
