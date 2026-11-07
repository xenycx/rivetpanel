<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { AppCatalog, AppTemplate } from '$lib/api/types';
	import { can } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import AppIcon from './AppIcon.svelte';
	import InstallAppDialog from './InstallAppDialog.svelte';

	// The one-click app catalog: search, a category filter and a grid of
	// cards. Choosing a card opens the short install form.
	// limit: how many cards show before "Show all" (0 = all). query: a search
	// owned by the page (its own search box is then hidden).
	let { limit = 0, heading = true, query }: { limit?: number; heading?: boolean; query?: string } = $props();

	const labels: Record<string, string> = {
		publishing: 'Publishing',
		productivity: 'Productivity',
		dev: 'Developer tools',
		monitoring: 'Monitoring',
		reading: 'Reading',
		media: 'Media and files',
		security: 'Security',
		ai: 'AI and agents'
	};

	let catalog = $state<AppCatalog | null>(null);
	let error = $state('');
	let q = $state('');
	let category = $state('all');
	let expanded = $state(false);
	let chosen = $state<AppTemplate | null>(null);
	let dialog = $state(false);

	onMount(async () => {
		try {
			catalog = await api<AppCatalog>('GET', '/app-templates');
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The app catalog could not be loaded.';
		}
	});

	const present = $derived((catalog?.categories ?? []).filter((c) => catalog?.templates.some((t) => t.category === c)));
	const matches = $derived.by(() => {
		const words = needle.trim().toLowerCase().split(/\s+/).filter(Boolean);
		return (catalog?.templates ?? []).filter(
			(t) =>
				(category === 'all' || t.category === category) &&
				words.every((w) => `${t.name} ${t.tagline} ${t.description} ${labels[t.category] ?? ''}`.toLowerCase().includes(w))
		);
	});
	const needle = $derived(query ?? q);
	const filtering = $derived(needle.trim() !== '' || category !== 'all');
	const shown = $derived(limit && !expanded && !filtering ? matches.slice(0, limit) : matches);
	const canInstall = $derived(can('bots.create'));

	function choose(t: AppTemplate) {
		chosen = t;
		dialog = true;
	}
</script>

<section id="catalog" class="{heading ? 'mt-8' : 'mt-4'} scroll-mt-20" aria-labelledby="catalog-h">
	{#if heading}
		<div class="flex flex-wrap items-end gap-x-6 gap-y-3">
			<div class="min-w-0">
				<h2 id="catalog-h" class="text-section font-semibold">One-click apps</h2>
				<p class="mt-0.5 max-w-measure text-small text-muted">Self-hosted apps set up with their database and sign-in, each in its own isolated containers.</p>
			</div>
			<label class="relative ml-auto w-full sm:w-64">
				<span class="sr-only">Search apps</span>
				<Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
				<input class="field pl-8" type="search" placeholder="Search apps" bind:value={q} />
			</label>
		</div>
	{:else}
		<h2 id="catalog-h" class="sr-only">One-click apps</h2>
	{/if}

	{#if error}
		<Notice tone="fail" class="mt-4" live>{error}</Notice>
	{:else if !catalog}
		<div class="mt-4"><Skeleton rows={3} label="Loading the app catalog" /></div>
	{:else}
		{#if present.length > 1}
			<div class="mt-3 flex flex-wrap gap-1" role="group" aria-label="Categories">
				<button type="button" class="seg-item" aria-pressed={category === 'all'} onclick={() => (category = 'all')}>All</button>
				{#each present as c (c)}
					<button type="button" class="seg-item" aria-pressed={category === c} onclick={() => (category = c)}>{labels[c] ?? c}</button>
				{/each}
			</div>
		{/if}

		{#if matches.length === 0}
			<p class="rows-empty mt-3 rounded-tile border border-rule-soft bg-panel">No apps match{needle.trim() ? ` “${needle.trim()}”` : ' this category'}. {#if query === undefined || category !== 'all'}<button class="link" onclick={() => { q = ''; category = 'all'; }}>Show all apps</button>{/if}</p>
		{:else}
			<ul class="mt-3 grid grid-cols-[repeat(auto-fill,minmax(15rem,1fr))] gap-2.5">
				{#each shown as t (t.id)}
					<li class="min-w-0">
						<button
							type="button"
							class="app-card group flex h-full w-full items-start gap-3 rounded-tile border border-rule-soft bg-panel p-3.5 text-left disabled:cursor-not-allowed disabled:opacity-55"
							disabled={!t.available || !canInstall}
							title={!t.available ? t.reason : !canInstall ? 'You cannot create apps in this workspace' : undefined}
							onclick={() => choose(t)}
						>
							<AppIcon id={t.id} name={t.name} size={40} />
							<span class="min-w-0 flex-1">
								<span class="flex items-center gap-2">
									<span class="truncate font-medium">{t.name}</span>
									{#if !t.web && t.console}<span class="pill shrink-0">Terminal</span>{/if}
								</span>
								<span class="mt-0.5 line-clamp-2 block text-small text-muted">{t.available ? t.tagline : t.reason}</span>
								<span class="mt-1.5 block truncate font-mono text-label text-muted">{t.services} {t.services === 1 ? 'container' : 'containers'} · <span class="whitespace-nowrap">{fmtBytes(t.memory_bytes)}</span></span>
							</span>
						</button>
					</li>
				{/each}
			</ul>
			{#if shown.length < matches.length}
				<button class="btn btn-sm btn-quiet mt-2 -ml-2" onclick={() => (expanded = true)}><Icon name="chevronDown" size={13} />Show all {matches.length} apps</button>
			{/if}
		{/if}
	{/if}
</section>

<InstallAppDialog bind:open={dialog} app={chosen} sites={catalog?.sites ?? false} publicPorts={catalog?.public_ports ?? false} />

<style>
	.app-card {
		transition:
			border-color 120ms,
			background-color 120ms,
			transform 120ms;
	}
	.app-card:not(:disabled):hover {
		border-color: color-mix(in srgb, var(--color-action) 45%, var(--color-rule));
	}
	.app-card:not(:disabled):active {
		transform: translateY(1px);
	}
	.app-card:focus-visible {
		outline: 2px solid var(--color-action);
		outline-offset: 2px;
	}
</style>
