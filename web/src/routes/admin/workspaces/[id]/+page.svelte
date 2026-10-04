<script lang="ts">
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { roleText, type Bot, type Operation, type Site, type Workspace, type WorkspaceMember } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import AdminBotTable from '$lib/components/AdminBotTable.svelte';
	import OperationRow from '$lib/components/OperationRow.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	type View = { workspace: Workspace; members: WorkspaceMember[]; bots: (Bot & { owner_email: string })[]; sites: Site[]; operations: Operation[] };
	const id = $derived(page.params.id ?? '');
	let v = $state<View | null>(null);
	let error = $state('');

	async function load() {
		try {
			v = await api<View>('GET', `/admin/workspaces/${id}`);
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The workspace could not be loaded.';
		}
	}
	$effect(() => {
		if (id) load();
	});
	async function suspend(s: Site, disabled: boolean) {
		try {
			await api('PATCH', `/admin/sites/${s.id}`, { disabled });
			toast(disabled ? `Suspended ${s.name}` : `Restored ${s.name}`);
			await load();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The site could not be changed.', 'fail');
		}
	}
</script>

<svelte:head><title>{v?.workspace.name ?? 'Workspace'} · Administration · RivetPanel</title></svelte:head>

<a href="/admin/workspaces" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All workspaces</a>
{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
{#if !v && !error}
	<Skeleton rows={5} />
{:else if v}
	{@const w = v.workspace}
	<div class="flex flex-wrap items-center gap-3">
		<h2 class="text-section">{w.personal ? `Personal workspace of ${w.owner_email}` : w.name}</h2>
		<span class="pill">{w.personal ? 'Personal' : 'Team'}</span>
		<a class="btn btn-sm ml-auto" href="/settings/workspaces/{w.id}"><Icon name="users" size={14} />Manage members</a>
	</div>
	<p class="mt-1 text-small text-muted">Owned by <a class="link" href="/admin/users/{w.owner_id}">{w.owner_email}</a> · created {fmtWhen(w.created_at_ms)}</p>

	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		<div class="card px-4 py-3"><dt class="eyebrow">Bots running</dt><dd class="mt-1 font-mono text-title font-semibold">{w.running_bots}/{w.bots}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Memory</dt><dd class="mt-1 font-mono text-title font-semibold">{fmtBytes(w.memory_bytes)}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Members</dt><dd class="mt-1 font-mono text-title font-semibold">{w.members}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Sites</dt><dd class="mt-1 font-mono text-title font-semibold">{w.sites}</dd></div>
	</dl>

	<section class="mt-8" aria-labelledby="bots-h">
		<h3 id="bots-h" class="mb-2 text-title font-semibold">Bots</h3>
		<AdminBotTable bots={v.bots} empty="No bots in this workspace." />
	</section>

	<div class="mt-8 grid gap-8 xl:grid-cols-2">
		<section aria-labelledby="mem-h">
			<h3 id="mem-h" class="mb-2 text-title font-semibold">Members</h3>
			<ul class="card divide-y divide-rule-soft overflow-hidden">
				{#each v.members as m (m.user_id)}
					<li class="flex items-center gap-3 px-4 py-2.5">
						<a class="min-w-0 flex-1 truncate hover:underline" href="/admin/users/{m.user_id}">{m.display_name || m.email}</a>
						<span class="pill" data-tone={m.role === 'owner' ? 'run' : undefined}>{roleText[m.role]}</span>
					</li>
				{/each}
			</ul>
		</section>
		<section aria-labelledby="sites-h">
			<h3 id="sites-h" class="mb-2 text-title font-semibold">Sites</h3>
			{#if v.sites.length}
				<ul class="card divide-y divide-rule-soft overflow-hidden">
					{#each v.sites as s (s.id)}
						<li class="flex flex-wrap items-center gap-3 px-4 py-2.5">
							<div class="min-w-0 flex-1">
								<a class="block truncate font-medium hover:underline" href="/sites/{s.id}">{s.name}</a>
								<a class="block truncate font-mono text-[12px] text-action hover:underline" href={s.url} target="_blank" rel="noopener">{s.url.replace(/^https?:\/\//, '')}</a>
							</div>
							<span class="pill" data-tone={s.disabled ? 'fail' : s.current_release ? 'run' : undefined}>{s.disabled ? 'Suspended' : s.current_release ? 'Live' : 'Empty'}</span>
							<button class="btn btn-sm {s.disabled ? '' : 'btn-danger'}" onclick={() => suspend(s, !s.disabled)}>{s.disabled ? 'Restore' : 'Suspend'}</button>
						</li>
					{/each}
				</ul>
			{:else}
				<p class="card px-4 py-4 text-small text-muted">No sites in this workspace.</p>
			{/if}
		</section>
	</div>

	<section class="mt-8" aria-labelledby="ops-h">
		<h3 id="ops-h" class="mb-2 text-title font-semibold">Recent deployments and operations</h3>
		{#if v.operations.length}
			<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
				{#each v.operations as op (op.id)}<OperationRow {op} showBot />{/each}
			</ul>
		{:else}
			<p class="card px-4 py-4 text-small text-muted">No deployments, builds or backups yet.</p>
		{/if}
	</section>
{/if}
