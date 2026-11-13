<script lang="ts">
	import { goto } from '$app/navigation';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { loadIndex, search, href, type Hit } from './index';
	import type { SearchEntry } from './types';

	let { compact = false }: { compact?: boolean } = $props();

	const uid = $props.id();
	let input = $state<HTMLInputElement>();
	let query = $state('');
	let entries = $state<SearchEntry[] | null>(null);
	let open = $state(false);
	let active = $state(0);
	let failed = $state(false);

	const hits = $derived<Hit[]>(entries && query.trim() ? search(entries, query, compact ? 8 : 12) : []);
	$effect(() => {
		void hits;
		active = 0;
	});

	async function prime() {
		if (entries) return;
		try {
			entries = await loadIndex();
		} catch {
			failed = true;
		}
	}

	function go(h: Hit) {
		open = false;
		query = '';
		input?.blur();
		goto(href(h.slug, h.id));
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			if (!hits.length) return;
			e.preventDefault();
			open = true;
			active = (active + (e.key === 'ArrowDown' ? 1 : hits.length - 1)) % hits.length;
			document.getElementById(`${uid}-o${active}`)?.scrollIntoView({ block: 'nearest' });
		} else if (e.key === 'Enter' && hits[active]) {
			e.preventDefault();
			go(hits[active]);
		} else if (e.key === 'Escape') {
			if (query) query = '';
			else input?.blur();
			open = false;
		}
	}

	// "/" focuses the search box that is on screen (the rail's on wide
	// screens, the bar's on narrow ones).
	function onwindowkey(e: KeyboardEvent) {
		if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || !input || input.offsetParent === null) return;
		const t = e.target as HTMLElement;
		if (t.closest('input, textarea, select, [contenteditable="true"]')) return;
		e.preventDefault();
		input.focus();
	}

	// Highlights the query words in a snippet without HTML injection.
	function marks(text: string): { s: string; m: boolean }[] {
		const words = query.trim().toLowerCase().split(/\s+/).filter((w) => w.length > 1);
		if (!words.length) return [{ s: text, m: false }];
		const re = new RegExp(`(${words.map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi');
		return text.split(re).map((s, i) => ({ s, m: i % 2 === 1 }));
	}
</script>

<svelte:window onkeydown={onwindowkey} />

<div class="docs-search" class:compact>
	<label for="{uid}-q" class="sr-only">Search the documentation</label>
	<div class="relative">
		<Icon name="search" size={compact ? 14 : 18} class="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />
		<input
			bind:this={input}
			id="{uid}-q"
			class="field docs-search-input"
			type="search"
			role="combobox"
			autocomplete="off"
			spellcheck="false"
			aria-expanded={open && !!query.trim()}
			aria-controls="{uid}-list"
			aria-activedescendant={open && hits.length ? `${uid}-o${active}` : undefined}
			placeholder={compact ? 'Search docs' : 'Search the documentation: ports, backups, RIVET_SITES_LISTEN…'}
			bind:value={query}
			onfocus={() => {
				prime();
				open = true;
			}}
			oninput={() => (open = true)}
			onblur={() => setTimeout(() => (open = false), 120)}
			{onkeydown}
		/>
		{#if compact}<kbd class="docs-search-kbd" aria-hidden="true">/</kbd>{/if}
	</div>
	{#if open && query.trim()}
		<div class="docs-results" id="{uid}-list" role="listbox" aria-label="Search results">
			{#if failed}
				<p class="px-3 py-3 text-small text-muted">Search could not load. Reload the page and try again.</p>
			{:else if !entries}
				<p class="px-3 py-3 text-small text-muted">Loading the index…</p>
			{:else if !hits.length}
				<p class="px-3 py-3 text-small text-muted">Nothing matches "{query.trim()}". Try a single word or a setting name.</p>
			{:else}
				{#each hits as h, i (h.slug + '#' + h.id)}
					<div
						id="{uid}-o{i}"
						role="option"
						tabindex="-1"
						aria-selected={i === active}
						class="docs-hit"
						onpointerenter={() => (active = i)}
						onpointerdown={(e) => {
							e.preventDefault();
							go(h);
						}}
					>
						<span class="docs-hit-where">{h.category}<Icon name="chevronRight" size={10} />{h.title}</span>
						<span class="docs-hit-title">{#each marks(h.heading || h.title) as part, j (j)}{#if part.m}<mark>{part.s}</mark>{:else}{part.s}{/if}{/each}</span>
						{#if h.snippet}<span class="docs-hit-snippet">{#each marks(h.snippet) as part, j (j)}{#if part.m}<mark>{part.s}</mark>{:else}{part.s}{/if}{/each}</span>{/if}
					</div>
				{/each}
			{/if}
		</div>
	{/if}
</div>
