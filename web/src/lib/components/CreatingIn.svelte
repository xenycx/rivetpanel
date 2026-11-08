<script lang="ts">
	// Names the workspace a create form puts the new app or site in, so its
	// allowance and room below read in context.
	import Logo from '$lib/components/ui/Logo.svelte';
	import { LIMITS_HELP } from '$lib/limits';

	let { workspace, class: cls = '' }: { workspace?: { id: string; name: string; personal: boolean } | null; class?: string } = $props();
</script>

{#if workspace}
	<p class="flex flex-wrap items-center gap-x-2 gap-y-1 text-small text-muted {cls}">
		<span>Creating in</span>
		<span class="inline-flex items-center gap-1.5 font-medium text-ink"><Logo kind="workspace" seed={workspace.id} class="size-5 rounded-tile" />{workspace.name || 'Personal'}</span>
		<span aria-hidden="true">·</span>
		<a class="link" href={LIMITS_HELP}>How limits work</a>
	</p>
{/if}
