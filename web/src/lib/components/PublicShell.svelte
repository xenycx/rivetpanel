<script lang="ts">
	import type { Snippet } from 'svelte';
	import { session } from '$lib/session.svelte';
	// Page frame for pages anonymous visitors can open (help center, status
	// page). Signed-in accounts already get the panel's own frame, unless
	// `always` asks for this one.
	let { children, always = false, title = 'RivetPanel' }: { children: Snippet; always?: boolean; title?: string } = $props();
	const framed = $derived(always || !session.user);
</script>

{#if framed}
	<div class="min-h-dvh bg-paper">
		<header class="border-b border-rule-soft">
			<div class="mx-auto flex h-14 max-w-5xl items-center gap-3 px-4 sm:px-6">
				<a href={session.user ? '/dashboard' : '/'} class="flex items-center gap-2"><img src="/favicon.svg" alt="" width="26" height="26" class="rounded-tile" /><span class="font-semibold tracking-[0.06em] uppercase">{title}</span></a>
				<nav class="ml-auto flex items-center gap-1 text-small">
					<a class="btn btn-quiet" href="/help">Help</a>
					{#if session.user}<a class="btn btn-quiet" href="/dashboard">Open panel</a>{:else}<a class="btn btn-quiet" href="/login">Sign in</a>{/if}
				</nav>
			</div>
		</header>
		<main id="main" class="mx-auto max-w-5xl px-4 py-8 sm:px-6 sm:py-10">{@render children()}</main>
	</div>
{:else}
	{@render children()}
{/if}
