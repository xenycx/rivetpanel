<script lang="ts">
	import { onLive } from '$lib/live.svelte';
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Stack } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { workspaceName, workspaces } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import AppIcon from './AppIcon.svelte';

	// Stacks imported from Compose files, one row each with how many of its
	// services run. Nothing is shown while there are none.
	let stacks = $state<Stack[] | null>(null);
	let error = $state('');

	async function load() {
		try {
			stacks = (await api<{ stacks: Stack[] }>('GET', '/stacks')).stacks;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Stacks could not be loaded.';
		}
	}
	// Live: re-fetch as soon as what this view shows changes anywhere.
	onMount(() => onLive(['stacks', 'bots'], () => void load()));
	onMount(() => {
		load();
		const t = setInterval(() => document.visibilityState === 'visible' && load(), 5000);
		return () => clearInterval(t);
	});
	const scoped = $derived((stacks ?? []).filter((s) => workspaces.selected === 'all' || s.workspace_id === workspaces.selected));
	const multi = $derived(workspaces.list.length > 1 && workspaces.selected === 'all');
	const memory = (s: Stack) => s.services.reduce((n, b) => n + b.memory_bytes, 0);
	const running = (s: Stack) => s.services.filter((b) => b.phase === 'running').length;
	const tone = (s: Stack) => {
		const r = running(s);
		if (s.services.some((b) => b.phase === 'failed' || b.phase === 'retrying')) return 'fail';
		if (r === s.services.length && r > 0) return 'run';
		if (r > 0) return 'warn';
		return undefined;
	};
</script>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if scoped.length}
	<section class="mt-6" aria-labelledby="stacks-h">
		<h2 id="stacks-h" class="mb-2 text-section font-semibold">Stacks</h2>
		<div class="rows" role="table" aria-label="Stacks">
			<div class="rows-head hidden grid-cols-[minmax(0,1.6fr)_8rem_minmax(0,1.6fr)_6rem_7rem] items-center gap-x-4 px-4 py-2 md:grid" role="row">
				<span role="columnheader">Name</span><span role="columnheader">Running</span><span role="columnheader">Services</span><span role="columnheader">Memory</span><span role="columnheader">Created</span>
			</div>
			{#each scoped as s (s.id)}
				<div class="row-link grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 px-4 py-2.5 md:grid-cols-[minmax(0,1.6fr)_8rem_minmax(0,1.6fr)_6rem_7rem]" role="row">
					<div class="flex min-w-0 items-center gap-3" role="cell">
						{#if s.template}
							<AppIcon id={s.template.id} name={s.template.name} size={32} />
						{:else}
							<span class="grid size-8 shrink-0 place-items-center rounded-control bg-paper-2 text-muted" aria-hidden="true"><Icon name="layers" size={15} /></span>
						{/if}
						<div class="min-w-0">
							<a href="/stacks/{s.id}" class="block truncate font-medium hover:underline">{s.name}</a>
							<p class="truncate text-small text-muted">{s.template ? s.template.name : 'Compose stack'}{multi && workspaceName(s.workspace_id) ? `, ${workspaceName(s.workspace_id)}` : ''}</p>
						</div>
					</div>
					<span role="cell"><span class="pill" data-tone={tone(s)}>{running(s)} of {s.services.length}</span></span>
					<span class="hidden truncate font-mono text-small text-muted md:block" role="cell">{s.services.map((b) => b.container?.service ?? b.name).join(', ') || '–'}</span>
					<span class="hidden font-mono text-small md:block" role="cell">{fmtBytes(memory(s))}</span>
					<span class="hidden text-small text-muted md:block" role="cell">{fmtAgo(s.created_at_ms)}</span>
				</div>
			{/each}
		</div>
	</section>
{/if}
