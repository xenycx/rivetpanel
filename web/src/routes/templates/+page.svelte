<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Template } from '$lib/api/types';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let templates = $state<Template[] | null>(null);
	let error = $state('');
	let lang = $state('');
	onMount(async () => {
		try {
			templates = (await api<{ templates: Template[] }>('GET', '/templates')).templates;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The templates could not be loaded.';
		}
	});
	const langs = $derived([...new Set((templates ?? []).map((t) => t.language))].sort());
	const shown = $derived((templates ?? []).filter((t) => !lang || t.language === lang));
	const accent: Record<string, string> = { JavaScript: '#f0c14b', TypeScript: '#4a8fe7', Python: '#4b8bbe', Rust: '#e2753a', Java: '#e76f51', Go: '#29beb0', Ruby: '#d6453d' };
</script>

<svelte:head><title>Templates · RivetPanel</title></svelte:head>

<section class="card card-glow p-6 sm:p-8">
	<p class="eyebrow">Templates</p>
	<h1 class="mt-2 text-[2rem] leading-tight font-semibold tracking-tight">Start from a working <span class="text-action">bot</span>.</h1>
	<p class="mt-2 max-w-2xl text-muted">Every starter builds inside the hardened runner, reads its token from the environment and registers a <code>/ping</code> command. Pick one, paste your token, press Start.</p>
	{#if langs.length}
		<div class="mt-5 flex flex-wrap gap-1.5" role="group" aria-label="Language">
			<button class="btn btn-sm {lang === '' ? 'btn-primary' : ''}" onclick={() => (lang = '')}>All</button>
			{#each langs as l (l)}<button class="btn btn-sm {lang === l ? 'btn-primary' : ''}" onclick={() => (lang = l)}>{l}</button>{/each}
		</div>
	{/if}
</section>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if templates === null && !error}
	<div class="mt-4"><Skeleton rows={3} label="Loading templates" /></div>
{:else}
	<ul class="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
		{#each shown as t (t.id)}
			<li class="card flex flex-col p-5">
				<div class="flex items-center justify-between gap-2">
					<span class="grid size-10 place-items-center rounded-tile font-mono text-small font-semibold" style="background: color-mix(in srgb, {accent[t.language] ?? '#ea621f'} 18%, transparent); color: {accent[t.language] ?? '#ea621f'}">{t.language.slice(0, 2).toUpperCase()}</span>
					<span class="pill">{t.language}</span>
				</div>
				<h2 class="mt-4 text-title font-semibold">{t.name}</h2>
				<p class="mt-1 text-small text-muted">{t.description}</p>
				<dl class="mt-4 grid gap-1 border-t border-rule-soft pt-3 text-small">
					<div class="flex justify-between gap-3"><dt class="text-muted">Tested with</dt><dd class="text-right">{t.tested_with}</dd></div>
					<div class="flex justify-between gap-3"><dt class="text-muted">Memory</dt><dd class="font-mono">{fmtBytes(t.default_memory_bytes)}</dd></div>
					{#if t.build_memory_bytes}<div class="flex justify-between gap-3"><dt class="text-muted">Build memory</dt><dd class="font-mono">{fmtBytes(t.build_memory_bytes)}</dd></div>{/if}
				</dl>
				<p class="mt-3 text-small text-muted">{t.first_start}</p>
				<a href="/bots/new?source=template&template={t.id}" class="btn btn-primary mt-auto pt-0" style="margin-top: 1rem">Use this template<Icon name="chevronRight" size={14} /></a>
			</li>
		{/each}
	</ul>
{/if}
