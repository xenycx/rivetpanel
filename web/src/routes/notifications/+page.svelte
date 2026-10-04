<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import { inbox, listNotifications, markRead, deleteNotification, refreshUnread, categoryLabels, type Notification } from '$lib/notifications.svelte';

	let items = $state<Notification[]>([]);
	let unreadOnly = $state(false);
	let more = $state(false);
	let loaded = $state(false);
	let error = $state('');
	const PAGE = 30;
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load(append = false) {
		error = '';
		try {
			const before = append && items.length ? items[items.length - 1].created_at_ms : undefined;
			const page = await listNotifications({ before, unread: unreadOnly, limit: PAGE });
			items = append ? [...items, ...page] : page;
			more = page.length === PAGE;
		} catch (e) {
			error = msg(e);
		} finally {
			loaded = true;
		}
	}
	onMount(() => load());

	async function open(n: Notification) {
		if (!n.read_at_ms) {
			await markRead([n.id]).catch(() => {});
			n.read_at_ms = Date.now();
		}
		if (n.link) goto(n.link);
	}
	async function toggleRead(n: Notification) {
		if (n.read_at_ms) return;
		await markRead([n.id]).catch((e) => (error = msg(e)));
		n.read_at_ms = Date.now();
	}
	async function remove(n: Notification) {
		try {
			await deleteNotification(n.id);
			items = items.filter((x) => x.id !== n.id);
		} catch (e) {
			error = msg(e);
		}
	}
	async function readAll() {
		await markRead('all').catch((e) => (error = msg(e)));
		await load();
	}
	async function clearRead() {
		try {
			await api('POST', '/notifications/clear');
			await refreshUnread();
			await load();
		} catch (e) {
			error = msg(e);
		}
	}
</script>

<svelte:head><title>Notifications · RivetPanel</title></svelte:head>

<header class="flex flex-wrap items-end gap-3 border-b border-rule-soft pb-5">
	<div class="min-w-0 flex-1">
		<h1 class="text-page">Notifications</h1>
		<p class="mt-0.5 text-muted">{inbox.unread ? `${inbox.unread} unread` : 'All caught up.'} Kept for 90 days.</p>
	</div>
	<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={unreadOnly} onchange={() => load()} />Unread only</label>
	<button class="btn btn-sm" onclick={readAll} disabled={!inbox.unread}>Mark all read</button>
	<button class="btn btn-sm" onclick={clearRead}>Delete read</button>
	<a class="btn btn-sm btn-quiet" href="/settings/notifications"><Icon name="gear" size={14} />Preferences</a>
</header>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

<div class="mt-6">
	{#if loaded && !items.length}
		<EmptyState title={unreadOnly ? 'No unread notifications' : 'No notifications'}>Alerts, deployments, backups, sharing, announcements and support replies appear here.</EmptyState>
	{:else}
		<ul class="card divide-y divide-rule-soft overflow-hidden">
			{#each items as n (n.id)}
				<li class="flex items-start gap-3 px-4 py-3 {n.read_at_ms ? '' : 'bg-action/5'}">
					<button class="min-w-0 flex-1 text-left" onclick={() => open(n)}>
						<span class="flex flex-wrap items-center gap-2 text-[11px] text-muted uppercase">
							{#if !n.read_at_ms}<span class="size-1.5 rounded-pill bg-action" aria-label="Unread"></span>{/if}
							{categoryLabels[n.category] ?? n.category}
							<span title={fmtWhen(n.created_at_ms)}>{fmtAgo(n.created_at_ms)}</span>
						</span>
						<span class="mt-0.5 block font-medium break-words">{n.title}</span>
						{#if n.body}<span class="mt-0.5 block text-small break-words whitespace-pre-line text-muted">{n.body}</span>{/if}
					</button>
					{#if !n.read_at_ms}<button class="btn btn-sm btn-quiet" onclick={() => toggleRead(n)}>Mark read</button>{/if}
					<button class="btn btn-quiet btn-icon btn-sm" onclick={() => remove(n)} aria-label="Delete notification" title="Delete"><Icon name="trash" size={14} /></button>
				</li>
			{/each}
		</ul>
		{#if more}<div class="mt-4 text-center"><button class="btn" onclick={() => load(true)}>Load older</button></div>{/if}
	{/if}
</div>
