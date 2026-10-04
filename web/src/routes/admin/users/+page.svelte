<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Role, User } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { adminPermissions } from '$lib/permissions';
	import { can, session } from '$lib/session.svelte';
	import { confirmDialog, confirmWithOption } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	type AdminUser = User & { bots: number; has_password: boolean };
	let users = $state<AdminUser[] | null>(null);
	let error = $state('');
	let q = $state('');
	let addOpen = $state(false);
	let inviteOpen = $state(false);
	let inviteForm = $state({ email: '', role: 'user', expires_in_days: 7, send_email: false });
	let inviteLink = $state('');
	let inviteMail = $state('');
	let form = $state({ email: '', password: '', role: 'user' });
	let addError = $state('');
	let busy = $state(false);
	let roles = $state<Role[]>([]);
	let roleFor = $state<AdminUser | null>(null);
	let roleChoice = $state('user');
	let roleOpen = $state(false);
	let emailFor = $state<AdminUser | null>(null);
	let emailOpen = $state(false);
	let newEmail = $state('');
	let emailError = $state('');
	const manage = $derived(can('users.manage'));
	const isAdmin = $derived(session.user?.role === 'admin');
	/** The role an account holds: a custom role id, or the built-in "admin"/"user". */
	const roleOf = (u: User) => u.role_id || u.role;
	const roleLabel = (u: User) => (u.role === 'admin' ? 'Administrator' : u.role_name || 'User');
	/** Roles this account may hand out: only administrators make administrators; others only roles within their own permissions. */
	const grantable = (r: Role) => isAdmin || (r.id !== 'admin' && r.permissions.every((p) => can(p)));
	/** Delegated managers cannot change administrators or accounts holding administration permissions they lack (the server refuses too). */
	const outranked = (u: User) =>
		!isAdmin && (u.role === 'admin' || (roles.find((r) => r.id === u.role_id)?.permissions ?? []).some((p) => adminPermissions.includes(p) && !can(p)));

	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	async function load() {
		try {
			users = (await api<{ users: AdminUser[] }>('GET', '/users')).users;
			error = '';
			roles = (await api<{ roles: Role[] }>('GET', '/admin/roles').catch(() => ({ roles: [] as Role[] }))).roles;
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		// Opened from the "New" menu: start with the invitation form.
		if (manage && new URLSearchParams(location.search).get('invite') === '1') inviteOpen = true;
	});

	const shown = $derived((users ?? []).filter((u) => !q || u.email.toLowerCase().includes(q.toLowerCase())));
	const admins = $derived((users ?? []).filter((u) => u.role === 'admin' && !u.disabled).length);

	async function add(e: SubmitEvent) {
		e.preventDefault();
		addError = '';
		busy = true;
		try {
			await api('POST', '/users', form);
			toast(`Created ${form.email}`);
			form = { email: '', password: '', role: 'user' };
			addOpen = false;
			await load();
		} catch (err) {
			addError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function invite(e: SubmitEvent) {
		e.preventDefault(); addError = ''; busy = true;
		try {
			const r = await api<{ path: string; emailed: boolean; email_error: string }>('POST', '/admin/account-invites', { ...inviteForm, send_email: inviteForm.send_email && !!inviteForm.email });
			inviteLink = location.origin + r.path;
			inviteMail = r.emailed ? `We emailed the link to ${inviteForm.email}. It is also here in case you need it.` : r.email_error ? `The email was not sent: ${r.email_error}.` : '';
			toast(r.emailed ? 'Invitation created and emailed' : 'Invitation created', 'success');
		} catch (err) { addError = msg(err); } finally { busy = false; }
	}
	async function copyInvite() { await navigator.clipboard.writeText(inviteLink); toast('Invitation link copied'); }
	async function patch(u: AdminUser, body: Record<string, unknown>, done: string) {
		try {
			await api('PATCH', `/users/${u.id}`, body);
			toast(done);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function disable(u: AdminUser) {
		const r = await confirmWithOption({
			title: `Disable ${u.email}?`,
			body: 'They are signed out everywhere at once: browser sessions, consoles and SFTP. Their bots stay as they are unless you stop them here.',
			confirmLabel: 'Disable account',
			tone: 'danger',
			checkbox: u.bots ? { label: `Also stop the ${u.bots} bot${u.bots === 1 ? '' : 's'} they own`, checked: false } : undefined
		});
		if (!r) return;
		await patch(u, { disabled: true, stop_bots: r.checked }, `Disabled ${u.email}`);
	}
	function openRole(u: AdminUser) {
		roleFor = u;
		roleChoice = roleOf(u);
		roleOpen = true;
	}
	async function saveRole(e: SubmitEvent) {
		e.preventDefault();
		const u = roleFor;
		if (!u || roleChoice === roleOf(u)) {
			roleOpen = false;
			return;
		}
		const r = roles.find((x) => x.id === roleChoice);
		if (roleChoice === 'admin') {
			const ok = await confirmDialog({
				title: `Make ${u.email} an administrator?`,
				body: 'Administrators can see and change every bot and every account on this panel.',
				confirmLabel: 'Make administrator',
				tone: 'primary'
			});
			if (!ok) return;
		}
		roleOpen = false;
		await patch(u, { role: roleChoice }, `${u.email} now has the role ${r?.name ?? roleChoice}`);
	}
	function openEmail(u: AdminUser) {
		emailFor = u;
		newEmail = u.email;
		emailError = '';
		emailOpen = true;
	}
	async function saveEmail(e: SubmitEvent) {
		e.preventDefault();
		const u = emailFor;
		if (!u) return;
		emailError = '';
		busy = true;
		try {
			await api('PATCH', `/users/${u.id}`, { email: newEmail });
			emailOpen = false;
			toast(`${u.email} now signs in as ${newEmail.trim().toLowerCase()}; the new address is not verified yet`);
			await load();
		} catch (err) {
			emailError = msg(err);
		} finally {
			busy = false;
		}
	}
	function menu(u: AdminUser): MenuItem[] {
		const self = u.id === session.user?.id;
		const lastAdmin = u.role === 'admin' && !u.disabled && admins <= 1;
		const adminTarget = outranked(u);
		const adminHint = u.role === 'admin' ? 'Administrators only' : 'Holds administration permissions you lack';
		return [
			{ label: 'Change role…', disabled: lastAdmin || adminTarget || (self && !isAdmin), hint: lastAdmin ? 'The only administrator' : adminTarget ? adminHint : self && !isAdmin ? 'Your own role' : undefined, onselect: () => openRole(u) },
			{ label: 'Change email…', disabled: adminTarget, hint: adminTarget ? adminHint : self ? 'Profile is the usual place' : undefined, onselect: () => openEmail(u) },
			u.email_verified
				? { label: 'Mark email as not verified', disabled: adminTarget || (self && !isAdmin), hint: adminTarget ? adminHint : undefined, onselect: () => patch(u, { email_verified: false }, `${u.email} is marked not verified`) }
				: { label: 'Mark email as verified', disabled: adminTarget || (self && !isAdmin), hint: adminTarget ? adminHint : self && !isAdmin ? 'Use the link on your profile' : undefined, onselect: () => patch(u, { email_verified: true }, `${u.email} is marked verified`) },
			'separator',
			u.disabled
				? { label: 'Enable account', disabled: adminTarget, hint: adminTarget ? adminHint : undefined, onselect: () => patch(u, { disabled: false }, `Enabled ${u.email}`) }
				: { label: 'Disable account', danger: true, disabled: self || lastAdmin || adminTarget, hint: self ? 'You cannot disable yourself' : adminTarget ? adminHint : undefined, onselect: () => disable(u) }
		];
	}
</script>

<svelte:head><title>Users · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Users</h2>
		<p class="mt-1 text-muted">{users?.length ?? '…'} account{users?.length === 1 ? '' : 's'}, {admins} administrator{admins === 1 ? '' : 's'}.</p>
	</div>
	{#if manage}<div class="flex gap-2"><button class="btn" onclick={() => { inviteOpen = true; inviteLink = ''; inviteMail = ''; addError = ''; }}><Icon name="link" />Invite user</button><button class="btn btn-primary" onclick={() => (addOpen = true)}><Icon name="plus" />Add user</button></div>{/if}
</div>

{#if (users?.length ?? 0) > 8}
	<label class="relative mt-4 block max-w-sm">
		<span class="sr-only">Search users</span>
		<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
		<input class="field pl-8" type="search" placeholder="Search by email" bind:value={q} />
	</label>
{/if}
{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}

<div class="mt-4">
	{#if users === null && !error}
		<Skeleton rows={3} />
	{:else}
		<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
			{#each shown as u (u.id)}
				<li class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 {u.disabled ? 'opacity-70' : ''}">
					{#if u.avatar_url}<img src={u.avatar_url} alt="" class="size-9 shrink-0 rounded-pill object-cover" />{:else}<span class="grid size-9 shrink-0 place-items-center rounded-pill bg-paper-2 text-small font-semibold text-muted" aria-hidden="true">{(u.display_name || u.email).slice(0, 2).toUpperCase()}</span>{/if}
					<div class="min-w-0 flex-1 basis-60">
						<p class="font-medium break-all"><a class="hover:underline" href="/admin/users/{u.id}">{u.display_name || u.email}</a>{#if u.id === session.user?.id}<span class="ml-2 text-small font-normal text-muted">You</span>{/if}<span class="pill ml-2 align-middle" data-tone={u.email_verified ? 'run' : 'warn'} title={u.email_verified ? 'Email address verified' : 'Email address not verified'}>{u.email_verified ? 'Verified' : 'Unverified'}</span></p>
						<p class="text-small text-muted">
							{u.display_name ? `${u.email} · ` : ''}{roleLabel(u)}{u.disabled ? ', disabled' : ''}. {u.bots} bot{u.bots === 1 ? '' : 's'}. {u.has_password ? 'Password' : 'Provider sign-in only'}. Joined {fmtWhen(u.created_at_ms)}.
						</p>
					</div>
					<a class="btn btn-sm hidden sm:inline-flex" href="/admin/users/{u.id}">Workspaces and bots<Icon name="chevronRight" size={13} /></a>
					{#if manage}<Menu label="Actions for {u.email}" items={menu(u)} />{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>
<p class="mt-3 max-w-prose text-small text-muted">A locked-out administrator can set a new password on the host with <code>rivetpanel reset-password EMAIL</code>.</p>

<Dialog bind:open={addOpen} title="Add a user" size="sm">
	<form id="user-add" class="grid gap-4" onsubmit={add}>
		<label class="block"><span class="label">Email</span><input class="field" type="email" required bind:value={form.email} autocomplete="off" /></label>
		<label class="block">
			<span class="label">Initial password</span>
			<input class="field" type="password" required minlength="12" autocomplete="new-password" bind:value={form.password} />
			<span class="help">At least 12 characters. They can change it under Settings.</span>
		</label>
		<label class="block">
			<span class="label">Role</span>
			<select class="field" bind:value={form.role}>
				{#each roles as r (r.id)}
					<option value={r.id} disabled={!grantable(r)}>{r.name}</option>
				{/each}
			</select>
			<span class="help">Custom roles are managed under Roles. You can only give roles whose permissions you hold.</span>
		</label>
		{#if addError}<p class="text-small text-fail" role="alert">{addError}</p>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="user-add" disabled={busy}>Add user</button>
	{/snippet}
</Dialog>

<Dialog bind:open={inviteOpen} title="Invite a user" size="sm">
	{#if inviteLink}
		<p class="text-muted">{inviteMail || 'This link is shown once. Send it to the person you want to invite.'}</p>
		<div class="mt-3 flex gap-2"><input class="field font-mono text-small" readonly value={inviteLink} /><button class="btn" onclick={copyInvite}>Copy</button></div>
	{:else}
		<form id="user-invite" class="grid gap-4" onsubmit={invite}>
			<label><span class="label">Email (optional)</span><input class="field" type="email" bind:value={inviteForm.email} placeholder="Locks the link to this address" /></label>
			<label><span class="label">Role</span><select class="field" bind:value={inviteForm.role}><option value="user">User</option>{#if isAdmin}<option value="admin">Administrator</option>{/if}</select><span class="help">Invitations give a built-in role; assign a custom role once the account exists.</span></label>
			<label><span class="label">Expires after</span><select class="field" bind:value={inviteForm.expires_in_days}><option value={1}>1 day</option><option value={3}>3 days</option><option value={7}>7 days</option><option value={14}>14 days</option></select></label>
			{#if session.features.mail}<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={inviteForm.send_email} disabled={!inviteForm.email} /><span>Email the link to this address<span class="help">{inviteForm.email ? 'The link is also shown here afterwards.' : 'Enter an email address above to send the link.'}</span></span></label>{/if}
			{#if addError}<p class="text-small text-fail">{addError}</p>{/if}
		</form>
	{/if}
	{#snippet footer()}<button class="btn" onclick={() => (inviteOpen = false)}>{inviteLink ? 'Done' : 'Cancel'}</button>{#if !inviteLink}<button class="btn btn-primary" type="submit" form="user-invite" disabled={busy}>Create invitation</button>{/if}{/snippet}
</Dialog>

<Dialog bind:open={roleOpen} title={roleFor ? `Role for ${roleFor.email}` : 'Role'} size="sm">
	<form id="user-role" class="grid gap-2" onsubmit={saveRole}>
		{#each roles as r (r.id)}
			<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft px-3 py-2 {grantable(r) ? '' : 'opacity-60'}">
				<input type="radio" name="role" class="mt-1" value={r.id} bind:group={roleChoice} disabled={!grantable(r)} />
				<span>{r.name}{#if r.system}<span class="pill ml-2" data-tone="idle">Built in</span>{/if}<span class="help">{r.description || (r.id === 'admin' ? 'Every permission.' : `${r.permissions.length} permission${r.permissions.length === 1 ? '' : 's'}.`)}{grantable(r) ? '' : ' Holds permissions you do not have.'}</span></span>
			</label>
		{/each}
		<p class="help">The change applies on the account's next request; open sessions are not signed out.</p>
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (roleOpen = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="user-role">Save role</button>
	{/snippet}
</Dialog>

<Dialog bind:open={emailOpen} title={emailFor ? `Email address for ${emailFor.email}` : 'Email address'} size="sm">
	<form id="user-email" class="grid gap-4" onsubmit={saveEmail}>
		<label class="block">
			<span class="label">New email address</span>
			<input class="field" type="email" required autocomplete="off" bind:value={newEmail} />
			<span class="help">Sign-in uses the new address at once and it is marked not verified. The old address is told about the change; outstanding reset and verification links stop working.</span>
		</label>
		{#if emailError}<p class="text-small text-fail" role="alert">{emailError}</p>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (emailOpen = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="user-email" disabled={busy}>Change email</button>
	{/snippet}
</Dialog>
