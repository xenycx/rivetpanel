<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import { roleHelp, roleRank, roleText, type Workspace, type WorkspaceMember, type WorkspaceRole } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { loadWorkspaces, selectWorkspace } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	const id = $derived(page.params.id ?? '');
	let ws = $state<Workspace | null>(null);
	let members = $state<WorkspaceMember[]>([]);
	let error = $state('');
	let name = $state('');
	let email = $state('');
	let role = $state<WorkspaceRole>('developer');
	let busy = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			const r = await api<{ workspace: Workspace; members: WorkspaceMember[] }>('GET', `/workspaces/${id}`);
			ws = r.workspace;
			members = r.members;
			name = r.workspace.name;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	$effect(() => {
		if (id) load();
	});

	// Panel administrators manage every workspace.
	const myRank = $derived(session.user?.role === 'admin' ? 4 : roleRank[ws?.role ?? '']);
	const manage = $derived(myRank >= roleRank.admin);

	async function rename(e: SubmitEvent) {
		e.preventDefault();
		try {
			await api('PATCH', `/workspaces/${id}`, { name });
			toast('Workspace renamed', 'success');
			await Promise.all([load(), loadWorkspaces()]);
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function add(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		try {
			const m = await api<WorkspaceMember>('PUT', `/workspaces/${id}/members`, { email, role });
			toast(`${m.email} is now ${roleText[m.role].toLowerCase()}`, 'success');
			email = '';
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		} finally {
			busy = false;
		}
	}
	async function setRole(m: WorkspaceMember, r: WorkspaceRole) {
		try {
			await api('PATCH', `/workspaces/${id}/members/${m.user_id}`, { role: r });
			toast(`${m.email} is now ${roleText[r].toLowerCase()}`);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
			await load();
		}
	}
	async function remove(m: WorkspaceMember) {
		const self = m.user_id === session.user?.id;
		const ok = await confirmDialog({
			title: self ? `Leave ${ws?.name}?` : `Remove ${m.email}?`,
			body: self ? 'You lose access to its bots and sites unless they are shared with you directly.' : 'They lose access to this workspace’s bots and sites unless shared with them directly. Bots they created stay in the workspace.',
			confirmLabel: self ? 'Leave workspace' : 'Remove member',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/workspaces/${id}/members/${m.user_id}`);
			if (self) {
				await loadWorkspaces();
				goto('/settings/workspaces');
				return;
			}
			toast(`Removed ${m.email}`);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function destroy() {
		const ok = await confirmDialog({
			title: `Delete ${ws?.name}?`,
			body: 'Members lose access immediately. The workspace must be empty: move or delete its bots and sites first.',
			confirmLabel: 'Delete workspace',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/workspaces/${id}`);
			selectWorkspace('all');
			await loadWorkspaces();
			toast('Workspace deleted');
			goto('/settings/workspaces');
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	const title = $derived(ws ? (ws.personal ? 'Personal workspace' : ws.name) : 'Workspace');
</script>

<svelte:head><title>{title} · RivetPanel</title></svelte:head>

<a href="/settings/workspaces" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All workspaces</a>
{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
{#if !ws && !error}
	<Skeleton rows={4} />
{:else if ws}
	<div class="mb-2 flex flex-wrap items-center gap-3">
		<h2 class="text-section">{title}</h2>
		{#if ws.role}<span class="pill">{roleText[ws.role]}</span>{/if}
		<button class="btn btn-sm ml-auto" onclick={() => { selectWorkspace(ws!.id); goto('/dashboard'); }}><Icon name="home" size={14} />Open overview</button>
	</div>

	{#if manage && !ws.personal}
		<SettingsSection title="Name" description="Shown in the workspace switcher and to every member.">
			<form class="card flex flex-wrap items-end gap-3 p-5" onsubmit={rename}>
				<label class="block min-w-0 flex-1 basis-60"><span class="label">Workspace name</span><input class="field" required maxlength="64" bind:value={name} /></label>
				<button class="btn" disabled={name.trim() === ws.name || !name.trim()}>Rename</button>
			</form>
		</SettingsSection>
	{/if}

	<SettingsSection title="Members" description="Roles apply to every bot{session.features.sites ? ' and site' : ''} in the workspace. Sharing a single bot (on its Users tab) adds access on top.">
		<div class="card overflow-hidden">
			<ul class="divide-y divide-rule-soft">
				{#each members as m (m.user_id)}
					<li class="flex flex-wrap items-center gap-3 px-4 py-3">
						<span class="grid size-9 shrink-0 place-items-center rounded-pill bg-paper-2 text-small font-semibold text-muted" aria-hidden="true">{(m.display_name || m.email).slice(0, 2).toUpperCase()}</span>
						<div class="min-w-0 flex-1 basis-48">
							<p class="truncate font-medium">{m.display_name || m.email}{#if m.user_id === session.user?.id}<span class="ml-2 text-small font-normal text-muted">you</span>{/if}</p>
							<p class="truncate text-small text-muted">{m.display_name ? `${m.email} · ` : ''}added {fmtWhen(m.created_at_ms)}</p>
						</div>
						{#if m.role === 'owner' || !manage}
							<span class="pill" data-tone={m.role === 'owner' ? 'run' : undefined}>{roleText[m.role]}</span>
						{:else}
							<select class="field w-auto" aria-label="Role of {m.email}" value={m.role} onchange={(e) => setRole(m, e.currentTarget.value as WorkspaceRole)}>
								{#each ['admin', 'developer', 'viewer'] as const as r (r)}<option value={r}>{roleText[r]}</option>{/each}
							</select>
						{/if}
						{#if m.role !== 'owner' && (manage || m.user_id === session.user?.id)}
							<button class="btn btn-sm btn-quiet" onclick={() => remove(m)} aria-label={m.user_id === session.user?.id ? 'Leave workspace' : `Remove ${m.email}`}>
								{m.user_id === session.user?.id ? 'Leave' : 'Remove'}
							</button>
						{/if}
					</li>
				{/each}
			</ul>
			{#if manage}
				<form class="grid gap-3 border-t border-rule-soft bg-paper/40 p-4 sm:grid-cols-[minmax(0,1fr)_12rem_auto] sm:items-end" onsubmit={add}>
					<label class="block"><span class="label">Add by email</span><input class="field" type="email" required bind:value={email} placeholder="teammate@example.com" /></label>
					<label class="block"><span class="label">Role</span>
						<select class="field" bind:value={role}>{#each ['admin', 'developer', 'viewer'] as const as r (r)}<option value={r}>{roleText[r]}</option>{/each}</select>
					</label>
					<button class="btn btn-primary" disabled={busy}><Icon name="plus" size={14} />Add member</button>
					<p class="text-small text-muted sm:col-span-3">{roleHelp[role]}. They need an account on this panel first.</p>
				</form>
			{/if}
		</div>
		<dl class="mt-4 grid gap-2 text-small sm:grid-cols-2">
			{#each ['owner', 'admin', 'developer', 'viewer'] as const as r (r)}
				<div class="flex gap-2"><dt class="w-20 shrink-0 font-medium">{roleText[r]}</dt><dd class="text-muted">{roleHelp[r]}</dd></div>
			{/each}
		</dl>
	</SettingsSection>

	{#if !ws.personal && myRank >= roleRank.owner}
		<SettingsSection title="Delete workspace" description="Only an empty workspace can be deleted. Personal workspaces cannot be deleted.">
			<div class="card flex flex-wrap items-center gap-4 border-fail/30 p-5">
				<p class="min-w-0 flex-1 basis-60 text-small text-muted">{ws.bots} bot{ws.bots === 1 ? '' : 's'} and {ws.sites} site{ws.sites === 1 ? '' : 's'} are in this workspace.</p>
				<button class="btn btn-danger" onclick={destroy} disabled={ws.bots > 0 || ws.sites > 0}>Delete workspace</button>
			</div>
		</SettingsSection>
	{/if}
{/if}
