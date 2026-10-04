<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { joinAddress, type GameStatus } from '$lib/api/games';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	let { bot }: { bot: Bot } = $props();
	let st = $state<GameStatus | null>(null);
	const running = $derived(bot.observed_state === 'running');

	async function load() {
		if (!running || document.visibilityState !== 'visible') return;
		try {
			st = await api<GameStatus>('GET', `/bots/${bot.id}/game/query`);
		} catch {
			/* best effort */
		}
	}
	onMount(() => {
		load();
		const t = setInterval(load, 10_000);
		return () => clearInterval(t);
	});
	$effect(() => {
		if (running) load();
		else st = null;
	});
	const addr = $derived(joinAddress(bot));
	async function copy() {
		try {
			await navigator.clipboard.writeText(addr);
			toast('Address copied', 'success');
		} catch {
			toast(addr, 'info');
		}
	}
</script>

<section class="card flex flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3" aria-label="Server status">
	{#if addr}
		<div class="min-w-0">
			<p class="eyebrow">Address</p>
			<button class="mt-0.5 flex items-center gap-1.5 font-mono text-small font-medium text-action hover:underline" onclick={copy} title="Copy the address players connect to">{addr}<Icon name="copy" size={12} /></button>
		</div>
	{/if}
	<div>
		<p class="eyebrow">Players</p>
		<p class="mt-0.5 font-mono text-small font-medium">{st?.online ? `${st.players} / ${st.max_players}` : running ? 'Starting…' : 'Offline'}</p>
	</div>
	{#if st?.version}<div><p class="eyebrow">Version</p><p class="mt-0.5 text-small font-medium">{st.version}</p></div>{/if}
	{#if st?.map}<div><p class="eyebrow">Map</p><p class="mt-0.5 text-small font-medium">{st.map}</p></div>{/if}
	{#if st?.online}<div><p class="eyebrow">Ping</p><p class="mt-0.5 font-mono text-small font-medium">{st.latency_ms} ms</p></div>{/if}
	{#if st?.motd}<p class="min-w-0 flex-1 basis-48 truncate text-small text-muted" title={st.motd}>{st.motd}</p>{/if}
	{#if st?.sample?.length}
		<p class="w-full truncate text-small text-muted">Online: {st.sample.join(', ')}{st.players > st.sample.length ? ` and ${st.players - st.sample.length} more` : ''}</p>
	{/if}
</section>
