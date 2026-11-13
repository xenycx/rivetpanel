<script lang="ts">
	import type { Snippet } from 'svelte';
	let { caption, wide = false, children }: { caption?: string; wide?: boolean; children: Snippet } = $props();
</script>

<figure class="doc-fig" class:wide>
	<div class="doc-fig-body">{@render children()}</div>
	{#if caption}<figcaption>{caption}</figcaption>{/if}
</figure>
