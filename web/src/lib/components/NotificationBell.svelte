<script lang="ts">
	import { onMount } from 'svelte';
	import { goto, afterNavigate } from '$app/navigation';
	import { poll } from '$lib/poll';
	import { fmtAgo } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { inbox, refreshUnread, listNotifications, markRead, deleteNotification, categoryLabels, type Notification } from '$lib/notifications.svelte';

	// The top-bar bell: unread count (polled every 30 s while the tab is
	// visible, and after every navigation) and the latest notifications.
	let open = $state(false);
	let items = $state<Notification[]>([]);
	let loading = $state(false);
	let failed = $state(false);
	let root: HTMLDivElement | undefined = $state();
	let btn: HTMLButtonElement | undefined = $state();

	onMount(() => poll(refreshUnread, 30_000));
	afterNavigate(() => {
		open = false;
		refreshUnread();
	});

	async function load() {
		loading = true;
		failed = false;
		try {
			items = await listNotifications({ limit: 12 });
		} catch {
			failed = true;
		} finally {
			loading = false;
		}
	}
	function toggle() {
		open = !open;
		if (open) load();
	}
	async function choose(n: Notification) {
		if (!n.read_at_ms) {
			await markRead([n.id]).catch(() => {});
			n.read_at_ms = Date.now();
		}
		open = false;
		if (n.link) goto(n.link);
	}
	async function remove(n: Notification) {
		await deleteNotification(n.id).catch(() => {});
		items = items.filter((x) => x.id !== n.id);
	}
	async function readAll() {
		await markRead('all').catch(() => {});
		items = items.map((x) => ({ ...x, read_at_ms: x.read_at_ms ?? Date.now() }));
	}
	function outside(e: MouseEvent) {
		if (open && root && !root.contains(e.target as Node)) open = false;
	}
	function key(e: KeyboardEvent) {
		if (open && e.key === 'Escape') {
			open = false;
			btn?.focus();
		}
	}
	const badge = $derived(inbox.unread > 99 ? '99+' : String(inbox.unread));
</script>

<svelte:window onclick={outside} onkeydown={key} />

<div class="relative" bind:this={root}>
	<button
		bind:this={btn}
		class="tb-btn relative {open ? 'text-action' : ''}"
		onclick={toggle}
		aria-label={inbox.unread ? `Notifications, ${inbox.unread} unread` : 'Notifications'}
		aria-expanded={open}
		aria-haspopup="dialog"
		title="Notifications"
	>
		<Icon name="bell" size={17} />
		{#if inbox.unread}<span class="absolute -top-0.5 -right-0.5 grid h-4 min-w-4 place-items-center rounded-pill bg-fail px-1 text-[10px] leading-none font-semibold text-white" aria-hidden="true">{badge}</span>{/if}
	</button>
	{#if open}
		<div class="absolute right-0 z-50 mt-2 w-[min(24rem,calc(100vw-2rem))] animate-enter rounded-card border border-rule-soft bg-raised shadow-overlay" role="dialog" aria-label="Notifications">
			<div class="flex items-center gap-2 border-b border-rule-soft px-4 py-3">
				<h2 class="flex-1 font-semibold">Notifications</h2>
				{#if inbox.unread}<button class="btn btn-sm btn-quiet" onclick={readAll}>Mark all read</button>{/if}
			</div>
			<ul class="max-h-[60vh] divide-y divide-rule-soft overflow-y-auto">
				{#each items as n (n.id)}
					<li class="group flex items-start gap-2 px-4 py-3 {n.read_at_ms ? '' : 'bg-action/5'}">
						<button class="min-w-0 flex-1 text-left" onclick={() => choose(n)}>
							<span class="flex items-center gap-2 text-[11px] text-muted uppercase">
								{#if !n.read_at_ms}<span class="size-1.5 rounded-pill bg-action" aria-label="Unread"></span>{/if}
								{categoryLabels[n.category] ?? n.category} · {fmtAgo(n.created_at_ms)}
							</span>
							<span class="mt-0.5 block font-medium break-words">{n.title}</span>
							{#if n.body}<span class="mt-0.5 line-clamp-2 block text-small break-words text-muted">{n.body}</span>{/if}
						</button>
						<button class="btn btn-quiet btn-icon btn-sm opacity-60 group-hover:opacity-100" onclick={() => remove(n)} aria-label="Delete notification" title="Delete"><Icon name="x" size={14} /></button>
					</li>
				{:else}
					<li class="px-4 py-6 text-center text-small text-muted">{loading ? 'Loading…' : failed ? 'Notifications could not be loaded.' : 'Nothing new.'}</li>
				{/each}
			</ul>
			<div class="flex items-center justify-between border-t border-rule-soft px-4 py-2.5 text-small">
				<a class="link" href="/notifications">All notifications</a>
				<a class="link" href="/settings/notifications">Preferences</a>
			</div>
		</div>
	{/if}
</div>
