<script lang="ts">
	import { page } from '$app/state';
	import Icon from '$lib/components/ui/Icon.svelte';
	import TicketThread from '$lib/components/TicketThread.svelte';
	import { type TicketView, ticketKey } from '$lib/api/support';

	// The ticket's own page; the address takes its id or its key (RT-000042).
	const id = $derived(page.params.id ?? '');
	let view = $state<TicketView | null>(null);
	const t = $derived(view?.ticket);
	const toQueue = $derived(!!view?.staff && !view.requester);
</script>

<svelte:head><title>{t ? `${ticketKey(t)} ${t.subject}` : 'Ticket'} · RivetPanel</title></svelte:head>

<nav class="flex items-center gap-1.5 text-small" aria-label="Breadcrumb">
	<a class="link inline-flex items-center gap-1" href={toQueue ? `/support?queue=all&t=${t?.id ?? ''}` : '/support'}><Icon name="chevronLeft" size={14} />{toQueue ? 'Support queue' : 'Support'}</a>
	{#if t}<span class="text-muted" aria-hidden="true">/</span><span class="ticket-key">{ticketKey(t)}</span>{/if}
</nav>

{#key id}
	<TicketThread {id} onview={(v) => (view = v)} />
{/key}
