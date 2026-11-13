<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { type Ticket, ticketKey, idleTone, fmtIdle, statusKeys, staffStatusShort, statusLabel, categoryLabel, drafts, shortcutAllowed } from '$lib/api/support';
	import { labelById } from '$lib/supportData.svelte';
	import PriorityIcon from './PriorityIcon.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import LabelChip from './LabelChip.svelte';

	// The board layout: a column per status. Staff who manage tickets drag a
	// card to another column (or focus it and press 1–4) to change its
	// status; everyone opens a card in the peek panel by clicking it or
	// pressing Enter. Closed shows the most recent cards only.
	let {
		tickets,
		staff,
		now,
		canMove,
		keys = true,
		selected = null,
		onopen,
		onmove
	}: {
		tickets: Ticket[];
		staff: boolean;
		now: number;
		canMove: boolean;
		/** Card shortcuts (off while the peek panel handles keys). */
		keys?: boolean;
		selected?: string | null;
		onopen: (id: string) => void;
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

	let dragging = $state<string | null>(null);
	let over = $state<string | null>(null);
	function dragStart(e: DragEvent, t: Ticket) {
		if (!canMove || !e.dataTransfer) return;
		dragging = t.id;
		e.dataTransfer.effectAllowed = 'move';
		e.dataTransfer.setData('text/plain', t.id);
	}
	function dragOver(e: DragEvent, s: string) {
		if (!dragging) return;
		e.preventDefault();
		over = s;
	}
	function drop(e: DragEvent, s: string) {
		e.preventDefault();
		const id = dragging ?? e.dataTransfer?.getData('text/plain');
		dragging = over = null;
		const t = tickets.find((x) => x.id === id);
		if (t && t.status !== s) onmove(t.id, s);
	}
	function cardKey(e: KeyboardEvent, t: Ticket) {
		if (e.key === 'Enter') {
			e.preventDefault();
			onopen(t.id);
		} else if (keys && canMove && /^[1-4]$/.test(e.key) && shortcutAllowed(e)) {
			e.preventDefault();
			e.stopPropagation();
			const s = statusKeys[Number(e.key) - 1];
			if (s !== t.status) onmove(t.id, s);
		}
	}
</script>

<div class="board" role="list" aria-label="Tickets by status">
	{#each columns as col (col.status)}
		<section
			class="col"
			class:over={over === col.status && dragging && tickets.find((t) => t.id === dragging)?.status !== col.status}
			aria-label="{names[col.status]}, {col.all.length} ticket{col.all.length === 1 ? '' : 's'}"
			role="listitem"
			ondragover={(e) => dragOver(e, col.status)}
			ondragleave={(e) => !(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node) && (over = null)}
			ondrop={(e) => drop(e, col.status)}
		>
			<header class="col-head">
				<span class="lozenge" data-s={col.status}><span class="lozenge-dot" aria-hidden="true"></span>{names[col.status]}</span>
				<span class="text-small text-muted tabular-nums">{col.all.length}</span>
			</header>
			<ul class="col-body">
				{#each col.cards as t (t.id)}
					{@const tone = idleTone(t, now)}
					{@const labels = (t.labels ?? []).map(labelById).filter((l) => !!l)}
					<li>
						<div
							role="button"
							class="card-item"
							class:dragging={dragging === t.id}
							class:selected={selected === t.id}
							class:assigned-me={staff && !!me && t.assignee_id === me}
							data-ticket={t.id}
							draggable={canMove}
							tabindex="0"
							aria-label="{ticketKey(t)} {t.subject}"
							ondragstart={(e) => dragStart(e, t)}
							ondragend={() => (dragging = over = null)}
							onclick={() => onopen(t.id)}
							onkeydown={(e) => cardKey(e, t)}
						>
							<p class="flex items-center gap-2">
								<span class="ticket-key">{ticketKey(t)}</span>
								<span class="flex-1"></span>
								{#if drafts.get(t.id)}<span class="text-action" title="Unsent draft"><Icon name="pencil" size={11} /></span>{/if}
								<time class="idle text-label tabular-nums" data-tone={tone} title={fmtWhen(t.updated_at_ms)}>{fmtIdle(t.updated_at_ms, now)}</time>
							</p>
							<p class="mt-1 line-clamp-2 text-small font-medium break-words">{t.subject}</p>
							{#if labels.length}<p class="mt-1.5 flex flex-wrap gap-1">{#each labels as l (l.id)}<LabelChip label={l} />{/each}</p>{/if}
							<p class="mt-2 flex items-center gap-2 text-label text-muted">
								<PriorityIcon priority={t.priority} />
								<span class="min-w-0 flex-1 truncate">{staff ? (t.requester?.name ?? t.requester_email) : categoryLabel(t.category)}</span>
								{#if staff}<PersonAvatar person={t.assignee ?? null} size={20} />{/if}
							</p>
						</div>
					</li>
				{:else}
					<li class="col-empty">{canMove ? 'Drop a ticket here' : 'No tickets'}</li>
				{/each}
				{#if col.all.length > col.cards.length}
					<li class="col-empty">{col.all.length - col.cards.length} older closed tickets: use the table or a search to find them.</li>
				{/if}
			</ul>
		</section>
	{/each}
</div>
{#if canMove}<p class="mt-2 text-small text-muted">Drag a card to another column, or focus it and press <kbd class="key-hint">1</kbd>–<kbd class="key-hint">4</kbd>, to change its status.</p>{/if}

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
			border-color 140ms,
			background-color 140ms;
	}
	.col.over {
		border-color: color-mix(in srgb, var(--color-action) 55%, transparent);
		background: color-mix(in srgb, var(--color-action) 6%, var(--color-paper));
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
	.card-item {
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-tile);
		background: var(--color-panel);
		padding: 0.6rem 0.7rem;
		box-shadow: 0 1px 2px rgb(20 18 16 / 0.05);
		cursor: pointer;
		outline: none;
		transition:
			border-color 120ms,
			box-shadow 160ms,
			transform 160ms cubic-bezier(0.16, 1, 0.3, 1),
			opacity 120ms;
	}
	.card-item[draggable='true'] {
		cursor: grab;
	}
	.card-item:hover {
		border-color: var(--color-rule);
		box-shadow: 0 4px 12px -4px rgb(20 18 16 / 0.18);
	}
	.card-item:focus-visible {
		box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-action) 60%, transparent);
	}
	.card-item.selected {
		border-color: color-mix(in srgb, var(--color-action) 60%, transparent);
		background: color-mix(in srgb, var(--color-action) 6%, var(--color-panel));
	}
	.card-item.dragging {
		opacity: 0.45;
		transform: rotate(-1.5deg) scale(0.98);
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
		.card-item.dragging {
			transform: none;
		}
	}
</style>
