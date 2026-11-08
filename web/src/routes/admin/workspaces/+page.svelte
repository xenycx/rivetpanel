<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { NodeInfo, Workspace, WorkspaceQuota } from '$lib/api/types';
	import { can } from '$lib/session.svelte';
	import { wsName } from '$lib/workspaces.svelte';
	import { fmtAgo } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import { logoSeed } from '$lib/avatar';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let list = $state<Workspace[] | null>(null);
	// A workspace's own limits in a few words ("Custom" when they only set kinds).
	function limitText(q: WorkspaceQuota): string {
		const parts: string[] = [];
		if (q.max_apps != null) parts.push(`${q.max_apps} app${q.max_apps === 1 ? '' : 's'}`);
		if (q.max_bots != null) parts.push(`${q.max_bots} bot${q.max_bots === 1 ? '' : 's'}`);
		if (q.max_games != null) parts.push(`${q.max_games} server${q.max_games === 1 ? '' : 's'}`);
		if (q.max_web_apps != null) parts.push(`${q.max_web_apps} web`);
		if (q.max_sites != null) parts.push(`${q.max_sites} site${q.max_sites === 1 ? '' : 's'}`);
		if (q.memory_bytes != null) parts.push(fmtBytes(q.memory_bytes));
		if (q.nano_cpus != null) parts.push(`${q.nano_cpus / 1e9} CPU`);
		return parts.join(' · ') || 'Custom';
	}
	let error = $state('');
	let q = $state('');
	let kind = $state<'all' | 'team' | 'personal'>('all');
	let sort = $state<'activity' | 'bots' | 'memory' | 'name'>('activity');

	// Node names for pinned workspaces (GET /nodes needs nodes.manage).
	let nodeNames = $state<Record<string, string>>({});
	const nodeText = (w: Workspace) => (!w.node_id ? 'Default' : can('nodes.manage') ? (nodeNames[w.node_id] ?? 'Pinned') : 'Pinned');

	onMount(async () => {
		if (can('nodes.manage'))
			api<{ nodes: NodeInfo[] }>('GET', '/nodes')
				.then((r) => (nodeNames = Object.fromEntries(r.nodes.map((n) => [n.id, n.name]))))
				.catch(() => {});
		try {
			list = (await api<{ workspaces: Workspace[] }>('GET', '/admin/workspaces')).workspaces;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Workspaces could not be loaded.';
		}
	});

	const shown = $derived.by(() => {
		const needle = q.trim().toLowerCase();
		const out = (list ?? []).filter(
			(w) => (kind === 'all' || (kind === 'team') !== w.personal) && (!needle || `${wsName(w)} ${w.owner_email}`.toLowerCase().includes(needle))
		);
		const by: Record<typeof sort, (a: Workspace, b: Workspace) => number> = {
			activity: (a, b) => b.last_active_at_ms - a.last_active_at_ms,
			bots: (a, b) => b.bots - a.bots,
			memory: (a, b) => b.memory_bytes - a.memory_bytes,
			name: (a, b) => wsName(a).localeCompare(wsName(b))
		};
		return out.sort((a, b) => by[sort](a, b) || wsName(a).localeCompare(wsName(b)));
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
<p class="mt-1 max-w-prose text-muted">Every account's personal workspace and every team, with what runs in it. Open one to see its members, apps, sites and recent deployments.</p>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if list === null && !error}
	<div class="mt-4"><Skeleton rows={4} /></div>
{:else if list}
	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		{#each [['Workspaces', `${list.length}`, `${totals.teams} team${totals.teams === 1 ? '' : 's'}`], ['Apps', `${totals.running}/${totals.bots}`, 'running / total'], ['Memory', fmtBytes(totals.memory), 'assigned to apps'], ['Sites', `${totals.sites}`, 'hosted']] as [k, v, h] (k)}
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
		<select class="field w-auto" bind:value={sort} aria-label="Sort"><option value="activity">Recently active</option><option value="bots">Most apps</option><option value="memory">Most memory</option><option value="name">Name</option></select>
	</div>

	<!-- The name takes the spare width; the numbers keep to one line. Limits
	     and node share a column, shown from xl up; Members and Sites from lg. -->
	<div class="card mt-3 overflow-x-auto">
		<table class="w-full text-left">
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal [&>th]:whitespace-nowrap">
					<th class="eyebrow w-full">Workspace</th><th class="eyebrow hidden text-right lg:table-cell">Members</th><th class="eyebrow text-right">Apps</th><th class="eyebrow hidden text-right lg:table-cell">Sites</th><th class="eyebrow text-right">Memory</th><th class="eyebrow hidden xl:table-cell">Limits · node</th><th class="eyebrow text-right">Active</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each shown as w (w.id)}
					<tr class="[&>td]:px-4 [&>td]:py-2.5 hover:bg-paper/40">
						<td class="w-full max-w-0">
							<div class="flex min-w-0 items-center gap-2.5">
								<Logo src={w.logo_url} kind="workspace" seed={logoSeed(w.id, w.logo_variant)} class="size-8 rounded-control" />
								<div class="min-w-0">
									<a class="block truncate font-medium hover:underline" href="/admin/workspaces/{w.id}" title={wsName(w)}>{wsName(w)}</a>
									<p class="truncate text-small text-muted">{w.personal ? (wsName(w) === 'Personal' ? '' : 'Personal · ') : 'Team · '}<a class="hover:text-ink hover:underline" href="/admin/users/{w.owner_id}" title="Owner: {w.owner_email}">{w.owner_email}</a></p>
								</div>
							</div>
						</td>
						<td class="hidden text-right font-mono text-small whitespace-nowrap tabular-nums lg:table-cell">{w.members}</td>
						<td class="text-right font-mono text-small whitespace-nowrap tabular-nums" title="{w.running_bots} running of {w.bots}"><span class={w.running_bots ? 'text-run' : ''}>{w.running_bots}</span>/{w.bots}</td>
						<td class="hidden text-right font-mono text-small whitespace-nowrap tabular-nums lg:table-cell">{w.sites}</td>
						<td class="text-right font-mono text-small whitespace-nowrap tabular-nums">{fmtBytes(w.memory_bytes)}</td>
						<td class="hidden max-w-56 text-small text-muted xl:table-cell">
							<span class="block truncate {w.quota ? 'text-ink' : ''}" title={w.quota ? `Own limits: ${limitText(w.quota)}` : 'Panel default limits'}>{w.quota ? limitText(w.quota) : 'Default limits'}</span>
							{#if w.node_id}<span class="block truncate" title={can('nodes.manage') ? 'New apps in this workspace go to this node' : 'New apps in this workspace go to a pinned node'}>Node: {nodeText(w)}</span>{/if}
						</td>
						<td class="text-right text-small whitespace-nowrap text-muted tabular-nums">{w.last_active_at_ms ? fmtAgo(w.last_active_at_ms) : '—'}</td>
					</tr>
				{:else}
					<tr><td colspan="7" class="px-4 py-4 text-small text-muted">No workspaces match.</td></tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
