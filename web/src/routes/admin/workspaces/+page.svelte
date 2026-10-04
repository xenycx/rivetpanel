<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Workspace } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let list = $state<Workspace[] | null>(null);
	let error = $state('');
	let q = $state('');
	let kind = $state<'all' | 'team' | 'personal'>('all');
	let sort = $state<'activity' | 'bots' | 'memory' | 'name'>('activity');

	onMount(async () => {
		try {
			list = (await api<{ workspaces: Workspace[] }>('GET', '/admin/workspaces')).workspaces;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Workspaces could not be loaded.';
		}
	});

	const shown = $derived.by(() => {
		const needle = q.trim().toLowerCase();
		const out = (list ?? []).filter(
			(w) => (kind === 'all' || (kind === 'team') !== w.personal) && (!needle || `${w.name} ${w.owner_email}`.toLowerCase().includes(needle))
		);
		const by: Record<typeof sort, (a: Workspace, b: Workspace) => number> = {
			activity: (a, b) => b.last_active_at_ms - a.last_active_at_ms,
			bots: (a, b) => b.bots - a.bots,
			memory: (a, b) => b.memory_bytes - a.memory_bytes,
			name: (a, b) => a.name.localeCompare(b.name)
		};
		return out.sort((a, b) => by[sort](a, b) || a.name.localeCompare(b.name));
	});
	const totals = $derived.by(() => {
		const t = { teams: 0, bots: 0, running: 0, sites: 0, memory: 0 };
		for (const w of list ?? []) {
			if (!w.personal) t.teams++;
			t.bots += w.bots;
			t.running += w.running_bots;
			t.sites += w.sites;
			t.memory += w.memory_bytes;
		}
		return t;
	});
</script>

<svelte:head><title>Workspaces · Administration · RivetPanel</title></svelte:head>

<h2 class="text-section">Workspaces</h2>
<p class="mt-1 max-w-prose text-muted">Every account's personal workspace and every team, with what runs in it. Open one to see its members, bots, sites and recent deployments.</p>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if list === null && !error}
	<div class="mt-4"><Skeleton rows={4} /></div>
{:else if list}
	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		{#each [['Workspaces', `${list.length}`, `${totals.teams} team${totals.teams === 1 ? '' : 's'}`], ['Bots', `${totals.running}/${totals.bots}`, 'running / total'], ['Memory', fmtBytes(totals.memory), 'assigned to running bots'], ['Sites', `${totals.sites}`, 'hosted']] as [k, v, h] (k)}
			<div class="card px-4 py-3"><dt class="eyebrow">{k}</dt><dd class="mt-1 font-mono text-title font-semibold">{v}</dd><dd class="text-small text-muted">{h}</dd></div>
		{/each}
	</dl>

	<div class="mt-5 flex flex-wrap items-center gap-2">
		<label class="relative min-w-0 flex-1 basis-60">
			<span class="sr-only">Search workspaces</span>
			<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
			<input class="field pl-8" type="search" placeholder="Search by name or owner" bind:value={q} />
		</label>
		<select class="field w-auto" bind:value={kind} aria-label="Kind"><option value="all">All kinds</option><option value="team">Teams</option><option value="personal">Personal</option></select>
		<select class="field w-auto" bind:value={sort} aria-label="Sort"><option value="activity">Recently active</option><option value="bots">Most bots</option><option value="memory">Most memory</option><option value="name">Name</option></select>
	</div>

	<div class="card mt-3 overflow-x-auto">
		<table class="w-full min-w-[44rem] text-left">
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal">
					<th class="eyebrow">Workspace</th><th class="eyebrow">Owner</th><th class="eyebrow text-right">Members</th><th class="eyebrow text-right">Bots</th><th class="eyebrow text-right">Sites</th><th class="eyebrow text-right">Memory</th><th class="eyebrow text-right">Active</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each shown as w (w.id)}
					<tr class="[&>td]:px-4 [&>td]:py-2.5 hover:bg-paper/40">
						<td class="max-w-64">
							<a class="flex items-center gap-2 font-medium hover:underline" href="/admin/workspaces/{w.id}">
								<span class="grid size-7 shrink-0 place-items-center rounded-control bg-paper-2 font-mono text-[11px] text-action" aria-hidden="true">{(w.personal ? 'P' : w.name.slice(0, 2)).toUpperCase()}</span>
								<span class="truncate">{w.personal ? 'Personal' : w.name}</span>
							</a>
						</td>
						<td class="max-w-56 truncate text-small"><a class="hover:underline" href="/admin/users/{w.owner_id}">{w.owner_email}</a></td>
						<td class="text-right font-mono text-small">{w.members}</td>
						<td class="text-right font-mono text-small"><span class={w.running_bots ? 'text-run' : ''}>{w.running_bots}</span>/{w.bots}</td>
						<td class="text-right font-mono text-small">{w.sites}</td>
						<td class="text-right font-mono text-small">{fmtBytes(w.memory_bytes)}</td>
						<td class="text-right text-small text-muted">{w.last_active_at_ms ? fmtAgo(w.last_active_at_ms) : '—'}</td>
					</tr>
				{:else}
					<tr><td colspan="7" class="px-4 py-4 text-small text-muted">No workspaces match.</td></tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
