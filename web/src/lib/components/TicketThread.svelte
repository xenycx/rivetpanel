<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { type TicketView, type TicketMessage, type ReactionKind, statusKeys, staffStatusLabel, ticketKey, shortcutAllowed } from '$lib/api/support';
	import TicketDetails from '$lib/components/support/TicketDetails.svelte';
	import TicketMessageItem from '$lib/components/support/TicketMessageItem.svelte';
	import TicketComposer from '$lib/components/support/TicketComposer.svelte';
	import LabelManager from '$lib/components/support/LabelManager.svelte';
	import { support, loadDesk } from '$lib/supportData.svelte';
	import { onLive } from '$lib/live.svelte';

	// One ticket: header, details, conversation and reply box.
	//   page  the ticket's own page: conversation beside a details sidebar
	//   pane  the split layout's right side: fixed height, scrolls inside
	//   peek  the slide-over panel of the table and board layouts
	let {
		id,
		mode = 'page',
		initial = null,
		onview,
		onescape,
		onclose
	}: {
		id: string;
		mode?: 'page' | 'pane' | 'peek';
		/** A cached copy to show at once while the current one loads. */
		initial?: TicketView | null;
		/** Every newly loaded or changed view (lists update their rows). */
		onview?: (v: TicketView) => void;
		/** Escape in the reply box. */
		onescape?: () => void;
		/** The peek panel's close button. */
		onclose?: () => void;
	} = $props();

	const embedded = $derived(mode !== 'page');
	let view = $state<TicketView | null>(untrack(() => initial));
	let error = $state('');
	let notFound = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const me = $derived(session.user?.id ?? '');

	// Messages present at first render arrive silently; later ones settle in.
	let known: Set<string> | null = null;
	const isNew = (m: TicketMessage) => !!known && !known.has(m.id);
	function accept(v: TicketView) {
		view = v;
		if (!known) known = new Set(v.messages.map((m) => m.id));
		onview?.(v);
	}
	async function load() {
		error = '';
		try {
			const v = await api<TicketView>('GET', `/tickets/${id}`);
			const grew = !!view && v.messages.length > view.messages.length;
			accept(v);
			if (grew) settleToEnd();
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) notFound = true;
			else error = msg(e);
		}
	}

	let scroller = $state<HTMLElement>();
	async function settleToEnd(smooth = true) {
		await tick();
		if (!embedded || !scroller) return;
		scroller.scrollTo({ top: scroller.scrollHeight, behavior: smooth && !matchMedia('(prefers-reduced-motion: reduce)').matches ? 'smooth' : 'auto' });
	}
	// A link to a message (#m-<id>) scrolls to it and flashes it.
	async function toHash() {
		const h = location.hash;
		if (!h.startsWith('#m-')) return false;
		await tick();
		const el = document.getElementById(h.slice(1));
		if (!el) return false;
		el.scrollIntoView({ block: 'center' });
		el.classList.add('flash');
		setTimeout(() => el.classList.remove('flash'), 1700);
		return true;
	}

	onMount(() => {
		if (view) {
			known = new Set(view.messages.map((m) => m.id));
			settleToEnd(false);
		}
		loadDesk();
		load().then(async () => {
			if (!(await toHash()) && !initial) settleToEnd(false);
		});
		const back = () => document.visibilityState === 'visible' && !sending && load();
		// Live: replies, notes and changes from others appear without a reload.
		const unlive = onLive(['tickets'], () => !sending && load());
		document.addEventListener('visibilitychange', back);
		return () => {
			unlive();
			document.removeEventListener('visibilitychange', back);
		};
	});

	const t = $derived(view?.ticket);
	const canReply = $derived(!!view && (view.manage || (view.requester && t?.status !== 'closed')));
	const key = $derived(t ? ticketKey(t) : '');

	// ---- Sending ---------------------------------------------------------------
	let composer = $state<{ compose: (note?: boolean) => void; quote: (a: string, b: string) => void }>();
	let sending = $state(false);
	let pending = $state<TicketMessage | null>(null);
	async function send({ body, internal, mentions, attachments }: { body: string; internal: boolean; mentions: string[]; attachments: string[] }) {
		if (!view) return false;
		sending = true;
		error = '';
		pending = {
			id: 'pending',
			author: 'You',
			mine: true,
			kind: 'message',
			staff: view.manage && !(view.requester && !internal),
			internal,
			body: body || `Sending ${attachments.length} file${attachments.length === 1 ? '' : 's'}…`,
			created_at_ms: Date.now(),
			person: null,
			reactions: []
		};
		settleToEnd();
		try {
			const v = await api<TicketView>('POST', `/tickets/${id}/messages`, { body, internal, mentions, attachments });
			pending = null;
			accept(v);
			settleToEnd();
			return true;
		} catch (e) {
			pending = null;
			error = msg(e);
			return false;
		} finally {
			sending = false;
		}
	}

	// ---- Changes -----------------------------------------------------------------
	/** Apply a change at once, then confirm it with the server (or roll back). */
	async function change(patch: Record<string, string>) {
		if (!view) return false;
		error = '';
		const before = view;
		const next = { ...view.ticket };
		if (patch.status) next.status = patch.status;
		if (patch.priority) next.priority = patch.priority;
		if (patch.category) next.category = patch.category;
		if (patch.subject) next.subject = patch.subject;
		if (patch.assignee_id !== undefined) next.assignee_id = patch.assignee_id || null;
		view = { ...view, ticket: next };
		onview?.(view);
		try {
			accept(await api<TicketView>('PATCH', `/tickets/${id}`, patch));
			return true;
		} catch (e) {
			view = before;
			onview?.(before);
			error = msg(e);
			return false;
		}
	}
	async function act(method: string, path: string, body?: unknown) {
		error = '';
		try {
			accept(await api<TicketView>(method, `/tickets/${id}${path}`, body));
			return true;
		} catch (e) {
			error = msg(e);
			return false;
		}
	}

	/** Staff: move the ticket to a status, with an undo. */
	export async function setStatus(s: string) {
		if (!view?.manage || !t || t.status === s) return;
		const prev = t.status;
		if (await change({ status: s })) {
			toast(`${key} is now ${staffStatusLabel[s].toLowerCase()}`, 'success', { label: 'Undo', run: () => change({ status: prev }) });
		}
	}
	/** Staff: take the ticket. */
	export async function assignMe() {
		if (!view?.manage || !me || t?.assignee_id === me) return;
		if (await change({ assignee_id: me })) toast(`${key} is assigned to you`);
	}
	const setLabels = (ids: string[]) => {
		if (view) view = { ...view, ticket: { ...view.ticket, labels: ids } };
		act('PUT', '/labels', { labels: ids });
	};
	const watch = (on: boolean) => act(on ? 'PUT' : 'DELETE', '/watch');

	async function close() {
		const ok = await confirmDialog({ title: 'Close this ticket?', body: 'You cannot reply to a closed ticket; open a new one if you need more help.', confirmLabel: 'Close ticket' });
		if (ok) await change({ status: 'closed' });
	}

	// ---- Messages ------------------------------------------------------------------
	const firstId = $derived(view?.messages.find((m) => m.kind === 'message')?.id);
	const closed = $derived(t?.status === 'closed');
	const canEdit = (m: TicketMessage) => !!view && m.mine && m.kind === 'message' && !m.deleted && (m.staff ? view.manage : !closed);
	const canRemove = (m: TicketMessage) =>
		!!view && m.kind === 'message' && !m.deleted && m.id !== firstId && (view.manage || (m.mine && !m.staff && !closed));
	const editMessage = (m: TicketMessage) => (body: string) => act('PATCH', `/messages/${m.id}`, { body });
	async function removeMessage(m: TicketMessage) {
		const own = m.mine;
		const ok = await confirmDialog({
			title: own ? 'Remove your message?' : 'Remove this message?',
			body: own
				? 'Its text and earlier versions are deleted for everyone. A short “Message removed” line stays in the conversation.'
				: 'Its text and earlier versions are deleted for everyone, including the person who wrote it.',
			confirmLabel: 'Remove message',
			tone: 'danger'
		});
		if (ok) act('DELETE', `/messages/${m.id}`);
	}
	function react(m: TicketMessage, kind: ReactionKind, on: boolean) {
		act(on ? 'PUT' : 'DELETE', `/messages/${m.id}/reactions/${kind}`);
	}
	function quote(m: TicketMessage) {
		composer?.quote(m.mine ? 'You' : (m.person?.name ?? m.author), m.body);
	}

	// ---- Subject editing --------------------------------------------------------
	const canEditDetails = $derived(!!view && (view.manage || (view.requester && (t?.status === 'open' || t?.status === 'pending'))));
	let editingSubject = $state(false);
	let subjectDraft = $state('');
	let subjectBox = $state<HTMLInputElement>();
	async function editSubject() {
		if (!t || !canEditDetails) return;
		subjectDraft = t.subject;
		editingSubject = true;
		await tick();
		subjectBox?.select();
	}
	async function saveSubject() {
		if (!t) return;
		const s = subjectDraft.trim();
		editingSubject = false;
		if (s && s !== t.subject) await change({ subject: s });
	}

	// ---- Keys ------------------------------------------------------------------------
	function keydown(e: KeyboardEvent) {
		if (!view || !shortcutAllowed(e)) return;
		if (e.key === 'r' && canReply) {
			e.preventDefault();
			composer?.compose(false);
		} else if (e.key === 'n' && view.manage) {
			e.preventDefault();
			composer?.compose(true);
		} else if (view.manage && /^[1-4]$/.test(e.key)) {
			e.preventDefault();
			setStatus(statusKeys[Number(e.key) - 1]);
		} else if (e.key === 'a' && view.manage) {
			e.preventDefault();
			assignMe();
		} else if (e.key === 'e' && canEditDetails && !editingSubject) {
			e.preventDefault();
			editSubject();
		}
	}

	// ---- Thread layout -------------------------------------------------------------
	const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric' });
	const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });
	function dayLabel(ms: number) {
		const d = new Date(ms);
		const today = new Date();
		const y = new Date();
		y.setDate(today.getDate() - 1);
		if (d.toDateString() === today.toDateString()) return 'Today';
		if (d.toDateString() === y.toDateString()) return 'Yesterday';
		return dayFmt.format(d);
	}
	type Row = { day?: string; m: TicketMessage };
	const rows = $derived.by(() => {
		const out: Row[] = [];
		let last = '';
		for (const m of [...(view?.messages ?? []), ...(pending ? [pending] : [])]) {
			const day = new Date(m.created_at_ms).toDateString();
			out.push({ m, day: day !== last ? dayLabel(m.created_at_ms) : undefined });
			last = day;
		}
		return out;
	});

	let labelsOpen = $state(false);

	// The requester waits for a first answer: say how long it usually takes.
	const awaiting = $derived(
		!!view && view.requester && !view.manage && t?.status === 'open' && !view.messages.some((m) => m.kind === 'message' && m.staff && !m.auto)
	);
	const rel = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
	function ago(ms: number) {
		const min = Math.round((ms - Date.now()) / 60000);
		if (Math.abs(min) < 60) return rel.format(min, 'minute');
		const h = Math.round(min / 60);
		if (Math.abs(h) < 24) return rel.format(h, 'hour');
		const d = Math.round(h / 24);
		if (Math.abs(d) < 31) return rel.format(d, 'day');
		return rel.format(Math.round(d / 30), 'month');
	}
</script>

<svelte:window onkeydown={keydown} />

{#snippet header()}
	<div class="flex items-start gap-3">
		<div class="min-w-0 flex-1">
			{#if embedded}<p class="flex items-center gap-2"><a class="ticket-key hover:text-ink" href="/support/{key}" title="Link to this ticket">{key}</a></p>{/if}
			{#if editingSubject}
				<form class="mt-0.5" onsubmit={(e) => (e.preventDefault(), saveSubject())}>
					<label class="sr-only" for="subject-{id}">Subject</label>
					<input
						id="subject-{id}"
						bind:this={subjectBox}
						bind:value={subjectDraft}
						maxlength="150"
						class="field subject-field {embedded ? 'text-title' : 'text-section'} font-semibold"
						onblur={saveSubject}
						onkeydown={(e) => e.key === 'Escape' && (e.preventDefault(), e.stopPropagation(), (editingSubject = false))}
					/>
				</form>
			{:else if embedded}
				<h2 class="subject-line text-title font-semibold text-balance break-words">
					{t?.subject}{#if canEditDetails}<button type="button" class="subject-edit" onclick={editSubject} title="Edit the subject (E)" aria-label="Edit the subject"><Icon name="pencil" size={13} /></button>{/if}
				</h2>
			{:else}
				<h1 class="subject-line text-page text-balance break-words">
					{t?.subject}{#if canEditDetails}<button type="button" class="subject-edit" onclick={editSubject} title="Edit the subject (E)" aria-label="Edit the subject"><Icon name="pencil" size={15} /></button>{/if}
				</h1>
			{/if}
			<p class="mt-1 text-small text-muted">
				Opened <time datetime={new Date(t!.created_at_ms).toISOString()} title={fmtWhen(t!.created_at_ms)}>{fmtWhen(t!.created_at_ms)}</time> <span class="text-muted/80">({ago(t!.created_at_ms)})</span>{#if view?.staff}{' '}by {t?.requester?.name ?? t?.requester_email}{/if}{#if t?.bot_name}{' '}· about {t.bot_name}{/if}
			</p>
		</div>
		{#if mode !== 'page' && view?.manage && t?.assignee_id !== me}
			<button type="button" class="btn btn-quiet h-8 shrink-0 px-2.5 text-small" onclick={assignMe} aria-keyshortcuts="a" title="Assign the ticket to yourself">Take it<kbd class="key-hint">A</kbd></button>
		{/if}
		{#if mode !== 'page'}
			<a class="btn btn-quiet h-8 shrink-0 px-2" href="/support/{key}" title="Open the ticket on its own page" aria-label="Open the ticket on its own page"><Icon name="maximize" size={14} /></a>
		{/if}
		{#if mode === 'peek'}
			<button type="button" class="btn btn-quiet h-8 shrink-0 px-2" onclick={onclose} title="Close (Esc)" aria-label="Close the ticket"><Icon name="x" size={15} /></button>
		{/if}
	</div>
{/snippet}

{#snippet details(variant: 'aside' | 'strip')}
	{#if view}
		<TicketDetails {view} {variant} onchange={change} onstatus={setStatus} onassignme={assignMe} onlabels={setLabels} onwatch={watch} onmanagelabels={() => (labelsOpen = true)} />
	{/if}
{/snippet}

{#snippet thread()}
	<ol class="grid gap-3" aria-label="Conversation">
		{#each rows as { m, day } (m.id)}
			{#if day}
				<li class="day-rule mt-3 text-label text-muted"><span>{day}</span></li>
			{/if}
			{#if m.kind === 'event'}
				<li id="m-{m.id}" class="flex flex-wrap items-center gap-x-2 px-1 text-small text-muted" class:msg-new={isNew(m)}>
					<Icon name="history" size={13} /><span>{m.body}</span><span aria-hidden="true">·</span><span>{m.person?.name ?? m.author}</span><time class="tabular-nums" datetime={new Date(m.created_at_ms).toISOString()} title={fmtWhen(m.created_at_ms)}>{timeFmt.format(m.created_at_ms)}</time>{#if m.internal}<span class="rounded-pill border border-warn/30 px-1.5 text-label text-warn">staff only</span>{/if}
				</li>
			{:else}
				<TicketMessageItem
					{m}
					ticketId={id}
					ticketKey={key}
					canEdit={canEdit(m)}
					canRemove={canRemove(m)}
					canReact={m.id !== 'pending' && !closed}
					showHistory={!!view && (view.staff || m.mine)}
					isNew={isNew(m)}
					pending={m.id === 'pending'}
					onedit={editMessage(m)}
					onremove={() => removeMessage(m)}
					onreact={(k, on) => react(m, k, on)}
					onquote={() => quote(m)}
				/>
			{/if}
		{/each}
	</ol>
{/snippet}

{#snippet reply()}
	{#if awaiting && support.desk.response_hours > 0}
		<p class="awaiting {embedded ? 'mx-5 mb-0 mt-2' : 'mt-4'} flex items-center gap-2 text-small text-muted" role="status">
			<Icon name="clock" size={13} class="shrink-0 text-action" />
			<span>The support team usually answers <strong class="font-medium text-ink">{support.desk.response_time}</strong>. You'll get a notification under the bell when they do.</span>
		</p>
	{/if}
	{#if view && canReply}
		<TicketComposer bind:this={composer} {id} {view} {embedded} {sending} onsend={send} {onescape} />
		{#if view.requester && !view.manage}
			<div class="flex flex-wrap justify-end gap-1 {embedded ? 'px-5 pb-3' : '-mt-2 pb-2'}">
				{#if t?.status === 'resolved'}<button type="button" class="btn btn-quiet h-8" onclick={() => change({ status: 'open' })}>Reopen</button>{/if}
				<button type="button" class="btn btn-quiet h-8" onclick={close}>Close ticket</button>
			</div>
		{/if}
	{:else if view?.requester && closed}
		<Notice class={embedded ? 'm-5' : 'mt-6'}>This ticket is closed. <a class="link" href="/support?new=1">Open a new ticket</a> if you need more help.</Notice>
	{/if}
{/snippet}

{#if notFound}
	<Notice tone="warn" class={embedded ? 'm-5' : 'mt-4'}>This ticket does not exist or you cannot see it.</Notice>
{:else if view && t}
	{#if embedded}
		<div class="flex h-full min-h-0 flex-col">
			<header class="grid gap-3 border-b border-rule-soft px-5 pt-4 pb-3">
				{@render header()}
				{@render details('strip')}
			</header>
			{#if error}<Notice tone="fail" class="mx-5 mt-3" live>{error}</Notice>{/if}
			<div bind:this={scroller} class="thread-scroll min-h-0 flex-1 overflow-y-auto px-5 pt-3 pb-4">{@render thread()}</div>
			{@render reply()}
		</div>
	{:else}
		<div class="ticket-wrap"><div class="ticket-page">
			<header class="ticket-head border-b border-rule-soft pb-4">
				{@render header()}
				<div class="ticket-strip mt-3">{@render details('strip')}</div>
			</header>
			<div class="ticket-main min-w-0">
				{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
				{@render thread()}
				{@render reply()}
			</div>
			<div class="ticket-side">{@render details('aside')}</div>
		</div></div>
	{/if}
	{#if view.manage}<LabelManager bind:open={labelsOpen} />{/if}
{:else if error}
	<Notice tone="fail" class={embedded ? 'm-5' : 'mt-4'} live>{error}</Notice>
{:else if embedded}
	<div class="grid h-full place-items-center text-small text-muted" aria-busy="true">Loading the ticket…</div>
{/if}

<style>
	/* The ticket page: the conversation beside a sticky details sidebar on
	   wide content, the details above the conversation otherwise. */
	.ticket-wrap {
		container-type: inline-size;
	}
	.ticket-page {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		grid-template-areas: 'head' 'main';
		gap: 1.25rem;
		margin-top: 0.75rem;
	}
	.ticket-head {
		grid-area: head;
	}
	.ticket-main {
		grid-area: main;
	}
	.ticket-side {
		grid-area: side;
		display: none;
	}
	/* Narrow: the compact strip under the title; wide: the sidebar. */
	@container (min-width: 54rem) {
		.ticket-strip {
			display: none;
		}
		.ticket-side {
			display: block;
		}
		.ticket-page {
			grid-template-columns: minmax(0, 1fr) 19rem;
			grid-template-areas: 'head head' 'main side';
			column-gap: 2rem;
		}
		.ticket-side {
			position: sticky;
			top: calc(3.5rem + 1px + 1.25rem);
			align-self: start;
		}
	}
	.subject-line {
		position: relative;
	}
	.subject-edit {
		display: inline-grid;
		place-items: center;
		width: 1.75rem;
		height: 1.75rem;
		margin-left: 0.375rem;
		vertical-align: middle;
		border-radius: var(--radius-control);
		color: var(--color-muted);
		opacity: 0;
		transition: opacity 120ms;
	}
	.subject-line:hover .subject-edit,
	.subject-edit:focus-visible {
		opacity: 1;
	}
	@media (pointer: coarse) {
		.subject-edit {
			opacity: 1;
		}
	}
	.subject-edit:hover {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 6%, transparent);
	}
	.subject-field {
		height: auto;
		padding-block: 0.25rem;
	}
	.day-rule {
		display: flex;
		align-items: center;
		gap: 0.75rem;
	}
	.day-rule::before,
	.day-rule::after {
		content: '';
		flex: 1;
		border-top: 1px solid var(--color-rule-soft);
	}
	.msg-new {
		animation: settle 380ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes settle {
		from {
			opacity: 0;
			transform: translateY(10px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.msg-new {
			animation: fade 200ms ease-out both;
		}
	}
	.thread-scroll {
		scrollbar-width: thin;
		scrollbar-color: var(--color-rule) transparent;
		overscroll-behavior: contain;
		/* Room above the first message for its hover toolbar. */
		scroll-padding-top: 1rem;
	}
</style>
