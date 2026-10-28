<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { roleText, type Bot, type Operation, type Site, type Workspace, type WorkspaceMember } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import { promptDialog } from '$lib/ui/dialogs.svelte';
	import { can } from '$lib/session.svelte';
	import { wsName } from '$lib/workspaces.svelte';
	import AdminBotTable from '$lib/components/AdminBotTable.svelte';
	import OperationRow from '$lib/components/OperationRow.svelte';
	import WorkspaceQuotaForm from '$lib/components/WorkspaceQuotaForm.svelte';
	import WorkspacePlacementForm from '$lib/components/WorkspacePlacementForm.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
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

	async function rename() {
		if (!v) return;
		const current = v.workspace.name;
		const name = await promptDialog({
			title: 'Rename workspace',
			body: 'Shown in the workspace switcher and to every member.',
			label: 'Workspace name',
			value: current,
			confirmLabel: 'Rename',
			validate: (x) => (!x.trim() ? 'Enter a name.' : x.trim().length > 64 ? 'Use 64 characters or fewer.' : null)
		});
		if (name === null || name.trim() === current) return;
		try {
			await api('PATCH', `/admin/workspaces/${id}`, { name: name.trim() });
			toast(`Renamed to ${name.trim()}`, 'success');
			await load();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The workspace could not be renamed.', 'fail');
		}
	}

	// Deleting a team workspace with everything in it, after typing its name.
	let delOpen = $state(false);
	let delConfirm = $state('');
	let delError = $state('');
	let deleting = $state(false);
	function openDelete() {
		delConfirm = delError = '';
		delOpen = true;
	}
	async function destroy(e: SubmitEvent) {
		e.preventDefault();
		if (!v || delConfirm !== v.workspace.name) return;
		deleting = true;
		delError = '';
		try {
			const name = v.workspace.name;
			await api('POST', `/admin/workspaces/${id}/delete`, { confirm: delConfirm });
			delOpen = false;
			toast(`Deleted ${name}`, 'success');
			goto('/admin/workspaces');
		} catch (err) {
			delError = err instanceof ApiError ? err.message : 'The workspace could not be deleted.';
		} finally {
			deleting = false;
		}
	}
	const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
</script>

<svelte:head><title>{v?.workspace.name ?? 'Workspace'} · Administration · RivetPanel</title></svelte:head>

<a href="/admin/workspaces" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All workspaces</a>
{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
{#if !v && !error}
	<Skeleton rows={5} />
{:else if v}
	{@const w = v.workspace}
	<div class="flex flex-wrap items-center gap-3">
		<h2 class="text-section">{wsName(w)}</h2>
		<span class="pill">{w.personal ? 'Personal' : 'Team'}</span>
		<div class="ml-auto flex flex-wrap gap-2">
			{#if can('workspaces.manage')}<button class="btn btn-sm" onclick={rename}><Icon name="pencil" size={14} />Rename</button>{/if}
			<a class="btn btn-sm" href="/settings/workspaces/{w.id}"><Icon name="users" size={14} />Manage members</a>
		</div>
	</div>
	<p class="mt-1 text-small text-muted">Owned by <a class="link" href="/admin/users/{w.owner_id}">{w.owner_email}</a> · created {fmtWhen(w.created_at_ms)}</p>

	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		<div class="card px-4 py-3"><dt class="eyebrow">Bots running</dt><dd class="mt-1 font-mono text-title font-semibold">{w.running_bots}/{w.bots}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Memory</dt><dd class="mt-1 font-mono text-title font-semibold">{fmtBytes(w.memory_bytes)}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Members</dt><dd class="mt-1 font-mono text-title font-semibold">{w.members}</dd></div>
		<div class="card px-4 py-3"><dt class="eyebrow">Sites</dt><dd class="mt-1 font-mono text-title font-semibold">{w.sites}</dd></div>
	</dl>

	<WorkspaceQuotaForm workspaceId={id} />
	<WorkspacePlacementForm workspaceId={id} nodeId={w.node_id ?? ''} />

	<section class="mt-8" aria-labelledby="bots-h">
		<h3 id="bots-h" class="mb-2 text-title font-semibold">Bots</h3>
		<AdminBotTable bots={v.bots} empty="No apps in this workspace." />
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

	{#if !w.personal && can('workspaces.manage')}
		<section class="mt-8" aria-labelledby="danger-h">
			<h3 id="danger-h" class="mb-2 text-title font-semibold">Delete workspace</h3>
			<div class="card flex flex-wrap items-center gap-4 border-fail/30 p-5">
				<p class="min-w-0 flex-1 basis-60 text-small text-muted">Deletes the workspace with every app, site and membership in it. This cannot be undone.</p>
				<button class="btn btn-danger" onclick={openDelete}>Delete workspace</button>
			</div>
		</section>

		<Dialog bind:open={delOpen} title="Delete {w.name}?" dismissable={!deleting}>
			<form id="ws-delete" class="grid gap-3" onsubmit={destroy}>
				<p>This permanently removes:</p>
				<ul class="list-disc pl-5 text-small">
					<li>{plural(w.bots, 'app')} with their containers, files, backups and add-ons</li>
					<li>{plural(w.sites, 'site')}</li>
					<li>{plural(w.members, 'member')}</li>
				</ul>
				<label class="block">
					<span class="label">Type <b>{w.name}</b> to confirm</span>
					<input class="field" autocomplete="off" spellcheck="false" bind:value={delConfirm} disabled={deleting} />
				</label>
				{#if delError}<Notice tone="fail" live>{delError}</Notice>{/if}
			</form>
			{#snippet footer()}
				<button class="btn" type="button" onclick={() => (delOpen = false)} disabled={deleting}>Cancel</button>
				<button class="btn btn-danger" type="submit" form="ws-delete" disabled={deleting || delConfirm !== w.name}>{deleting ? 'Deleting…' : 'Delete workspace'}</button>
			{/snippet}
		</Dialog>
	{/if}
{/if}
