<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { type Ticket, ticketKey, idleTone, fmtIdle, categoryLabel, drafts } from '$lib/api/support';
	import { labelById } from '$lib/supportData.svelte';
	import StatusLozenge from './StatusLozenge.svelte';
	import PriorityIcon from './PriorityIcon.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import LabelChip from './LabelChip.svelte';

	// A ticket in the list and split layouts: key, subject and idle time;
	// then priority, the requester (staff) or category, labels, a draft mark,
	// the assignee and the status.
	let {
		t,
		staff,
		now,
		selected = false,
		dim = false,
		showStatus = true,
		onselect
	}: {
		t: Ticket;
		staff: boolean;
		now: number;
		selected?: boolean;
		dim?: boolean;
		showStatus?: boolean;
		/** Called on a plain click; the link opens the page otherwise. */
		onselect?: (e: MouseEvent) => void;
	} = $props();

	const tone = $derived(idleTone(t, now));
	const draft = $derived(drafts.get(t.id));
	const labels = $derived((t.labels ?? []).map(labelById).filter((l) => !!l));
	const mine = $derived(!!t.assignee_id && t.assignee_id === session.user?.id);
	const idleTitle = $derived(
		t.status === 'open' ? `Waiting for staff; no activity for ${fmtIdle(t.updated_at_ms, now)} (since ${fmtWhen(t.updated_at_ms)})` : `Last activity ${fmtWhen(t.updated_at_ms)}`
	);
</script>

<a
	class="ticket-row relative block px-4 py-3"
	href="/support/{ticketKey(t)}"
	data-ticket={t.id}
	aria-current={selected ? 'true' : undefined}
	class:dim
	class:assigned-me={staff && mine}
	onclick={(e) => {
		if (!onselect || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		onselect(e);
	}}
>
	{#if selected}<span class="ticket-ink" aria-hidden="true"></span>{/if}
	<span class="relative flex items-baseline gap-2">
		<span class="ticket-key shrink-0">{ticketKey(t)}</span>
		<span class="min-w-0 flex-1 truncate font-medium" title={t.subject}>{t.subject}</span>
		<time class="idle shrink-0 text-small tabular-nums" data-tone={tone} datetime={new Date(t.updated_at_ms).toISOString()} title={idleTitle}>
			{#if tone !== 'calm'}<Icon name="clock" size={12} class="mr-0.5 inline align-[-1px]" />{/if}{fmtIdle(t.updated_at_ms, now)}
		</time>
	</span>
	<span class="relative mt-1.5 flex min-w-0 items-center gap-2 text-small text-muted">
		<PriorityIcon priority={t.priority} />
		{#if staff}
			<PersonAvatar person={t.requester ?? null} size={16} />
			<span class="min-w-0 truncate">{t.requester?.name ?? t.requester_email}</span>
		{:else}
			<span class="min-w-0 truncate">{[categoryLabel(t.category), t.bot_name].filter(Boolean).join(' · ')}</span>
		{/if}
		{#each labels.slice(0, 2) as l (l.id)}<LabelChip label={l} class="shrink-0" />{/each}
		{#if labels.length > 2}<span class="shrink-0 text-label">+{labels.length - 2}</span>{/if}
		<span class="flex-1"></span>
		{#if draft}<span class="flex shrink-0 items-center gap-1 text-action" title="You have an unsent {draft.internal ? 'note' : 'reply'}"><Icon name="pencil" size={11} />Draft</span>{/if}
		{#if t.watching}<span class="shrink-0" title="You watch this ticket"><Icon name="eye" size={12} /></span>{/if}
		{#if staff}<PersonAvatar person={t.assignee ?? null} size={18} />{/if}
		{#if showStatus}<StatusLozenge status={t.status} {staff} class="shrink-0" />{/if}
	</span>
</a>

<style>
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
	/* Assigned to me: a quiet accent dot before the key. */
	.ticket-row.assigned-me .ticket-key::before {
		content: '';
		display: inline-block;
		width: 0.375rem;
		height: 0.375rem;
		margin-right: 0.375rem;
		vertical-align: 0.1em;
		border-radius: var(--radius-pill);
		background: var(--color-action);
	}
	/* The selection is a translucent slab with the sidebar's accent tick; a
	   View Transition slides it from row to row (app.css, vt-ticket-ink). */
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
</style>
