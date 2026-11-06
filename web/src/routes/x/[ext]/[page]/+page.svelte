<script lang="ts">
	import { page } from '$app/state';
	import { session } from '$lib/session.svelte';
	import { extensions, loadExtensions } from '$lib/extensions/store.svelte';
	import ExtensionFrame from '$lib/components/ExtensionFrame.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// A full page an extension adds to the sidebar.
	const ext = $derived(page.params.ext ?? '');
	const pageId = $derived(page.params.page ?? '');
	$effect(() => {
		if (session.user && !extensions.loaded) loadExtensions();
	});
	const contribution = $derived(extensions.list.find((x) => x.id === ext));
	const item = $derived(contribution?.nav.find((n) => n.page === pageId));
</script>

<svelte:head><title>{item?.label ?? 'Extension'} · RivetPanel</title></svelte:head>

{#if !extensions.loaded}
	<Skeleton rows={4} label="Loading" />
{:else if !contribution || !item}
	<EmptyState title="This page is not available">
		The extension that provides it is turned off or not installed, or your account cannot open it.
		{#snippet actions()}<a class="btn" href="/dashboard">Back to the overview</a>{/snippet}
	</EmptyState>
{:else}
	<header class="mb-4 border-b border-rule-soft pb-4">
		<h1 class="text-page">{item.label}</h1>
		<p class="mt-0.5 flex items-center gap-1.5 text-small text-muted"><Icon name="layers" size={13} />Extension: {contribution.name} by {contribution.publisher}{#if contribution.source === 'dev'}<span class="pill" data-tone="warn">Unsigned</span>{/if}</p>
	</header>
	<ExtensionFrame {ext} page={pageId} title="{item.label} ({contribution.name})" height="max(30rem, calc(100dvh - 13rem))" />
{/if}
