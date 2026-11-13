<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import {
		type TicketView,
		type TicketMessage,
		ticketPriorities,
		priorityLabel,
		statusKeys,
		statusLabel,
		staffStatusLabel,
		staffStatusShort,
		statusTone,
		categoryLabel,
		drafts,
		shortcutAllowed
	} from '$lib/api/support';

	// One ticket's conversation: header, staff controls, thread and composer.
	// The ticket page shows it in the page flow; the triage desk shows it
	// embedded in a pane of fixed height that scrolls its thread inside and
	// keeps the composer in reach.
	let {
		id,
		embedded = false,
		initial = null,
		onview,
		onescape
	}: {
		id: string;
		embedded?: boolean;
		/** A cached copy to show at once while the current one loads. */
		initial?: TicketView | null;
		/** Every newly loaded or changed view (the desk updates its row and cache). */
		onview?: (v: TicketView) => void;
		/** Escape in the composer (the desk returns focus to its list). */
		onescape?: () => void;
	} = $props();

	let view = $state<TicketView | null>(untrack(() => initial));
	let error = $state('');
	let notFound = $state(false);
	let staff = $state<{ id: string; email: string; display_name: string }[]>([]);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	// Messages present at first render arrive silently; later ones (a reply
	// sent here, or one fetched on refresh) settle in.
	let known: Set<string> | null = null;
	const isNew = (m: TicketMessage) => !!known && !known.has(m.id);

	function accept(v: TicketView) {
		view = v;
		if (!known) known = new Set(v.messages.map((m) => m.id));
		onview?.(v);
	}

	let staffLoaded = false;
	async function load() {
		error = '';
		try {
			const v = await api<TicketView>('GET', `/tickets/${id}`);
			const grew = !!view && v.messages.length > view.messages.length;
			accept(v);
			if (grew) settleToEnd();
			if (v.manage && !staffLoaded) {
				staffLoaded = true;
				staff = (await api<{ staff: typeof staff }>('GET', '/tickets/staff')).staff;
			}
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) notFound = true;
			else error = msg(e);
		}
	}

	// The thread scroller (embedded) opens at the latest message.
	let scroller = $state<HTMLElement>();
	async function settleToEnd(smooth = true) {
		await tick();
		if (!embedded || !scroller) return;
		scroller.scrollTo({ top: scroller.scrollHeight, behavior: smooth && !matchMedia('(prefers-reduced-motion: reduce)').matches ? 'smooth' : 'auto' });
	}

	onMount(() => {
		if (view) {
			known = new Set(view.messages.map((m) => m.id));
			settleToEnd(false);
		}
		load().then(() => {
			if (!initial) settleToEnd(false);
		});
		// Pick up answers that arrived while the tab was in the background.
		const back = () => document.visibilityState === 'visible' && !sending && load();
		document.addEventListener('visibilitychange', back);
		return () => document.removeEventListener('visibilitychange', back);
	});

	const t = $derived(view?.ticket);
	const labels = $derived(view?.staff ? staffStatusLabel : statusLabel);
	const canReply = $derived(!!view && (view.manage || (view.requester && t?.status !== 'closed')));
	const me = $derived(session.user?.id);

	// ---- Composer --------------------------------------------------------
	const saved = untrack(() => drafts.get(id));
	let reply = $state(saved?.body ?? '');
	let internal = $state(saved?.internal ?? false);
	let sending = $state(false);
	let box = $state<HTMLTextAreaElement>();
	$effect(() => {
		const body = reply;
		const note = internal;
		untrack(() => (body.trim() ? drafts.set(id, { body, internal: note }) : drafts.delete(id)));
	});

	/** Focus the composer, as a reply or as an internal note. */
	export function compose(note = false) {
		if (!canReply) return;
		internal = note && !!view?.manage;
		box?.focus();
	}

	let pending = $state<TicketMessage | null>(null);
	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		const body = reply.trim();
		if (!body || sending || !view) return;
		sending = true;
		error = '';
		const note = internal;
		// Show it in the thread at once; the server's copy replaces it.
		pending = {
			id: 'pending',
			author: 'You',
			mine: true,
			kind: 'message',
			staff: view.manage && !(view.requester && !note),
			internal: note,
			body,
			created_at_ms: Date.now()
		};
		reply = '';
		internal = false;
		settleToEnd();
		try {
			const v = await api<TicketView>('POST', `/tickets/${id}/messages`, { body, internal: note });
			pending = null;
			accept(v);
			settleToEnd();
		} catch (err) {
			pending = null;
			reply = body;
			internal = note;
			error = msg(err);
		} finally {
			sending = false;
		}
	}
	function composerKey(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
			e.preventDefault();
			send();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			box?.blur();
			onescape?.();
		}
	}

	// ---- Changes -----------------------------------------------------------
	/** Apply a change at once, then confirm it with the server (or roll back). */
	async function change(patch: { status?: string; priority?: string; assignee_id?: string }) {
		if (!view) return false;
		error = '';
		const before = view;
		const next = { ...view.ticket };
		if (patch.status) next.status = patch.status;
		if (patch.priority) next.priority = patch.priority;
		if (patch.assignee_id !== undefined) {
			next.assignee_id = patch.assignee_id || null;
			next.assignee_email = staff.find((s) => s.id === patch.assignee_id)?.email ?? '';
		}
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

	/** Staff: move the ticket to a status, with an undo. */
	export async function setStatus(s: string) {
		if (!view?.manage || !t || t.status === s) return;
		const prev = t.status;
		if (await change({ status: s })) {
			toast(`#${t.number} is now ${staffStatusLabel[s].toLowerCase()}`, 'success', { label: 'Undo', run: () => change({ status: prev }) });
		}
	}

	/** Staff: take the ticket. */
	export async function assignMe() {
		if (!view?.manage || !me || t?.assignee_id === me) return;
		if (await change({ assignee_id: me })) toast(`#${t?.number} is assigned to you`);
	}

	async function close() {
		const ok = await confirmDialog({ title: 'Close this ticket?', body: 'You cannot reply to a closed ticket; open a new one if you need more help.', confirmLabel: 'Close ticket' });
		if (ok) await change({ status: 'closed' });
	}

	function key(e: KeyboardEvent) {
		if (!view || !shortcutAllowed(e)) return;
		if (e.key === 'r' && canReply) {
			e.preventDefault();
			compose(false);
		} else if (e.key === 'n' && view.manage) {
			e.preventDefault();
			compose(true);
		} else if (view.manage && /^[1-4]$/.test(e.key)) {
			e.preventDefault();
			setStatus(statusKeys[Number(e.key) - 1]);
		} else if (e.key === 'a' && view.manage) {
			e.preventDefault();
			assignMe();
		}
	}

	// ---- Thread layout -------------------------------------------------------
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
</script>

<svelte:window onkeydown={key} />

{#if notFound}
	<Notice tone="warn" class={embedded ? 'm-5' : 'mt-4'}>This ticket does not exist or you cannot see it.</Notice>
{:else if view && t}
	<div class={embedded ? 'flex h-full min-h-0 flex-col' : 'contents'}>
		<header class={embedded ? 'border-b border-rule-soft px-5 pt-4 pb-4' : 'mt-3 border-b border-rule-soft pb-5'}>
			<div class="flex items-start gap-3">
				<div class="min-w-0 flex-1">
					<p class="font-mono text-small text-muted tabular-nums">#{t.number}</p>
					{#if embedded}
						<h2 class="text-section font-semibold break-words text-balance">{t.subject}</h2>
					{:else}
						<h1 class="text-page break-words text-balance">{t.subject}</h1>
					{/if}
					<p class="mt-1 text-small text-muted">
						{categoryLabel(t.category)} · {t.priority} priority · opened {fmtWhen(t.created_at_ms)}
						{#if t.bot_name}{' '}· about {#if t.bot_id && view.requester}<a class="link" href="/bots/{t.bot_id}">{t.bot_name}</a>{:else}{t.bot_name}{/if}{/if}
						{#if view.staff}{' '}· by {t.requester_email}{/if}
					</p>
				</div>
				{#if view.manage && t.assignee_id !== me}
					<button type="button" class="btn btn-quiet h-8 shrink-0 px-2.5 text-small" onclick={assignMe} title="Assign the ticket to yourself (press A)" aria-keyshortcuts="a">Take it<kbd class="key-hint">A</kbd></button>
				{/if}
				{#if embedded}
					<a class="btn btn-quiet h-8 shrink-0 px-2" href="/support/{t.id}" title="Open the ticket on its own page" aria-label="Open the ticket on its own page"><Icon name="maximize" size={14} /></a>
				{/if}
				{#if !view.manage}
					<span class="shrink-0 rounded-pill border px-2.5 py-1 text-small {statusTone[t.status]}">{labels[t.status]}</span>
				{/if}
			</div>

			{#if view.manage}
				<div class="mt-4 flex flex-wrap items-center gap-2">
					<div role="group" aria-label="Status" class="seg">
						{#each statusKeys as s, i (s)}
							<button type="button" class="seg-item status-opt" data-s={s} aria-pressed={t.status === s} onclick={() => setStatus(s)} title="{staffStatusLabel[s]} (press {i + 1})" aria-keyshortcuts={String(i + 1)}>
								<span class="status-dot" aria-hidden="true"></span>{staffStatusShort[s]}
							</button>
						{/each}
					</div>
					<select class="field h-8 w-auto py-0 text-small" aria-label="Priority" value={t.priority} onchange={(e) => change({ priority: e.currentTarget.value })}>
						{#each ticketPriorities as p (p)}<option value={p}>{priorityLabel(p)} priority</option>{/each}
					</select>
					<select class="field h-8 w-auto max-w-60 py-0 text-small" aria-label="Assignee" value={t.assignee_id ?? ''} onchange={(e) => change({ assignee_id: e.currentTarget.value })}>
						<option value="">Unassigned</option>
						{#each staff as s (s.id)}<option value={s.id}>{s.id === me ? 'Assigned to me' : `Assigned to ${s.display_name || s.email}`}</option>{/each}
					</select>
				</div>
			{:else if view.staff}
				<p class="mt-3 text-small text-muted">You can read this ticket and its internal notes but not answer it.{#if t.assignee_email}{' '}Assigned to {t.assignee_email}.{/if}</p>
			{/if}
		</header>

		{#if error}<Notice tone="fail" class={embedded ? 'mx-5 mt-4' : 'mt-4'} live>{error}</Notice>{/if}

		<div bind:this={scroller} class={embedded ? 'thread-scroll min-h-0 flex-1 overflow-y-auto px-5 pt-2 pb-4' : 'pb-2'}>
			<ol class="grid gap-3" aria-label="Conversation">
				{#each rows as { m, day } (m.id)}
					{#if day}
						<li class="day-rule mt-3 text-label text-muted"><span>{day}</span></li>
					{/if}
					{#if m.kind === 'event'}
						<li class="flex flex-wrap items-center gap-x-2 px-1 text-small text-muted" class:msg-new={isNew(m)}>
							<Icon name="history" size={13} /><span>{m.body}</span><span aria-hidden="true">·</span><span>{m.author}</span><time class="tabular-nums" datetime={new Date(m.created_at_ms).toISOString()} title={fmtWhen(m.created_at_ms)}>{timeFmt.format(m.created_at_ms)}</time>{#if m.internal}<span class="rounded-pill border border-warn/30 px-1.5 text-label text-warn">staff only</span>{/if}
						</li>
					{:else}
						<li
							class="msg rounded-card border p-4 {m.internal ? 'border-warn/40 bg-warn/6' : m.staff ? 'border-action/25 bg-action/4' : 'border-rule-soft bg-raised'}"
							class:msg-new={isNew(m) || m.id === 'pending'}
							class:opacity-60={m.id === 'pending'}
							aria-busy={m.id === 'pending'}
						>
							<div class="flex flex-wrap items-center gap-2 text-small">
								<span class="font-medium">{m.mine ? 'You' : m.author}</span>
								{#if m.staff && !m.internal}<span class="rounded-pill border border-action/30 px-1.5 text-label text-action">support</span>{/if}
								{#if m.internal}<span class="rounded-pill border border-warn/40 px-1.5 text-label text-warn">internal note · staff only</span>{/if}
								<span class="ml-auto text-muted tabular-nums">
									{#if m.id === 'pending'}Sending…{:else}<time datetime={new Date(m.created_at_ms).toISOString()} title={fmtWhen(m.created_at_ms)}>{timeFmt.format(m.created_at_ms)}</time>{/if}
								</span>
							</div>
							<p class="mt-2 max-w-[75ch] break-words whitespace-pre-wrap">{m.body}</p>
						</li>
					{/if}
				{/each}
			</ol>
		</div>

		{#if canReply}
			<form class="composer {embedded ? 'border-t border-rule-soft px-5 pt-3 pb-4' : 'sticky bottom-0 z-10 -mx-1 mt-4 px-1 pt-3 pb-4'}" data-note={internal} data-embedded={embedded} onsubmit={send}>
				<div class="composer-box rounded-tile border" data-idle={embedded && !reply.trim()}>
					{#if view.manage}
						<div class="flex items-center gap-2 px-3 pt-2.5">
							<div role="group" aria-label="Write" class="seg">
								<button type="button" class="seg-item" aria-pressed={!internal} onclick={() => compose(false)}>Reply<kbd class="key-hint">R</kbd></button>
								<button type="button" class="seg-item" aria-pressed={internal} onclick={() => compose(true)}><Icon name="lock" size={12} />Internal note<kbd class="key-hint">N</kbd></button>
							</div>
							{#if internal}<span class="text-small text-warn">The requester never sees it.</span>{/if}
						</div>
					{/if}
					<label class="sr-only" for="reply-{id}">{internal ? 'Internal note' : 'Reply'}</label>
					<textarea
						id="reply-{id}"
						bind:this={box}
						bind:value={reply}
						onkeydown={composerKey}
						class="composer-input block w-full resize-none bg-transparent px-3 py-2.5 outline-none"
						rows="3"
						maxlength="10000"
						placeholder={internal ? 'Note for the support team…' : view.manage ? `Reply to ${t.requester_email ?? 'the requester'}…` : 'Write a reply…'}
					></textarea>
					<div class="composer-actions flex flex-wrap items-center gap-2 px-3 pb-2.5">
						<button class="btn btn-primary h-8 px-3" disabled={sending || !reply.trim()}><Icon name="send" size={14} />{internal ? 'Add note' : 'Send reply'}</button>
						<span class="hidden text-small text-muted sm:inline"><kbd class="key-hint">Ctrl</kbd> <kbd class="key-hint">Enter</kbd></span>
						{#if view.requester && !view.manage}
							<span class="ml-auto flex gap-1">
								{#if t.status === 'resolved'}<button type="button" class="btn btn-quiet h-8" onclick={() => change({ status: 'open' })}>Reopen</button>{/if}
								<button type="button" class="btn btn-quiet h-8" onclick={close}>Close ticket</button>
							</span>
						{/if}
					</div>
				</div>
			</form>
		{:else if view.requester && t.status === 'closed'}
			<Notice class={embedded ? 'm-5' : 'mt-6'}>This ticket is closed. <a class="link" href="/support?new=1">Open a new ticket</a> if you need more help.</Notice>
		{/if}
	</div>
{:else if error}
	<Notice tone="fail" class={embedded ? 'm-5' : 'mt-4'} live>{error}</Notice>
{:else if embedded}
	<div class="grid h-full place-items-center text-small text-muted" aria-busy="true">Loading the ticket…</div>
{/if}

<style>
	/* Status switch: each option carries its lifecycle colour as a dot, and
	   the chosen one takes the colour as a tint. */
	.status-opt {
		--s: var(--color-muted);
	}
	.status-opt[data-s='open'] {
		--s: var(--color-action);
	}
	.status-opt[data-s='pending'] {
		--s: var(--color-warn);
	}
	.status-opt[data-s='resolved'] {
		--s: var(--color-run);
	}
	.status-dot {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: var(--radius-pill);
		background: var(--s);
		opacity: 0.55;
		transition:
			opacity 160ms,
			transform 220ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.status-opt[aria-pressed='true'] {
		background: color-mix(in srgb, var(--s) var(--tint-state), var(--color-raised));
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--s) 45%, transparent);
	}
	.status-opt[aria-pressed='true'] .status-dot {
		opacity: 1;
		transform: scale(1.15);
	}
	.key-hint {
		margin-left: 0.125rem;
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
	@media (pointer: coarse) {
		.key-hint {
			display: none;
		}
	}

	/* Day rule: a hairline with the day centred on it. */
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

	/* A message that arrives while the thread is open settles in from below. */
	.msg-new {
		animation: settle 380ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes settle {
		from {
			opacity: 0;
			transform: translateY(10px);
			filter: blur(2px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.msg-new {
			animation: fade 200ms ease-out both;
		}
	}

	/* Composer: the whole surface turns amber for an internal note, so a note
	   is never mistaken for a reply. */
	.composer[data-embedded='false'] {
		background: linear-gradient(to bottom, transparent, var(--color-paper) 0.75rem);
	}
	.composer-box {
		border-color: var(--color-rule);
		background: var(--color-panel);
		transition:
			border-color 160ms,
			background-color 160ms,
			box-shadow 160ms;
	}
	.composer-box:focus-within {
		border-color: color-mix(in srgb, var(--color-action) 55%, var(--color-rule));
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-action) 14%, transparent);
	}
	.composer[data-note='true'] .composer-box {
		border-color: color-mix(in srgb, var(--color-warn) 45%, var(--color-rule));
		background: color-mix(in srgb, var(--color-warn) 7%, var(--color-panel));
	}
	.composer[data-note='true'] .composer-box:focus-within {
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-warn) 16%, transparent);
	}
	.composer-input {
		field-sizing: content;
		min-height: 4.5rem;
		max-height: 16rem;
	}
	/* In the desk an empty, unfocused composer folds to one line, so the
	   thread keeps the room; focus (R, N or a click) opens it again. */
	.composer-box[data-idle='true']:not(:focus-within) .composer-input {
		min-height: 2.6rem;
		height: 2.6rem;
		overflow: hidden;
	}
	.composer-box[data-idle='true']:not(:focus-within) .composer-actions {
		display: none;
	}
	.composer-input::placeholder {
		color: var(--color-muted);
	}
	.thread-scroll {
		scrollbar-width: thin;
		scrollbar-color: var(--color-rule) transparent;
		overscroll-behavior: contain;
	}
</style>
