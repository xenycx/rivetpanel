<script lang="ts">
	import { onMount } from 'svelte';
	import { fly } from 'svelte/transition';
	import { cubicOut } from 'svelte/easing';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { type Ticket, ticketKey, idleTone, fmtIdle, statusKeys, staffStatusShort, statusLabel, categoryLabel, drafts, shortcutAllowed } from '$lib/api/support';
	import { labelById } from '$lib/supportData.svelte';
	import { glide, canGlide } from '$lib/ui/glide';
	import PriorityIcon from './PriorityIcon.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import LabelChip from './LabelChip.svelte';

	// The board layout: a column per status. Cards are picked up with the
	// mouse or pen (after a few pixels of movement) or a touch long-press,
	// carried as a lifted copy, and dropped on another column; the card then
	// glides into its new place. With keyboard focus on a card, 1–4 move it.
	// Who may move what:
	//   manage     staff who manage tickets: any status
	//   requester  the requester: close a ticket, or reopen a resolved one
	//   none       view-only staff: nothing
	let {
		tickets,
		staff,
		now,
		mode,
		keys = true,
		selected = null,
		onopen,
		onmove
	}: {
		tickets: Ticket[];
		staff: boolean;
		now: number;
		mode: 'manage' | 'requester' | 'none';
		/** Card shortcuts (off while the peek panel handles keys). */
		keys?: boolean;
		selected?: string | null;
		onopen: (id: string) => void;
		/** Apply the move (the board animates it). */
		onmove: (id: string, status: string) => void;
	} = $props();

	const CLOSED_LIMIT = 30;
	const columns = $derived(
		statusKeys.map((s) => {
			const all = tickets.filter((t) => t.status === s);
			return { status: s, all, cards: s === 'closed' ? all.slice(0, CLOSED_LIMIT) : all };
		})
	);
	const me = $derived(session.user?.id);
	const names = $derived(staff ? staffStatusShort : statusLabel);

	function allowed(t: Ticket, to: string) {
		if (t.status === to || mode === 'none') return false;
		if (mode === 'manage') return true;
		return to === 'closed' || (to === 'open' && t.status === 'resolved');
	}
	const movable = (t: Ticket) => statusKeys.some((s) => allowed(t, s));
	const cardEl = (id: string) => board?.querySelector<HTMLElement>(`.card-item[data-ticket="${CSS.escape(id)}"]`) ?? null;

	// ---- Dragging --------------------------------------------------------------
	let board = $state<HTMLElement>();
	let ghost = $state<HTMLElement>();
	type Pending = { t: Ticket; el: HTMLElement; x: number; y: number; touch: boolean; timer?: ReturnType<typeof setTimeout> };
	let pending: Pending | null = null;
	let drag = $state<{ t: Ticket; w: number; h: number; dx: number; dy: number; x: number; y: number; ox: number; oy: number } | null>(null);
	let over = $state<string | null>(null);
	let suppressClick = false;

	function down(e: PointerEvent, t: Ticket) {
		if (e.button !== 0 || !movable(t) || (e.target as HTMLElement).closest('a, button')) return;
		const el = e.currentTarget as HTMLElement;
		pending = { t, el, x: e.clientX, y: e.clientY, touch: e.pointerType === 'touch' };
		// Touch: hold still briefly to pick a card up, so swiping still scrolls.
		if (pending.touch) pending.timer = setTimeout(() => pending && begin(pending.x, pending.y), 260);
	}
	function begin(x: number, y: number) {
		if (!pending) return;
		const r = pending.el.getBoundingClientRect();
		drag = { t: pending.t, w: r.width, h: r.height, dx: pending.x - r.left, dy: pending.y - r.top, x, y, ox: r.left, oy: r.top };
		pending = null;
		document.documentElement.classList.add('dragging-card');
		navigator.vibrate?.(8);
	}
	function move(e: PointerEvent) {
		if (pending && !drag) {
			const dist = Math.hypot(e.clientX - pending.x, e.clientY - pending.y);
			if (pending.touch) {
				if (dist > 10) cancelPending(); // a swipe: let it scroll
				return;
			}
			if (dist <= 5) return;
			begin(e.clientX, e.clientY);
		}
		if (!drag) return;
		drag.x = e.clientX;
		drag.y = e.clientY;
		const col = (document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null)?.closest<HTMLElement>('[data-col]')?.dataset.col ?? null;
		over = col;
		autoScroll(e.clientX);
	}
	function cancelPending() {
		if (pending?.timer) clearTimeout(pending.timer);
		pending = null;
	}
	function up() {
		cancelPending();
		if (!drag) return;
		suppressClick = true;
		setTimeout(() => (suppressClick = false), 0);
		const d = drag;
		const to = over;
		over = null;
		document.documentElement.classList.remove('dragging-card');
		if (to && allowed(d.t, to)) {
			glide('vt-card', ghost ?? null, () => {
				drag = null;
				onmove(d.t.id, to);
			}, () => cardEl(d.t.id));
		} else {
			flyBack(d);
		}
	}
	// A drop outside an allowed column: the card returns to where it was.
	function flyBack(d: NonNullable<typeof drag>) {
		const el = ghost;
		if (!el || !canGlide()) {
			drag = null;
			return;
		}
		const a = el.animate([{ transform: el.style.transform }, { transform: `translate3d(${d.ox}px, ${d.oy}px, 0)` }], { duration: 220, easing: 'cubic-bezier(0.16, 1, 0.3, 1)' });
		const done = () => {
			if (drag === d) drag = null;
		};
		a.finished.then(done, done);
		setTimeout(done, 260); // a background tab may never report the end
	}
	// Near the board's edges the board scrolls sideways.
	function autoScroll(x: number) {
		if (!board) return;
		const r = board.getBoundingClientRect();
		if (x > r.right - 48) board.scrollLeft += 14;
		else if (x < r.left + 48) board.scrollLeft -= 14;
	}
	function cancelDrag() {
		cancelPending();
		if (drag) flyBack(drag);
		over = null;
		document.documentElement.classList.remove('dragging-card');
	}
	onMount(() => {
		// Hold the page still while a card is carried on a touch screen.
		const block = (e: TouchEvent) => drag && e.cancelable && e.preventDefault();
		window.addEventListener('touchmove', block, { passive: false });
		return () => window.removeEventListener('touchmove', block);
	});

	function click(t: Ticket) {
		if (suppressClick) return;
		onopen(t.id);
	}
	function cardKey(e: KeyboardEvent, t: Ticket) {
		if (e.key === 'Enter') {
			e.preventDefault();
			onopen(t.id);
		} else if (keys && /^[1-4]$/.test(e.key) && shortcutAllowed(e)) {
			const s = statusKeys[Number(e.key) - 1];
			if (!allowed(t, s)) return;
			e.preventDefault();
			e.stopPropagation();
			glide('vt-card', cardEl(t.id), () => onmove(t.id, s), () => cardEl(t.id)).then(() => cardEl(t.id)?.focus({ preventScroll: true }));
		}
	}
	const dropOk = (s: string) => !!drag && allowed(drag.t, s);
</script>

<svelte:window onpointermove={move} onpointerup={up} onpointercancel={cancelDrag} onkeydown={(e) => e.key === 'Escape' && drag && (e.preventDefault(), cancelDrag())} onblur={cancelDrag} />

{#snippet cardBody(t: Ticket)}
	{@const tone = idleTone(t, now)}
	{@const labels = (t.labels ?? []).map(labelById).filter((l) => !!l)}
	<p class="flex items-center gap-2">
		<span class="ticket-key">{ticketKey(t)}</span>
		<span class="flex-1"></span>
		{#if drafts.get(t.id)}<span class="text-action" title="Unsent draft"><Icon name="pencil" size={11} /></span>{/if}
		<time class="idle text-label tabular-nums" data-tone={tone} title="Opened {fmtWhen(t.created_at_ms)} · last activity {fmtWhen(t.updated_at_ms)}">{fmtIdle(t.updated_at_ms, now)}</time>
	</p>
	<p class="mt-1 line-clamp-2 text-small font-medium break-words">{t.subject}</p>
	{#if labels.length}<p class="mt-1.5 flex flex-wrap gap-1">{#each labels as l (l.id)}<LabelChip label={l} />{/each}</p>{/if}
	<p class="mt-2 flex items-center gap-2 text-label text-muted">
		<PriorityIcon priority={t.priority} />
		<span class="min-w-0 flex-1 truncate">{staff ? (t.requester?.name ?? t.requester_email) : categoryLabel(t.category)}</span>
		{#if staff}<PersonAvatar person={t.assignee ?? null} size={20} />{/if}
	</p>
{/snippet}

<div bind:this={board} class="board" class:carrying={!!drag} role="list" aria-label="Tickets by status">
	{#each columns as col (col.status)}
		<section
			class="col"
			data-col={col.status}
			class:over={over === col.status && dropOk(col.status)}
			class:closed-to={!!drag && drag.t.status !== col.status && !dropOk(col.status)}
			aria-label="{names[col.status]}, {col.all.length} ticket{col.all.length === 1 ? '' : 's'}"
			role="listitem"
		>
			<header class="col-head">
				<span class="lozenge" data-s={col.status}><span class="lozenge-dot" aria-hidden="true"></span>{names[col.status]}</span>
				<span class="count text-small text-muted tabular-nums">
					{#key col.all.length}<span class="inline-block" in:fly={{ y: -6, duration: 220, easing: cubicOut }}>{col.all.length}</span>{/key}
				</span>
			</header>
			<ul class="col-body">
				{#if over === col.status && dropOk(col.status) && drag}
					<li class="drop-slot" style:height="{drag.h}px" aria-hidden="true"></li>
				{/if}
				{#each col.cards as t (t.id)}
					<li>
						<div
							role="button"
							class="card-item"
							class:lifted={drag?.t.id === t.id}
							class:selected={selected === t.id}
							class:assigned-me={staff && !!me && t.assignee_id === me}
							class:movable={movable(t)}
							data-ticket={t.id}
							tabindex="0"
							aria-label="{ticketKey(t)} {t.subject}"
							aria-roledescription={movable(t) ? 'movable card' : undefined}
							onpointerdown={(e) => down(e, t)}
							onclick={() => click(t)}
							onkeydown={(e) => cardKey(e, t)}
							oncontextmenu={(e) => drag && e.preventDefault()}
						>
							{@render cardBody(t)}
						</div>
					</li>
				{:else}
					{#if !(over === col.status && drag)}<li class="col-empty">{mode === 'manage' ? 'Drop a ticket here' : 'No tickets'}</li>{/if}
				{/each}
				{#if col.all.length > col.cards.length}
					<li class="col-empty">{col.all.length - col.cards.length} older closed tickets: use the table or a search to find them.</li>
				{/if}
			</ul>
		</section>
	{/each}
</div>

{#if drag}
	<div bind:this={ghost} class="ghost card-item" style:width="{drag.w}px" style:transform="translate3d({drag.x - drag.dx}px, {drag.y - drag.dy}px, 0)" aria-hidden="true">
		<div class="ghost-inner">{@render cardBody(drag.t)}</div>
	</div>
{/if}

{#if mode !== 'none'}
	<p class="mt-2 text-small text-muted">
		{mode === 'manage' ? 'Drag a card to another column' : 'Drag a card to Closed to close it, or a resolved one back to Open'} (hold it first on a touch screen), or focus it and press <kbd class="key-hint">1</kbd>–<kbd class="key-hint">4</kbd>.
	</p>
{/if}

<style>
	.board {
		display: grid;
		grid-template-columns: repeat(4, minmax(15rem, 1fr));
		gap: 0.75rem;
		overflow-x: auto;
		padding-bottom: 0.25rem;
	}
	.col {
		display: flex;
		min-height: 18rem;
		max-height: calc(100dvh - 3.5rem - 14rem);
		flex-direction: column;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-card);
		background: color-mix(in srgb, var(--color-paper-2) 45%, var(--color-paper));
		transition:
			border-color 160ms,
			background-color 160ms,
			opacity 160ms,
			box-shadow 200ms;
	}
	.col.over {
		border-color: color-mix(in srgb, var(--color-action) 60%, transparent);
		background: color-mix(in srgb, var(--color-action) 7%, var(--color-paper));
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-action) 35%, transparent);
	}
	/* While carrying a card, columns it cannot go to step back. */
	.col.closed-to {
		opacity: 0.5;
	}
	.col-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 0.6rem 0.75rem 0.4rem;
	}
	.col-body {
		display: grid;
		align-content: start;
		gap: 0.5rem;
		min-height: 0;
		overflow-y: auto;
		padding: 0.25rem 0.5rem 0.6rem;
		scrollbar-width: thin;
	}
	.col-empty {
		border: 1px dashed var(--color-rule);
		border-radius: var(--radius-tile);
		padding: 0.9rem 0.75rem;
		text-align: center;
		font-size: var(--text-small);
		color: var(--color-muted);
	}
	/* Where the card will land: it becomes the most recently updated. */
	.drop-slot {
		border: 1.5px dashed color-mix(in srgb, var(--color-action) 65%, transparent);
		border-radius: var(--radius-tile);
		background: color-mix(in srgb, var(--color-action) 8%, transparent);
		animation: slot-in 200ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes slot-in {
		from {
			opacity: 0;
			transform: scaleY(0.6);
		}
	}
	.card-item {
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-tile);
		background: var(--color-panel);
		padding: 0.6rem 0.7rem;
		box-shadow: 0 1px 2px rgb(20 18 16 / 0.05);
		cursor: pointer;
		outline: none;
		user-select: none;
		-webkit-touch-callout: none;
		transition:
			border-color 120ms,
			box-shadow 180ms,
			transform 180ms cubic-bezier(0.16, 1, 0.3, 1),
			opacity 140ms;
	}
	.card-item.movable {
		cursor: grab;
	}
	.card-item:hover {
		border-color: var(--color-rule);
		box-shadow: 0 4px 12px -4px rgb(20 18 16 / 0.18);
		transform: translateY(-1px);
	}
	.card-item:focus-visible {
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-action) 60%, transparent);
	}
	.card-item.selected {
		border-color: color-mix(in srgb, var(--color-action) 60%, transparent);
		background: color-mix(in srgb, var(--color-action) 6%, var(--color-panel));
	}
	/* The card being carried leaves a faint outline where it was. */
	.card-item.lifted {
		opacity: 0.35;
		transform: scale(0.98);
		box-shadow: none;
		border-style: dashed;
	}
	.carrying .card-item:hover {
		transform: none;
	}
	.ghost {
		position: fixed;
		top: 0;
		left: 0;
		z-index: 70;
		pointer-events: none;
		cursor: grabbing;
		border-color: color-mix(in srgb, var(--color-action) 45%, var(--color-rule));
		box-shadow:
			0 18px 40px -14px rgb(20 18 16 / 0.45),
			0 2px 6px rgb(20 18 16 / 0.12);
		transition: none;
	}
	.ghost-inner {
		animation: lift 160ms cubic-bezier(0.16, 1, 0.3, 1) both;
		transform-origin: 30% 20%;
		transform: rotate(-1.5deg) scale(1.03);
	}
	@keyframes lift {
		from {
			transform: none;
		}
	}
	:global(html.dragging-card),
	:global(html.dragging-card *) {
		cursor: grabbing !important;
		user-select: none !important;
	}
	.card-item.assigned-me .ticket-key::before {
		content: '';
		display: inline-block;
		width: 0.375rem;
		height: 0.375rem;
		margin-right: 0.375rem;
		vertical-align: 0.1em;
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
	@media (prefers-reduced-motion: reduce) {
		.ghost-inner {
			animation: none;
			transform: none;
		}
		.card-item:hover,
		.card-item.lifted {
			transform: none;
		}
		.drop-slot {
			animation: fade 160ms ease-out both;
		}
	}
</style>
