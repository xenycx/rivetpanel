<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { type Ticket, ticketKey, idleTone, fmtIdle, categoryLabel, drafts } from '$lib/api/support';
	import { labelById } from '$lib/supportData.svelte';
	import { supportView, setSupportSort, type SortKey } from '$lib/ui/supportLayout.svelte';
	import StatusLozenge from './StatusLozenge.svelte';
	import PriorityIcon from './PriorityIcon.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import LabelChip from './LabelChip.svelte';

	// The table layout: sortable columns, a sticky header, the selected row
	// marked; clicking a row (or Enter on it) opens the ticket in the peek
	// panel. Ctrl/⌘-click still opens the ticket's own page.
	let {
		tickets,
		staff,
		now,
		selected = null,
		onopen
	}: { tickets: Ticket[]; staff: boolean; now: number; selected?: string | null; onopen: (id: string) => void } = $props();

	const cols = $derived<{ key: SortKey; label: string; cls?: string }[]>([
		{ key: 'key', label: 'Key', cls: 'w-28' },
		{ key: 'subject', label: 'Subject' },
		{ key: 'status', label: 'Status', cls: 'w-32' },
		{ key: 'priority', label: 'Priority', cls: 'w-28' },
		...(staff
			? [
					{ key: 'requester' as SortKey, label: 'Requester', cls: 'w-48' },
					{ key: 'assignee' as SortKey, label: 'Assignee', cls: 'w-44' }
				]
			: []),
		{ key: 'updated', label: 'Updated', cls: 'w-24 text-right' }
	]);
	const me = $derived(session.user?.id);
</script>

<div class="table-wrap card overflow-auto">
	<table class="w-full border-collapse text-left">
		<thead>
			<tr>
				{#each cols as c (c.key)}
					{@const on = supportView.sort.key === c.key}
					<th scope="col" class={c.cls} aria-sort={on ? (supportView.sort.desc ? 'descending' : 'ascending') : 'none'}>
						<button type="button" class="sort-btn" class:on onclick={() => setSupportSort(c.key)}>
							{c.label}{#if on}<Icon name="chevronDown" size={12} class={supportView.sort.desc ? '' : 'rotate-180'} />{/if}
						</button>
					</th>
				{/each}
			</tr>
		</thead>
		<tbody>
			{#each tickets as t (t.id)}
				{@const tone = idleTone(t, now)}
				{@const labels = (t.labels ?? []).map(labelById).filter((l) => !!l)}
				<tr
					data-ticket={t.id}
					tabindex="0"
					aria-selected={selected === t.id}
					class:assigned-me={staff && !!me && t.assignee_id === me}
					onclick={(e) => (e.metaKey || e.ctrlKey ? window.open(`/support/${ticketKey(t)}`, '_blank') : onopen(t.id))}
					onkeydown={(e) => e.key === 'Enter' && (e.preventDefault(), onopen(t.id))}
				>
					<td><span class="ticket-key">{ticketKey(t)}</span></td>
					<td class="min-w-64">
						<span class="flex min-w-0 items-center gap-2">
							<a class="min-w-0 truncate font-medium hover:underline" href="/support/{ticketKey(t)}" onclick={(e) => !(e.metaKey || e.ctrlKey) && e.preventDefault()} title={t.subject}>{t.subject}</a>
							{#each labels.slice(0, 3) as l (l.id)}<LabelChip label={l} class="shrink-0" />{/each}
							{#if drafts.get(t.id)}<span class="flex shrink-0 items-center gap-1 text-small text-action"><Icon name="pencil" size={11} />Draft</span>{/if}
						</span>
						{#if !staff}<span class="block truncate text-small text-muted">{[categoryLabel(t.category), t.bot_name].filter(Boolean).join(' · ')}</span>{/if}
					</td>
					<td><StatusLozenge status={t.status} {staff} /></td>
					<td class="text-small"><PriorityIcon priority={t.priority} label /></td>
					{#if staff}
						<td><span class="flex min-w-0 items-center gap-2 text-small"><PersonAvatar person={t.requester ?? null} size={20} /><span class="truncate">{t.requester?.name ?? t.requester_email}</span></span></td>
						<td>
							<span class="flex min-w-0 items-center gap-2 text-small" class:text-muted={!t.assignee}>
								<PersonAvatar person={t.assignee ?? null} size={20} /><span class="truncate">{t.assignee ? (t.assignee.id === me ? 'You' : t.assignee.name) : 'Unassigned'}</span>
							</span>
						</td>
					{/if}
					<td class="text-right">
						<time class="idle text-small tabular-nums" data-tone={tone} datetime={new Date(t.updated_at_ms).toISOString()} title={fmtWhen(t.updated_at_ms)}>{fmtIdle(t.updated_at_ms, now)}</time>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
	{#if !tickets.length}<p class="px-4 py-10 text-center text-small text-muted">Nothing matches these filters.</p>{/if}
</div>

<style>
	.table-wrap {
		max-height: calc(100dvh - 3.5rem - 12rem);
		min-height: 16rem;
		scrollbar-width: thin;
	}
	th {
		position: sticky;
		top: 0;
		z-index: 1;
		background: var(--color-panel);
		border-bottom: 1px solid var(--color-rule);
		padding: 0;
		font-size: var(--text-label);
		font-weight: 600;
		color: var(--color-muted);
		white-space: nowrap;
	}
	.sort-btn {
		display: inline-flex;
		width: 100%;
		align-items: center;
		gap: 0.25rem;
		padding: 0.6rem 0.75rem;
		text-align: inherit;
		justify-content: inherit;
	}
	th.text-right .sort-btn {
		justify-content: flex-end;
	}
	.sort-btn:hover,
	.sort-btn.on {
		color: var(--color-ink);
	}
	td {
		border-bottom: 1px solid var(--color-rule-soft);
		padding: 0.55rem 0.75rem;
		vertical-align: middle;
		max-width: 1px;
	}
	td:nth-child(2) {
		max-width: none;
	}
	tr[data-ticket] {
		cursor: default;
		outline: none;
		transition: background-color 120ms;
	}
	tr[data-ticket]:hover {
		background: color-mix(in srgb, var(--color-ink) 3.5%, transparent);
	}
	tr[data-ticket]:focus-visible {
		box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--color-action) 60%, transparent);
	}
	tr[aria-selected='true'] {
		background: color-mix(in srgb, var(--color-action) 9%, transparent);
	}
	tr[aria-selected='true'] .ticket-key {
		color: var(--color-action);
		font-weight: 600;
	}
	tr.assigned-me .ticket-key::before {
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
</style>
