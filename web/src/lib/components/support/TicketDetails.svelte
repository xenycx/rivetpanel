<script lang="ts">
	import { api } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { fmtWhen, fmtAgo } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import {
		type TicketView,
		type Person,
		ticketCategories,
		ticketPriorities,
		priorityLabel,
		statusKeys,
		staffStatusLabel,
		staffStatusShort,
		categoryLabel
	} from '$lib/api/support';
	import { support, loadLabels, loadStaff, labelById } from '$lib/supportData.svelte';
	import StatusLozenge from './StatusLozenge.svelte';
	import PriorityIcon from './PriorityIcon.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import LabelChip from './LabelChip.svelte';
	import Picker from './Picker.svelte';

	// The ticket's fields, like an issue tracker's details panel. `aside` is
	// the sidebar of the ticket page; `strip` the compact row of the desk and
	// the peek panel. Staff who manage tickets change everything; the
	// requester changes category and linked app while the ticket is open;
	// view-only staff read.
	let {
		view,
		variant = 'aside',
		onchange,
		onstatus,
		onassignme,
		onlabels,
		onwatch,
		onmanagelabels
	}: {
		view: TicketView;
		variant?: 'aside' | 'strip';
		onchange: (patch: Record<string, string>) => Promise<boolean>;
		onstatus: (s: string) => void;
		onassignme: () => void;
		onlabels: (ids: string[]) => void;
		onwatch: (on: boolean) => void;
		onmanagelabels?: () => void;
	} = $props();

	const t = $derived(view.ticket);
	const me = $derived(session.user?.id ?? '');
	const manage = $derived(view.manage);
	const editable = $derived(manage || (view.requester && (t.status === 'open' || t.status === 'pending')));
	$effect(() => {
		if (view.staff) loadLabels();
		if (manage) loadStaff();
	});

	const staffPeople = $derived<Person[]>(
		support.staff.map((s) => ({ id: s.id, name: s.display_name || s.email, email: s.email, avatar_url: s.avatar_url, logo_variant: s.logo_variant }))
	);
	const assigneeOptions = $derived([
		{ value: '', text: 'Unassigned', data: null as Person | null },
		...staffPeople.map((p) => ({ value: p.id, text: p.id === me ? `${p.name} (me)` : p.name, data: (p.id === t.assignee?.id ? t.assignee : p) as Person | null }))
	]);
	const priorityOptions = ticketPriorities.map((p) => ({ value: p, text: priorityLabel(p) }));
	const categoryOptions = ticketCategories.map((c) => ({ value: c.value, text: c.label }));
	const labelOptions = $derived(support.labels.map((l) => ({ value: l.id, text: l.name, data: l })));
	const labels = $derived((t.labels ?? []).map(labelById).filter((l) => !!l));

	// Linked app: the requester picks among the apps it can reach now.
	let bots = $state<Bot[] | null>(null);
	async function loadBots() {
		if (bots || !view.requester) return;
		try {
			bots = (await api<{ bots: Bot[] }>('GET', '/bots')).bots;
		} catch {
			bots = [];
		}
	}
	const appOptions = $derived([
		{ value: '', text: 'Nothing specific' },
		...(view.requester ? (bots ?? []).map((b) => ({ value: b.id, text: b.name })) : t.bot_id ? [{ value: t.bot_id, text: t.bot_name }] : [])
	]);
</script>

{#snippet assigneeRow(o: { text: string; data?: Person | null }, _on: boolean)}
	<PersonAvatar person={o.data ?? null} size={20} /><span class="truncate">{o.text}</span>
{/snippet}
{#snippet plainRow(o: { value: string; text: string }, _on: boolean)}
	<span class="truncate">{o.text}</span>
{/snippet}
{#snippet priorityRow(o: { value: string; text: string }, _on: boolean)}
	<PriorityIcon priority={o.value} label />
{/snippet}
{#snippet labelRow(o: { data?: { name: string; color: import('$lib/api/support').LabelColor } }, _on: boolean)}
	{#if o.data}<LabelChip label={o.data} />{/if}
{/snippet}
{#snippet labelFooter(close: () => void)}
	<button type="button" class="pick-foot" onclick={() => (close(), onmanagelabels?.())}><Icon name="sliders" size={13} />Manage labels…</button>
{/snippet}

{#snippet statusField()}
	{#if manage}
		<div role="group" aria-label="Status" class="seg status-seg">
			{#each statusKeys as s, i (s)}
				<button type="button" class="seg-item status-opt" data-s={s} aria-pressed={t.status === s} onclick={() => onstatus(s)} title="{staffStatusLabel[s]} (press {i + 1})" aria-keyshortcuts={String(i + 1)}>
					<span class="status-dot" aria-hidden="true"></span>{staffStatusShort[s]}
				</button>
			{/each}
		</div>
	{:else}
		<StatusLozenge status={t.status} staff={view.staff} />
	{/if}
{/snippet}

{#snippet assigneeField()}
	<Picker label="Assignee" options={assigneeOptions} value={[t.assignee_id ?? '']} disabled={!manage} onpick={([v]) => onchange({ assignee_id: v })} row={assigneeRow} width="17rem">
		{#snippet trigger()}
			<PersonAvatar person={t.assignee ?? null} size={20} /><span class="truncate">{t.assignee ? (t.assignee.id === me ? 'You' : t.assignee.name) : 'Unassigned'}</span>
		{/snippet}
	</Picker>
{/snippet}

{#snippet priorityField()}
	<Picker label="Priority" options={priorityOptions} value={[t.priority]} disabled={!manage} onpick={([v]) => onchange({ priority: v })} row={priorityRow} width="11rem">
		{#snippet trigger()}<PriorityIcon priority={t.priority} label />{/snippet}
	</Picker>
{/snippet}

{#snippet labelsField()}
	<Picker label="Labels" options={labelOptions} value={t.labels ?? []} multi disabled={!manage} onpick={onlabels} row={labelRow} footer={manage ? labelFooter : undefined} width="15rem" empty="No labels yet">
		{#snippet trigger()}
			{#if labels.length}
				<span class="flex flex-wrap gap-1">{#each labels as l (l.id)}<LabelChip label={l} />{/each}</span>
			{:else}
				<span class="text-muted">{manage ? 'Add labels' : 'None'}</span>
			{/if}
		{/snippet}
	</Picker>
{/snippet}

{#snippet watchField()}
	<button type="button" class="picker-trigger" aria-pressed={!!t.watching} onclick={() => onwatch(!t.watching)} title={t.watching ? 'Stop watching: no more notifications unless assigned' : 'Watch: get this ticket’s notifications'}>
		<Icon name={t.watching ? 'eye' : 'eyeOff'} size={14} />{t.watching ? 'Watching' : 'Watch'}
		{#if view.watchers?.length}
			<span class="flex -space-x-1.5">{#each view.watchers.slice(0, 4) as w (w.id)}<PersonAvatar person={w} size={18} class="ring-2 ring-panel" />{/each}</span>
		{/if}
	</button>
{/snippet}

{#if variant === 'strip'}
	<div class="flex flex-wrap items-center gap-x-1.5 gap-y-2">
		{@render statusField()}
		{#if view.staff}
			{@render assigneeField()}
			{@render priorityField()}
			{@render labelsField()}
			{@render watchField()}
		{:else}
			<PriorityIcon priority={t.priority} label class="px-2 text-small" />
		{/if}
	</div>
{:else}
	<aside class="details" aria-label="Details">
		<div class="mb-3">{@render statusField()}</div>
		{#if manage && t.assignee_id !== me}
			<button type="button" class="btn btn-quiet mb-3 h-8 w-full justify-center text-small" onclick={onassignme} aria-keyshortcuts="a">Assign to me<kbd class="key-hint">A</kbd></button>
		{/if}
		<dl class="details-grid">
			{#if view.staff}
				<dt>Assignee</dt>
				<dd>{@render assigneeField()}</dd>
				<dt>Requester</dt>
				<dd><span class="flex min-w-0 items-center gap-1.5 px-2 py-1"><PersonAvatar person={t.requester ?? null} size={20} /><span class="truncate" title={t.requester_email}>{t.requester?.name ?? t.requester_email}</span></span></dd>
			{/if}
			<dt>Priority</dt>
			<dd>{@render priorityField()}</dd>
			{#if view.staff}
				<dt>Labels</dt>
				<dd>{@render labelsField()}</dd>
			{/if}
			<dt>Category</dt>
			<dd>
				<Picker label="Category" options={categoryOptions} value={[t.category]} disabled={!editable} onpick={([v]) => onchange({ category: v })} row={plainRow} width="13rem">
					{#snippet trigger()}<span class="truncate">{categoryLabel(t.category)}</span>{/snippet}
				</Picker>
			</dd>
			<dt>App</dt>
			<dd>
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<span class="contents" onpointerenter={loadBots} onfocusin={loadBots}>
					<Picker label="Linked app" options={appOptions} value={[t.bot_id ?? '']} disabled={!editable || (!view.requester && !t.bot_id)} onpick={([v]) => onchange({ bot_id: v })} row={plainRow} width="15rem">
						{#snippet trigger()}<Icon name="box" size={13} class="text-muted" /><span class="truncate">{t.bot_name || 'Nothing specific'}</span>{/snippet}
					</Picker>
				</span>
				{#if t.bot_id && view.requester}<a class="link ml-2 text-small" href="/bots/{t.bot_id}">Open</a>{/if}
			</dd>
			{#if view.staff}
				<dt>Watchers</dt>
				<dd>{@render watchField()}</dd>
			{/if}
			<dt>Created</dt>
			<dd class="px-2 py-1 text-small" title={fmtWhen(t.created_at_ms)}>{fmtWhen(t.created_at_ms)}</dd>
			<dt>Updated</dt>
			<dd class="px-2 py-1 text-small" title={fmtWhen(t.updated_at_ms)}>{fmtAgo(t.updated_at_ms)}</dd>
		</dl>
		{#if view.requester && !manage && !editable}
			<p class="mt-3 text-small text-muted">Details can be changed while the ticket is open.</p>
		{/if}
	</aside>
{/if}

<style>
	.details {
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-card);
		background: var(--color-panel);
		padding: 1rem;
	}
	.details-grid {
		display: grid;
		grid-template-columns: 5.5rem minmax(0, 1fr);
		align-items: center;
		gap: 0.25rem 0.5rem;
	}
	.details-grid dt {
		font-size: var(--text-small);
		color: var(--color-muted);
	}
	.details-grid dd {
		min-width: 0;
		display: flex;
		align-items: center;
	}
	.status-seg {
		flex-wrap: nowrap;
	}
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
	.details .status-seg {
		display: flex;
		width: 100%;
	}
	.details .status-seg .status-opt {
		flex: 1;
		padding-inline: 0.25rem;
	}
</style>
