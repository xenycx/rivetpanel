<script lang="ts">
	import { logoSeed } from '$lib/avatar';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import { currentWorkspace, selectWorkspace, workspaces, wsName } from '$lib/workspaces.svelte';

	// Says which workspace a page shows, next to its title. A page that follows
	// the switcher (`follows`) names the chosen workspace with a way back to
	// all of them; a page that does not says it shows everything, but only
	// while a workspace is chosen, when the difference matters.
	let { follows = true, class: cls = '' }: { follows?: boolean; class?: string } = $props();
	const ws = $derived(currentWorkspace());
</script>

{#if ws && follows}
	<p class="flex min-w-0 items-center gap-1.5 text-small text-muted {cls}">
		<Logo src={ws.logo_url} kind="workspace" seed={logoSeed(ws.id, ws.logo_variant)} class="size-4 rounded-xs" />
		<span class="truncate font-medium text-ink" title={wsName(ws)}>{wsName(ws)}</span>
		<span aria-hidden="true">·</span>
		<button type="button" class="shrink-0 whitespace-nowrap hover:text-ink hover:underline" onclick={() => selectWorkspace('all')}>Show all workspaces</button>
	</p>
{:else if ws && workspaces.list.length > 1}
	<p class="flex items-center gap-1.5 text-small text-muted {cls}" title="This page is not limited to {wsName(ws)}, the workspace chosen in the switcher.">
		<Icon name="layers" size={13} />Showing all workspaces
	</p>
{/if}
