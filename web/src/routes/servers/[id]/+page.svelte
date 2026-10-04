<script lang="ts">
	import { page } from '$app/state';
	import BotView from '$lib/components/BotView.svelte';
</script>

<!-- Re-created when navigating between servers so no state leaks across them. -->
{#key page.params.id}
	<BotView id={page.params.id!} />
{/key}
