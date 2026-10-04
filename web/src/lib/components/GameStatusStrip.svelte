<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import type { GameStatus } from '$lib/api/games';
	import { connectInfo, loadGameHosts } from '$lib/connect.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { describe } from '$lib/status';

	let { bot }: { bot: Bot } = $props();
	let st = $state<GameStatus | null>(null);
	const running = $derived(bot.observed_state === 'running');
	// The same state words as the status badge in the page header.
	const shown = $derived(describe(bot));

	async function load() {
		if (!running || document.visibilityState !== 'visible') return;
		try {
			st = await api<GameStatus>('GET', `/bots/${bot.id}/game/query`);
		} catch {
			/* best effort */
		}
	}
	onMount(() => {
		loadGameHosts();
		load();
		const t = setInterval(load, 10_000);
		return () => clearInterval(t);
	});
	$effect(() => {
		if (running) load();
		else st = null;
	});
	const ci = $derived(connectInfo(bot));
	const addr = $derived(ci?.full ?? '');
	async function copy() {
		try {
			await navigator.clipboard.writeText(addr);
			toast('Address copied', 'success');
		} catch {
			toast(addr, 'info');
		}
	}
</script>

<!-- Inline items for the server page's stat strip: address, players and
     what the server reports about itself. -->
{#if addr}
	<div>
		<span class="stat-k">Address</span>
		<button class="stat-v flex max-w-full items-center gap-1 hover:underline" onclick={copy} title="Copy {addr}"><span class="truncate">{addr}</span><Icon name="copy" size={11} class="shrink-0 text-muted" /></button>
	</div>
{/if}
<div>
	<span class="stat-k">Players</span>
	<!-- The process state comes from the same source as the status badge;
	     only the player count comes from the game's own status query. -->
	<p class="stat-v" title={running && !st?.online ? 'The game has not answered a status query yet; it may still be loading the world.' : undefined}>{st?.online ? `${st.players} / ${st.max_players}` : !running ? 'Offline' : bot.phase === 'running' ? 'No answer yet' : shown.label}</p>
</div>
{#if st?.version}<div><span class="stat-k">Version</span><p class="stat-v" title={st.version}>{st.version}</p></div>{/if}
{#if st?.online}<div><span class="stat-k">Ping</span><p class="stat-v">{st.latency_ms} ms</p></div>{/if}
{#if st?.map}<div><span class="stat-k">Map</span><p class="stat-v" title={st.map}>{st.map}</p></div>{/if}
{#if st?.sample?.length}
	<p class="col-span-full truncate text-muted" title={st.motd || undefined}>Online: {st.sample.join(', ')}{st.players > st.sample.length ? ` and ${st.players - st.sample.length} more` : ''}</p>
{/if}
