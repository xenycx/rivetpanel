<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { can, session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import TicketThread from '$lib/components/TicketThread.svelte';
	import TicketRow from '$lib/components/support/TicketRow.svelte';
	import TicketTable from '$lib/components/support/TicketTable.svelte';
	import TicketBoard from '$lib/components/support/TicketBoard.svelte';
	import TicketPeek from '$lib/components/support/TicketPeek.svelte';
	import LayoutIcon from '$lib/components/support/LayoutIcon.svelte';
	import LabelChip from '$lib/components/support/LabelChip.svelte';
	import Picker from '$lib/components/support/Picker.svelte';
	import LabelManager from '$lib/components/support/LabelManager.svelte';
	import SavedReplyManager from '$lib/components/support/SavedReplyManager.svelte';
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
		categoryLabel,
		ticketKey,
		sortTickets,
		shortcutAllowed
	} from '$lib/api/support';
	import { support, loadLabels } from '$lib/supportData.svelte';
	import { supportLayouts, supportView, setSupportLayout } from '$lib/ui/supportLayout.svelte';

	const isStaff = $derived(can('tickets.view_all') || can('tickets.manage'));
	const canManage = $derived(can('tickets.manage'));
	const canOpen = $derived(can('tickets.create'));
	const queue = $derived(page.url.searchParams.get('queue') === 'all' && isStaff);
	const me = $derived(session.user?.id);

	let tickets = $state<Ticket[]>([]);
	let loaded = $state(false);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	// The whole list (open and closed, up to the server's limit) loads once;
	// tabs, search and filters narrow it here, so they answer at once and
	// every tab can show its count.
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
			selected = null;
			load();
			if (queue) loadLabels();
		});
	});

	// ---- Layout --------------------------------------------------------------------
	let deskWidth = $state(0);
	const narrow = $derived(deskWidth > 0 && deskWidth < 880);
	const layout = $derived(narrow ? 'list' : supportView.layout);

	// ---- Filters -------------------------------------------------------------------
	type Tab = 'active' | 'open' | 'pending' | 'resolved' | 'closed' | 'any';
	const tabs: Tab[] = ['active', 'open', 'pending', 'resolved', 'closed', 'any'];
	let tab = $state<Tab>('active');
	let assignee = $state<'any' | 'me' | 'none' | 'watching'>('any');
	let labelFilter = $state<string[]>([]);
	let query = $state('');
	// Tickets changed here stay in view until the filter changes, so resolving
	// one does not pull the row from under the cursor.
	const kept = new SvelteSet<string>();
	const statusNames = $derived(queue ? staffStatusShort : statusLabel);
	const tabLabel = (k: Tab) => (k === 'active' ? 'Active' : k === 'any' ? 'All' : statusNames[k]);
	const inTab = (t: Ticket, k: Tab) => k === 'any' || (k === 'active' ? t.status === 'open' || t.status === 'pending' : t.status === k);

	const narrowed = $derived.by(() => {
		const raw = query.trim().toLowerCase();
		const q = raw.replace(/^#/, '');
		const asNumber = /^(rt-?)?0*(\d+)$/.exec(q)?.[2];
		return tickets.filter((t) => {
			if (assignee === 'me' && t.assignee_id !== me) return false;
			if (assignee === 'none' && t.assignee_id) return false;
			if (assignee === 'watching' && !t.watching) return false;
			if (labelFilter.length && !labelFilter.every((l) => t.labels?.includes(l))) return false;
			if (!q) return true;
			if (asNumber && String(t.number) === asNumber) return true;
			const labelNames = (t.labels ?? []).map((id) => support.labels.find((l) => l.id === id)?.name);
			return [ticketKey(t), t.subject, t.requester_email, t.requester?.name, t.bot_name, t.assignee_email, t.assignee?.name, categoryLabel(t.category), ...labelNames].some((f) =>
				f?.toLowerCase().includes(q)
			);
		});
	});
	const counts = $derived(Object.fromEntries(tabs.map((k) => [k, narrowed.filter((t) => inTab(t, k)).length])) as Record<Tab, number>);
	const filtered = $derived(narrowed.filter((t) => inTab(t, tab) || kept.has(t.id)));
	const shown = $derived(layout === 'table' ? sortTickets(filtered, supportView.sort.key, supportView.sort.desc) : filtered);
	function setTab(k: Tab) {
		tab = k;
		kept.clear();
	}
	const labelOptions = $derived(support.labels.map((l) => ({ value: l.id, text: l.name, data: l })));

	// ---- Selection (split) and the peek panel (table, board) ---------------------
	let selected = $state<string | null>(null);
	const cache = new SvelteMap<string, TicketView>();
	const inflight = new Map<string, Promise<TicketView | null>>();
	let listEl = $state<HTMLElement>();
	let searchEl = $state<HTMLInputElement>();
	let now = $state(Date.now());

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

	// A thread reports every change: keep its row and the cache in step.
	function onview(v: TicketView) {
		cache.set(v.ticket.id, v);
		const i = tickets.findIndex((t) => t.id === v.ticket.id);
		if (i < 0) return;
		if (!inTab(v.ticket, tab)) kept.add(v.ticket.id);
		tickets[i] = { ...tickets[i], ...v.ticket, messages: v.messages.filter((m) => m.kind === 'message' && !m.deleted).length };
	}

	function syncUrl(id: string | null) {
		// Keep the open ticket in the address without a navigation (which
		// would fade the page).
		const u = new URL(location.href);
		if (id) u.searchParams.set('t', id);
		else u.searchParams.delete('t');
		if (u.href !== location.href) history.replaceState(history.state, '', u);
	}

	const reduced = () => matchMedia('(prefers-reduced-motion: reduce)').matches;
	let want: string | null = null;
	const rowEl = (id: string) => document.querySelector<HTMLElement>(`#main [data-ticket="${CSS.escape(id)}"]`);
	/** Show a ticket (split pane or peek); dir is the direction of travel in the list. */
	async function select(id: string, dir = 0, focus = false) {
		if (id === selected) return;
		want = id;
		if (!cache.has(id)) await Promise.race([fetchView(id), new Promise((r) => setTimeout(r, 280))]);
		if (want !== id) return;
		const swap = async () => {
			selected = id;
			await tick();
			if (focus) rowEl(id)?.focus({ preventScroll: true });
			rowEl(id)?.scrollIntoView({ block: 'nearest' });
		};
		const root = document.documentElement;
		if (layout !== 'split' || !selected || !document.startViewTransition || reduced() || document.visibilityState !== 'visible') {
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
		const i = shown.findIndex((t) => t.id === id);
		for (const n of [shown[i + 1], shown[i - 1]]) if (n && !cache.has(n.id)) fetchView(n.id);
	}
	function closePeek() {
		const id = selected;
		selected = null;
		want = null;
		syncUrl(null);
		if (id) rowEl(id)?.focus({ preventScroll: true });
	}
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

	// Split: open the ticket named in the address, or the first one. Table and
	// board: reopen the peek panel named in the address.
	let opened = false;
	let lastLayout = '';
	$effect(() => {
		if (!loaded || narrow || deskWidth === 0) return;
		void layout;
		untrack(() => {
			// Switching layouts starts clean: the peek panel stays closed.
			if (lastLayout && lastLayout !== layout && layout !== 'split') {
				opened = true;
				selected = null;
				syncUrl(null);
			}
			lastLayout = layout;
			const fromUrl = page.url.searchParams.get('t');
			const known = fromUrl && tickets.find((t) => t.id === fromUrl || ticketKey(t) === fromUrl)?.id;
			if (layout === 'split') {
				if (selected && shown.some((t) => t.id === selected)) return;
				const pick = known || shown[0]?.id || null;
				if (pick) select(pick);
				else selected = null;
			} else if (layout === 'list') {
				selected = null;
			} else if (!opened && known) {
				opened = true;
				select(known);
			}
		});
	});

	// Board: change a ticket's status by dragging or 1–4, with an undo.
	async function move(id: string, status: string) {
		const i = tickets.findIndex((t) => t.id === id);
		if (i < 0) return;
		const before = tickets[i];
		tickets[i] = { ...before, status };
		try {
			const v = await api<TicketView>('PATCH', `/tickets/${id}`, { status });
			onview(v);
			toast(`${ticketKey(before)} is now ${staffStatusLabel[status].toLowerCase()}`, 'success', { label: 'Undo', run: () => move(id, before.status) });
		} catch (e) {
			tickets[i] = before;
			toast(msg(e), 'fail');
		}
	}

	function key(e: KeyboardEvent) {
		if (e.key === '/' && shortcutAllowed(e)) {
			e.preventDefault();
			searchEl?.focus();
			searchEl?.select();
			return;
		}
		if (!shortcutAllowed(e) || (layout !== 'split' && layout !== 'table')) return;
		const onRow = e.target instanceof HTMLElement && !!e.target.closest('[data-ticket]');
		if (e.key === 'j' || (onRow && e.key === 'ArrowDown')) {
			e.preventDefault();
			step(1);
		} else if (e.key === 'k' || (onRow && e.key === 'ArrowUp')) {
			e.preventDefault();
			step(-1);
		} else if (e.key === 'Enter' && onRow && selected && layout === 'split') {
			e.preventDefault();
			const t = tickets.find((x) => x.id === selected);
			if (t) goto(`/support/${ticketKey(t)}`);
		}
	}
	function searchKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			if (query) query = '';
			else backToList();
		} else if (e.key === 'Enter' || e.key === 'ArrowDown') {
			e.preventDefault();
			if ((layout === 'split' || layout === 'table') && shown[0]) select(shown[0].id, 0, true);
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
		const back = () => document.visibilityState === 'visible' && ((now = Date.now()), load());
		document.addEventListener('visibilitychange', back);
		return () => {
			clearInterval(clock);
			document.removeEventListener('visibilitychange', back);
		};
	});

	// ---- Staff tools ------------------------------------------------------------------
	let labelsOpen = $state(false);
	let repliesOpen = $state(false);
	const toolItems = $derived<MenuItem[]>([
		{ label: 'Manage labels…', icon: 'sliders', onselect: () => (labelsOpen = true) },
		{ label: 'Saved replies…', icon: 'file', onselect: () => (repliesOpen = true) }
	]);

	// ---- New ticket ------------------------------------------------------------------
	let composing = $state(false);
	let bots = $state<Bot[]>([]);
	let form = $state({ subject: '', category: 'general', priority: 'normal', bot_id: '', body: '' });
	let sending = $state(false);
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
			goto(`/support/${ticketKey(t)}`);
		} catch (err) {
			error = msg(err);
		} finally {
			sending = false;
		}
	}
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

	const rowSelect = (t: Ticket) => () => {
		const i = shown.findIndex((x) => x.id === selected);
		const j = shown.findIndex((x) => x.id === t.id);
		select(t.id, i < 0 ? 0 : Math.sign(j - i));
	};
</script>

<svelte:window onkeydown={key} />
<svelte:head><title>{queue ? 'Support queue' : 'Support'} · RivetPanel</title></svelte:head>

<header class="flex flex-wrap items-end gap-3 border-b border-rule-soft pb-5">
	<div class="min-w-0 flex-[1_1_18rem]">
		<h1 class="text-page">{queue ? 'Support queue' : 'Support'}</h1>
		<p class="mt-0.5 text-muted">{queue ? 'Every ticket you may answer or read.' : 'Ask the people who run this panel for help. Only you and the support team see your tickets.'}</p>
	</div>
	{#if !narrow}
		<div role="radiogroup" aria-label="Layout" class="seg">
			{#each supportLayouts as l (l.id)}
				<button type="button" role="radio" class="seg-item !px-2" aria-checked={supportView.layout === l.id} title="{l.name}: {l.text}" onclick={() => setSupportLayout(l.id)}>
					<LayoutIcon layout={l.id} /><span class="sr-only">{l.name}</span>
				</button>
			{/each}
		</div>
	{/if}
	{#if isStaff}
		<nav class="seg" aria-label="Ticket views">
			{#if canOpen}<a class="seg-item" href="/support" aria-current={!queue ? 'page' : undefined}>My tickets</a>{/if}
			<a class="seg-item" href="/support?queue=all" aria-current={queue ? 'page' : undefined}>Queue</a>
		</nav>
	{/if}
	{#if queue && canManage}<Menu label="Support tools" items={toolItems} icon="sliders" text="Tools" />{/if}
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
		<label class="block"><span class="label">Message</span><textarea class="field min-h-36" required maxlength="10000" bind:value={form.body} placeholder="What happened, what you expected, and what you already tried. Markdown works."></textarea></label>
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
		{#if layout !== 'board'}
			<div role="tablist" aria-label="Status" class="seg">
				{#each tabs as k (k)}
					<button role="tab" type="button" class="seg-item" aria-selected={tab === k} onclick={() => setTab(k)}>
						{tabLabel(k)}<span class="tab-count tabular-nums" class:zero={!counts[k]}>{counts[k]}</span>
					</button>
				{/each}
			</div>
		{/if}
		<label class="search relative ml-auto flex min-w-48 flex-[0_1_18rem] items-center">
			<span class="sr-only">Search tickets</span>
			<Icon name="search" size={14} class="pointer-events-none absolute left-2.5 text-muted" />
			<input bind:this={searchEl} bind:value={query} onkeydown={searchKey} type="search" class="field h-8 w-full py-0 pr-8 pl-8" placeholder={queue ? 'RT-000042, subject, person, label' : 'RT-000042, subject, app'} autocomplete="off" />
			<kbd class="slash-hint pointer-events-none absolute right-2" aria-hidden="true">/</kbd>
		</label>
		{#if queue}
			<label class="flex items-center gap-2 text-small"><span class="text-muted">Assignee</span>
				<select class="field h-8 w-auto py-0" bind:value={assignee} onchange={() => kept.clear()}>
					<option value="any">Anyone</option>
					<option value="me">Me</option>
					<option value="none">Nobody</option>
					<option value="watching">Watching</option>
				</select>
			</label>
			{#if support.labels.length}
				<Picker label="Filter by label" options={labelOptions} value={labelFilter} multi onpick={(v) => ((labelFilter = v), kept.clear())} width="14rem" triggerClass="picker-trigger border-rule!">
					{#snippet trigger()}
						<Icon name="sliders" size={13} class="text-muted" />
						{#if labelFilter.length}
							{#each labelFilter.slice(0, 2) as id (id)}{@const l = support.labels.find((x) => x.id === id)}{#if l}<LabelChip label={l} />{/if}{/each}{#if labelFilter.length > 2}<span class="text-label">+{labelFilter.length - 2}</span>{/if}
						{:else}<span>Labels</span>{/if}
					{/snippet}
					{#snippet row(o)}{#if o.data}<LabelChip label={o.data} />{/if}{/snippet}
				</Picker>
			{/if}
		{/if}
	</div>

	{#if loaded && !tickets.length}
		<div class="mt-4">
			<EmptyState title="No tickets">
				{#if queue}Nobody has asked for help yet.{:else if canOpen}Open a ticket when you need help; answers also arrive under the bell in the top bar.{:else}Your role cannot open support tickets.{/if}
			</EmptyState>
		</div>
	{:else if layout === 'split'}
		<div bind:this={deskBody} style:--desk-top="{deskTop}px" class="desk-body mt-4 grid grid-cols-[minmax(18rem,23rem)_minmax(0,1fr)] gap-4">
			<div class="list-col card flex min-h-0 flex-col overflow-hidden">
				<ul bind:this={listEl} class="list-scroll min-h-0 flex-1 divide-y divide-rule-soft overflow-y-auto" aria-label="Tickets">
					{#each shown as t (t.id)}
						<li><TicketRow {t} staff={queue} {now} selected={selected === t.id} dim={kept.has(t.id) && !inTab(t, tab)} showStatus={tab === 'any' || tab === 'active' || kept.has(t.id)} onselect={rowSelect(t)} /></li>
					{/each}
					{#if loaded && !shown.length}<li class="px-4 py-10 text-center text-small text-muted">{query ? `Nothing matches “${query}”.` : 'Nothing here. Clear the filters or pick another tab.'}</li>{/if}
				</ul>
				<p class="legend border-t border-rule-soft px-4 py-2 text-label text-muted" aria-label="Keyboard shortcuts">
					<span><kbd>J</kbd> <kbd>K</kbd> move</span><span><kbd>R</kbd> reply</span>{#if canManage}<span><kbd>N</kbd> note</span><span><kbd>1</kbd>–<kbd>4</kbd> status</span><span><kbd>A</kbd> take</span>{/if}<span><kbd>E</kbd> subject</span><span><kbd>/</kbd> search</span>
				</p>
			</div>
			<section class="desk-pane card min-h-0 overflow-hidden" aria-label="Selected ticket">
				{#if selected}
					{#key selected}
						<TicketThread id={selected} mode="pane" initial={cache.get(selected) ?? null} {onview} onescape={backToList} />
					{/key}
				{:else}
					<div class="grid h-full place-items-center p-8 text-center text-muted">
						<p class="max-w-[32ch]">{loaded ? 'Pick a ticket on the left, or press J to start at the top.' : 'Loading tickets…'}</p>
					</div>
				{/if}
			</section>
		</div>
	{:else if layout === 'table'}
		<div class="mt-4"><TicketTable tickets={shown} staff={queue} {now} {selected} onopen={(id) => select(id)} /></div>
	{:else if layout === 'board'}
		<div class="mt-4"><TicketBoard tickets={narrowed} staff={queue} {now} canMove={queue && canManage} keys={!selected} {selected} onopen={(id) => select(id)} onmove={move} /></div>
	{:else}
		<ul class="list-card mt-4" aria-label="Tickets">
			{#each shown as t (t.id)}<li><TicketRow {t} staff={queue} {now} dim={kept.has(t.id) && !inTab(t, tab)} showStatus={tab === 'any' || tab === 'active' || kept.has(t.id)} /></li>{/each}
			{#if loaded && !shown.length}<li class="px-4 py-10 text-center text-small text-muted">{query ? `Nothing matches “${query}”.` : 'Nothing here. Pick another tab.'}</li>{/if}
		</ul>
	{/if}
</div>

{#if selected && (layout === 'table' || layout === 'board')}
	<TicketPeek id={selected} initial={cache.get(selected) ?? null} {onview} onclose={closePeek} />
{/if}

{#if queue && canManage}
	<LabelManager bind:open={labelsOpen} />
	<SavedReplyManager bind:open={repliesOpen} />
{/if}

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
	/* The desk fills the window below where it starts; on a short window the
	   page scrolls until the desk docks under the top bar. */
	.desk-body {
		position: sticky;
		top: calc(3.5rem + 1px + 1rem);
		height: max(30rem, min(calc(100dvh - var(--desk-top, 0px) - 1.5rem), calc(100dvh - 3.5rem - 1px - 2rem)));
	}
	.list-scroll {
		scrollbar-width: thin;
		scrollbar-color: var(--color-rule) transparent;
		overscroll-behavior: contain;
	}
</style>
