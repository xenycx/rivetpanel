<script lang="ts">
	import type { Snippet } from 'svelte';

	// Frame for the signed-out account pages (sign in, registration, password
	// reset, email confirmation): the product name and one quiet panel.
	let { children }: { children: Snippet } = $props();
</script>

<main id="main" class="grid min-h-dvh place-items-center bg-paper px-4 py-10">
	<div class="w-full max-w-[25rem]">
		<a href="/" class="flex w-fit items-center gap-2 px-1 font-semibold"><img src="/favicon.svg" alt="" width="26" height="26" class="rounded-control" />RivetPanel</a>
		<div class="mt-4 rounded-card border border-rule-soft bg-panel p-6 sm:p-7">
			{@render children()}
		</div>
	</div>
</main>
