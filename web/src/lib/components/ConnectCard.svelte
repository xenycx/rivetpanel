<script lang="ts">
	import { onMount } from 'svelte';
	import type { Bot } from '$lib/api/types';
	import type { GameDetail } from '$lib/api/games';
	import { connectInfo, joinHint, loadGameHosts } from '$lib/connect.svelte';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// How players join this game server: the address to type into the game,
	// a copy button and where to paste it.
	let { bot, game }: { bot: Bot; game: GameDetail | null } = $props();
	onMount(() => void loadGameHosts());

	const ci = $derived(connectInfo(bot, game?.spec));
	const hint = $derived(joinHint(game ? { slug: game.blueprint.slug, category: game.blueprint.category, query: game.spec.query } : null));
	const nodesAdmin = $derived(session.user?.role === 'admin' || session.permissions.includes('nodes.manage'));

	async function copy() {
		if (!ci) return;
		try {
			await navigator.clipboard.writeText(ci.full);
			toast(`Copied ${ci.full}`, 'success');
		} catch {
			toast(ci.full, 'info');
		}
	}
</script>

<section aria-labelledby="connect-h">
	<h2 id="connect-h" class="text-title font-semibold">Connect</h2>
	{#if !ci}
		<p class="mt-2 text-small text-muted">This server has no port yet. Add one under Network.</p>
	{:else}
		<button type="button" class="mt-2 flex w-full items-center gap-2 rounded-control border border-rule bg-raised px-3 py-2 text-left hover:border-muted" onclick={copy} title="Copy {ci.full}">
			<span class="min-w-0 flex-1 truncate font-mono text-[0.9375rem] font-medium">{ci.short}</span>
			<Icon name="copy" size={14} class="shrink-0 text-muted" />
			<span class="sr-only">Copy the address</span>
		</button>
		{#if ci.short !== ci.full}<p class="mt-1 text-small text-muted">Port {ci.port} is the game's default, so the host is enough (<code>{ci.full}</code> works too).</p>{/if}
		{#if ci.queryPort}<p class="mt-1 text-small text-muted">Server browser query port: <code>{ci.queryPort}</code>.</p>{/if}
		<p class="mt-1.5 text-small">{hint}</p>
		{#if ci.reach !== 'public'}
			<p class="mt-1.5 text-small text-muted">
				{ci.reach === 'local' ? 'Only reachable from this machine.' : 'Only reachable on your local network.'}
				{#if nodesAdmin}Set the address players use under <a class="link" href="/admin/nodes">Administration → Nodes → Public address</a>.{:else}An administrator can set a public address for this node.{/if}
			</p>
		{:else if ci.source === 'panel'}
			<p class="mt-1.5 text-small text-muted">This is the panel's own host name.{#if nodesAdmin} If players use another one, set it under <a class="link" href="/admin/nodes">Administration → Nodes → Public address</a>.{/if}</p>
		{/if}
	{/if}
</section>
