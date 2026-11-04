<script lang="ts">
	import type { Snippet } from 'svelte';
	// Every empty state names the next useful action. `level` keeps the heading
	// one step below the section it sits in (2 directly under a page title).
	let { title, children, actions, compact = false, level = 2 }: { title: string; children?: Snippet; actions?: Snippet; compact?: boolean; level?: 2 | 3 | 4 } = $props();
</script>

<div class="rounded-card border border-dashed border-rule {compact ? 'px-4 py-5' : 'px-6 py-8'}">
	<svelte:element this={`h${level}`} class="text-title">{title}</svelte:element>
	{#if children}<div class="mt-1 max-w-prose text-muted">{@render children()}</div>{/if}
	{#if actions}<div class="mt-4 flex flex-wrap gap-2">{@render actions()}</div>{/if}
</div>
