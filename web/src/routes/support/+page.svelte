<script lang="ts">
	import { untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { can } from '$lib/session.svelte';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import type { KBArticle } from '$lib/api/kb';
	import { type Ticket, ticketCategories, ticketPriorities, ticketStatuses, statusLabel, staffStatusLabel, statusTone, categoryLabel } from '$lib/api/support';

	const isStaff = $derived(can('tickets.view_all') || can('tickets.manage'));
	const canOpen = $derived(can('tickets.create'));
	const queue = $derived(page.url.searchParams.get('queue') === 'all' && isStaff);

	let tickets = $state<Ticket[]>([]);
	let status = $state('active');
	let mineOnly = $state(false);
	let loaded = $state(false);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		error = '';
		try {
			const q = new URLSearchParams({ scope: queue ? 'all' : 'mine' });
			if (status !== 'any') q.set('status', status);
			if (queue && mineOnly) q.set('assigned', 'me');
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
		untrack(() => load());
	});

	// New ticket
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
	const labels = $derived(queue ? staffStatusLabel : statusLabel);

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
</script>

<svelte:head><title>Support · RivetPanel</title></svelte:head>

<header class="flex flex-wrap items-end gap-3 border-b border-rule-soft pb-5">
	<div class="min-w-0 flex-1">
		<h1 class="text-page">{queue ? 'Support queue' : 'Support'}</h1>
		<p class="mt-0.5 text-muted">{queue ? 'Every ticket you may answer or read.' : 'Ask the people who run this panel for help. Only you and the support team see your tickets.'}</p>
	</div>
	{#if isStaff}
		<nav class="flex gap-1 rounded-control border border-rule-soft p-0.5 text-small" aria-label="Ticket views">
			{#if canOpen}<a class="rounded-control px-3 py-1 {queue ? 'text-muted' : 'bg-raised font-medium'}" href="/support" aria-current={!queue ? 'page' : undefined}>My tickets</a>{/if}
			<a class="rounded-control px-3 py-1 {queue ? 'bg-raised font-medium' : 'text-muted'}" href="/support?queue=all" aria-current={queue ? 'page' : undefined}>Queue</a>
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
				<select class="field" bind:value={form.priority}>{#each ticketPriorities as p (p)}<option value={p}>{p}</option>{/each}</select>
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

<div class="mt-6 flex flex-wrap items-center gap-3">
	<label class="flex items-center gap-2 text-small"><span class="text-muted">Status</span>
		<select class="field h-8 w-auto py-0" bind:value={status} onchange={load}>
			<option value="active">Open and pending</option>
			<option value="any">Any</option>
			{#each ticketStatuses as s (s)}<option value={s}>{labels[s]}</option>{/each}
		</select>
	</label>
	{#if queue && can('tickets.manage')}<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={mineOnly} onchange={load} />Assigned to me</label>{/if}
</div>

<div class="mt-4">
	{#if loaded && !tickets.length}
		<EmptyState title="No tickets">
			{#if queue}Nothing matches this filter.{:else if canOpen}Open a ticket when you need help; answers also arrive under the bell in the top bar.{:else}Your role cannot open support tickets.{/if}
		</EmptyState>
	{:else}
		<ul class="card divide-y divide-rule-soft overflow-hidden">
			{#each tickets as t (t.id)}
				<li>
					<a class="flex flex-wrap items-center gap-3 px-4 py-3 hover:bg-paper/60" href="/support/{t.id}">
						<span class="w-14 shrink-0 font-mono text-small text-muted">#{t.number}</span>
						<span class="min-w-0 flex-1">
							<span class="block truncate font-medium">{t.subject}</span>
							<span class="block text-small text-muted">
								{[categoryLabel(t.category), `${t.priority} priority`, t.bot_name, queue && t.requester_email, queue && t.assignee_email && `assigned to ${t.assignee_email}`].filter(Boolean).join(' · ')}
							</span>
						</span>
						<span class="text-small text-muted" title={fmtWhen(t.updated_at_ms)}>{fmtAgo(t.updated_at_ms)} · {t.messages} message{t.messages === 1 ? '' : 's'}</span>
						<span class="rounded-pill border px-2 py-0.5 text-[12px] {statusTone[t.status]}">{labels[t.status]}</span>
					</a>
				</li>
			{/each}
		</ul>
	{/if}
</div>
