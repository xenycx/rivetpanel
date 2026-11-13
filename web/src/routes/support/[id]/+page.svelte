<script lang="ts">
	import { page } from '$app/state';
	import Icon from '$lib/components/ui/Icon.svelte';
	import TicketThread from '$lib/components/TicketThread.svelte';
	import type { TicketView } from '$lib/api/support';

	const id = $derived(page.params.id ?? '');
	let view = $state<TicketView | null>(null);
	const t = $derived(view?.ticket);
</script>

<svelte:head><title>{t ? `#${t.number} ${t.subject}` : 'Ticket'} · RivetPanel</title></svelte:head>

<a class="link inline-flex items-center gap-1 text-small" href={view?.staff && !view.requester ? `/support?queue=all&t=${id}` : '/support'}><Icon name="chevronLeft" size={14} />{view?.staff && !view.requester ? 'Support queue' : 'Support'}</a>

<div class="max-w-[60rem]">
	{#key id}
		<TicketThread {id} onview={(v) => (view = v)} />
	{/key}
</div>
