<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { type TicketView, ticketPriorities, ticketStatuses, statusLabel, staffStatusLabel, statusTone, categoryLabel } from '$lib/api/support';

	const id = $derived(page.params.id ?? '');
	let view = $state<TicketView | null>(null);
	let error = $state('');
	let notFound = $state(false);
	let reply = $state('');
	let internal = $state(false);
	let sending = $state(false);
	let staff = $state<{ id: string; email: string; display_name: string }[]>([]);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		error = '';
		try {
			view = await api<TicketView>('GET', `/tickets/${id}`);
			if (view.manage && !staff.length) {
				staff = (await api<{ staff: typeof staff }>('GET', '/tickets/staff')).staff;
			}
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) notFound = true;
			else error = msg(e);
		}
	}
	$effect(() => {
		if (id) load();
	});

	async function send(e: SubmitEvent) {
		e.preventDefault();
		sending = true;
		error = '';
		try {
			view = await api<TicketView>('POST', `/tickets/${id}/messages`, { body: reply, internal });
			reply = '';
			toast(internal ? 'Internal note added' : 'Reply sent');
			internal = false;
		} catch (err) {
			error = msg(err);
		} finally {
			sending = false;
		}
	}
	async function change(patch: Record<string, string>) {
		error = '';
		try {
			view = await api<TicketView>('PATCH', `/tickets/${id}`, patch);
		} catch (e) {
			error = msg(e);
			await load();
		}
	}
	async function close() {
		const ok = await confirmDialog({ title: 'Close this ticket?', body: 'You cannot reply to a closed ticket; open a new one if you need more help.', confirmLabel: 'Close ticket' });
		if (ok) await change({ status: 'closed' });
	}
	const t = $derived(view?.ticket);
	const labels = $derived(view?.staff ? staffStatusLabel : statusLabel);
	const canReply = $derived(!!view && (view.manage || (view.requester && t?.status !== 'closed')));
</script>

<svelte:head><title>{t ? `#${t.number} ${t.subject}` : 'Ticket'} · RivetPanel</title></svelte:head>

<a class="link inline-flex items-center gap-1 text-small" href={view?.staff && !view.requester ? '/support?queue=all' : '/support'}><Icon name="chevronLeft" size={14} />Support</a>

{#if notFound}
	<Notice tone="warn" class="mt-4">This ticket does not exist or you cannot see it.</Notice>
{:else if view && t}
	<header class="mt-3 flex flex-wrap items-start gap-3 border-b border-rule-soft pb-5">
		<div class="min-w-0 flex-1">
			<p class="font-mono text-small text-muted">#{t.number}</p>
			<h1 class="text-page break-words">{t.subject}</h1>
			<p class="mt-1 text-small text-muted">
				{categoryLabel(t.category)} · {t.priority} priority · opened {fmtWhen(t.created_at_ms)}
				{#if t.bot_name} · about {#if t.bot_id && view.requester}<a class="link" href="/bots/{t.bot_id}">{t.bot_name}</a>{:else}{t.bot_name}{/if}{/if}
				{#if view.staff} · by {t.requester_email}{/if}
			</p>
		</div>
		<span class="rounded-pill border px-2.5 py-1 text-small {statusTone[t.status]}">{labels[t.status]}</span>
	</header>

	{#if view.manage}
		<div class="mt-5 grid gap-3 sm:grid-cols-3">
			<label class="block"><span class="label">Status</span>
				<select class="field" value={t.status} onchange={(e) => change({ status: e.currentTarget.value })}>
					{#each ticketStatuses as s (s)}<option value={s}>{staffStatusLabel[s]}</option>{/each}
				</select>
			</label>
			<label class="block"><span class="label">Priority</span>
				<select class="field" value={t.priority} onchange={(e) => change({ priority: e.currentTarget.value })}>
					{#each ticketPriorities as p (p)}<option value={p}>{p}</option>{/each}
				</select>
			</label>
			<label class="block"><span class="label">Assigned to</span>
				<select class="field" value={t.assignee_id ?? ''} onchange={(e) => change({ assignee_id: e.currentTarget.value })}>
					<option value="">Nobody</option>
					{#each staff as s (s.id)}<option value={s.id}>{s.display_name ? `${s.display_name} (${s.email})` : s.email}{s.id === session.user?.id ? ' — me' : ''}</option>{/each}
				</select>
			</label>
		</div>
	{:else if view.staff}
		<p class="mt-4 text-small text-muted">You can read this ticket and its internal notes but not answer it.{#if t.assignee_email} Assigned to {t.assignee_email}.{/if}</p>
	{/if}

	{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

	<ol class="mt-6 grid gap-3" aria-label="Conversation">
		{#each view.messages as m (m.id)}
			{#if m.kind === 'event'}
				<li class="flex items-center gap-2 px-1 text-small text-muted">
					<Icon name="history" size={13} />{m.body} · {m.author} · {fmtWhen(m.created_at_ms)}{#if m.internal}<span class="rounded-pill border border-warn/30 px-1.5 text-[11px] text-warn">staff only</span>{/if}
				</li>
			{:else}
				<li class="rounded-card border p-4 {m.internal ? 'border-warn/40 bg-warn/6' : m.staff ? 'border-action/25 bg-action/4' : 'border-rule-soft bg-raised'}">
					<div class="flex flex-wrap items-center gap-2 text-small">
						<span class="font-medium">{m.mine ? 'You' : m.author}</span>
						{#if m.staff && !m.internal}<span class="rounded-pill border border-action/30 px-1.5 text-[11px] text-action">support</span>{/if}
						{#if m.internal}<span class="rounded-pill border border-warn/40 px-1.5 text-[11px] text-warn">internal note · staff only</span>{/if}
						<span class="ml-auto text-muted">{fmtWhen(m.created_at_ms)}</span>
					</div>
					<p class="mt-2 break-words whitespace-pre-wrap">{m.body}</p>
				</li>
			{/if}
		{/each}
	</ol>

	{#if canReply}
		<form class="card mt-6 grid gap-3 p-4" onsubmit={send}>
			<label class="block"><span class="label">{internal ? 'Internal note (the requester never sees it)' : 'Reply'}</span>
				<textarea class="field min-h-28" required maxlength="10000" bind:value={reply}></textarea>
			</label>
			<div class="flex flex-wrap items-center gap-3">
				<button class="btn btn-primary" disabled={sending}><Icon name="send" size={14} />{internal ? 'Add note' : 'Send reply'}</button>
				{#if view.manage}<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={internal} />Internal note</label>{/if}
				{#if view.requester && !view.manage}
					{#if t.status === 'resolved'}<button type="button" class="btn btn-quiet" onclick={() => change({ status: 'open' })}>Reopen</button>{/if}
					<button type="button" class="btn btn-quiet ml-auto" onclick={close}>Close ticket</button>
				{/if}
			</div>
		</form>
	{:else if view.requester && t.status === 'closed'}
		<Notice class="mt-6">This ticket is closed. <a class="link" href="/support">Open a new ticket</a> if you need more help.</Notice>
	{/if}
{:else if error}
	<Notice tone="fail" class="mt-4" live>{error}</Notice>
{/if}
