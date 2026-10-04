<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { PermissionInfo, Role } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { can, session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// Roles are enforced by the server on every request; this page only edits
	// them. The two built-in roles are fixed.
	let roles = $state<Role[] | null>(null);
	let catalog = $state<PermissionInfo[]>([]);
	let error = $state('');
	let open = $state(false);
	let editing = $state<Role | null>(null);
	let viewing = $state(false);
	let form = $state({ name: '', description: '', permissions: [] as string[] });
	let formError = $state('');
	let busy = $state(false);

	const manage = $derived(can('roles.manage'));
	const isAdmin = $derived(session.user?.role === 'admin');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');
	const groups = [
		{ id: 'resources', title: 'Bots, servers and sites', help: 'What accounts with this role may do with what they own or what is shared with them.' },
		{ id: 'administration', title: 'Administration', help: 'Parts of the administration area this role opens.' }
	] as const;
	const label = (p: string) => catalog.find((x) => x.name === p)?.label ?? p;

	async function load() {
		try {
			const [r, p] = await Promise.all([api<{ roles: Role[] }>('GET', '/admin/roles'), api<{ permissions: PermissionInfo[] }>('GET', '/admin/permissions')]);
			roles = r.roles;
			catalog = p.permissions;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	function openNew() {
		editing = null;
		viewing = false;
		form = { name: '', description: '', permissions: catalog.filter((p) => p.group === 'resources' && can(p.name)).map((p) => p.name) };
		formError = '';
		open = true;
	}
	function openRole(r: Role, readOnly: boolean) {
		editing = r;
		viewing = readOnly;
		form = { name: r.name, description: r.description, permissions: [...r.permissions] };
		formError = '';
		open = true;
	}
	function toggle(p: string, on: boolean) {
		form.permissions = on ? [...form.permissions.filter((x) => x !== p), p] : form.permissions.filter((x) => x !== p);
	}
	function setGroup(group: string, on: boolean) {
		const names = catalog.filter((p) => p.group === group && (isAdmin || can(p.name))).map((p) => p.name);
		form.permissions = on ? [...new Set([...form.permissions, ...names])] : form.permissions.filter((x) => !names.includes(x));
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (viewing) return;
		busy = true;
		formError = '';
		// Keep the catalog order so the audit record and the list read naturally.
		const body = { ...form, permissions: catalog.map((p) => p.name).filter((p) => form.permissions.includes(p)) };
		try {
			if (editing) {
				await api('PATCH', `/admin/roles/${editing.id}`, body);
				toast(`Saved ${form.name}. Accounts with it follow the change on their next request.`, 'success');
			} else {
				await api('POST', '/admin/roles', body);
				toast(`Created ${form.name}`, 'success');
			}
			open = false;
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function remove(r: Role) {
		const ok = await confirmDialog({
			title: `Delete the role ${r.name}?`,
			body: 'This cannot be undone. A role can only be deleted when no account holds it.',
			confirmLabel: 'Delete role',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/admin/roles/${r.id}`);
			toast(`Deleted ${r.name}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	function menu(r: Role): MenuItem[] {
		const mine = session.user?.role_id === r.id;
		return [
			{ label: 'Edit', disabled: mine && !isAdmin, hint: mine && !isAdmin ? 'Your own role' : undefined, onselect: () => openRole(r, false) },
			{ label: 'Delete', danger: true, disabled: r.users > 0, hint: r.users > 0 ? 'Still assigned' : undefined, onselect: () => remove(r) }
		];
	}
</script>

<svelte:head><title>Roles · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Roles</h2>
		<p class="mt-1 max-w-3xl text-muted">A role is a named set of permissions. The panel checks them on every request, so a missing permission is refused by the server, not only hidden here.</p>
	</div>
	{#if manage}<button class="btn btn-primary" onclick={openNew} disabled={!catalog.length}><Icon name="plus" />New role</button>{/if}
</div>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

<div class="mt-4">
	{#if roles === null && !error}
		<Skeleton rows={3} label="Loading roles" />
	{:else if roles}
		<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
			{#each roles as r (r.id)}
				<li class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
					<div class="min-w-0 flex-1 basis-60">
						<p class="font-medium">
							{r.name}
							{#if r.system}<span class="pill ml-2" data-tone="idle">Built in</span>{/if}
							{#if session.user?.role_id === r.id || (!session.user?.role_id && session.user?.role === r.id)}<span class="ml-2 text-small font-normal text-muted">Yours</span>{/if}
						</p>
						{#if r.description}<p class="text-small text-muted">{r.description}</p>{/if}
						<p class="text-small text-muted">
							{r.id === 'admin' ? 'Every permission' : `${r.permissions.length} permission${r.permissions.length === 1 ? '' : 's'}`} · {r.users} account{r.users === 1 ? '' : 's'}{r.system ? '' : ` · changed ${fmtWhen(r.updated_at_ms)}`}
						</p>
					</div>
					<button class="btn btn-sm" onclick={() => openRole(r, r.system || !manage)}>{r.system || !manage ? 'View' : 'Permissions'}</button>
					{#if manage && !r.system}<Menu label="Actions for {r.name}" items={menu(r)} />{/if}
				</li>
			{/each}
		</ul>
		<p class="mt-3 max-w-prose text-small text-muted">
			Built-in roles cannot be changed: administrators hold every permission, including the environment editor and making other administrators; users hold every permission for their own bots, servers and sites and nothing in the administration. Assign roles on the Users page.
			{#if !isAdmin}You can only grant permissions you hold yourself.{/if}
		</p>
	{/if}
</div>

<Dialog bind:open title={viewing ? (editing?.name ?? 'Role') : editing ? `Edit ${editing.name}` : 'New role'} size="lg">
	<form id="role-form" class="grid gap-4" onsubmit={save}>
		{#if !viewing}
			<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={form.name} placeholder="Support, Developers…" /></label>
			<label class="block"><span class="label">Description (optional)</span><input class="field" maxlength="280" bind:value={form.description} /></label>
		{:else if editing?.description}
			<p class="text-muted">{editing.description}</p>
		{/if}
		{#each groups as g (g.id)}
			<fieldset class="grid gap-2">
				<legend class="label">{g.title}</legend>
				<div class="-mt-1 flex flex-wrap items-baseline gap-2">
					<p class="help mt-0 flex-1">{g.help}</p>
					{#if !viewing}
						<button type="button" class="link text-small" onclick={() => setGroup(g.id, true)}>All</button>
						<button type="button" class="link text-small" onclick={() => setGroup(g.id, false)}>None</button>
					{/if}
				</div>
				<div class="grid gap-2 sm:grid-cols-2">
					{#each catalog.filter((p) => p.group === g.id) as p (p.name)}
						{@const held = isAdmin || can(p.name)}
						<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft px-3 py-2 {viewing || held ? '' : 'opacity-60'}">
							<input type="checkbox" class="mt-0.5" checked={editing?.id === 'admin' || form.permissions.includes(p.name)} disabled={viewing || !held} onchange={(e) => toggle(p.name, e.currentTarget.checked)} />
							<span>{p.label}<span class="help">{p.description}{!viewing && !held ? ' You do not hold this permission.' : ''}</span><code class="text-small text-muted">{p.name}</code></span>
						</label>
					{/each}
				</div>
			</fieldset>
		{/each}
		{#if editing?.id === 'admin'}<Notice tone="info">Administrators also open the environment editor and the modules page, and can make other administrators; no custom role can.</Notice>{/if}
		{#if !viewing && form.permissions.length === 0}<p class="text-small text-muted">A role without permissions can sign in and see what is shared with it, but cannot change anything.</p>{/if}
		{#if formError}<p class="text-small text-fail" role="alert">{formError}</p>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>{viewing ? 'Close' : 'Cancel'}</button>
		{#if !viewing}<button class="btn btn-primary" type="submit" form="role-form" disabled={busy}>{editing ? 'Save role' : 'Create role'}</button>{/if}
	{/snippet}
</Dialog>
