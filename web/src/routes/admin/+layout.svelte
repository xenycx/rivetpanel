<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { canAdminister, session } from '$lib/session.svelte';
	import { adminLinks } from '$lib/adminNav';
	import SubNav from '$lib/components/ui/SubNav.svelte';
	let { children } = $props();
	// Sections follow the account's role permissions; the server refuses the
	// rest whatever the interface shows.
	const links = $derived(session.user ? adminLinks() : []);
	$effect(() => {
		if (!session.user) return;
		if (!canAdminister()) goto('/dashboard');
		else if (links.length && !links.some((l) => page.url.pathname === l.href || (l.match && page.url.pathname.startsWith(l.match)))) goto(links[0].href);
	});
</script>

{#if session.user && canAdminister()}
	<header class="border-b border-rule-soft pb-5">
		<p class="eyebrow">Administration</p>
		<h1 class="mt-1 text-page">This installation<span class="text-action">.</span></h1>
		<p class="mt-0.5 text-muted">
			{#if session.user.role === 'admin'}Accounts, roles, workspaces, hosted sites, game servers, the host and its health.{:else}The parts of the administration your role ({session.user.role_name || 'custom'}) allows.{/if}
		</p>
	</header>
	<div class="mt-6 flex flex-col gap-6 md:flex-row md:gap-10">
		<SubNav {links} label="Administration sections" />
		<div class="min-w-0 flex-1">{@render children()}</div>
	</div>
{/if}
