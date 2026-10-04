<script lang="ts">
	import { untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { Perm, type SubUser } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import ChangeList from '$lib/components/ChangeList.svelte';
	import type { AuditEvent, AuditPage } from '$lib/audit';
	import { confirmWithOption } from '$lib/ui/dialogs.svelte';
	import { goto } from '$app/navigation';
	import { session } from '$lib/session.svelte';

	let { botId, botName, isGame = false }: { botId: string; botName: string; isGame?: boolean } = $props();
	const noun = $derived(isGame ? 'server' : 'bot');

	const options = $derived(
		isGame
			? [
					{ bit: Perm.console, label: 'View console', hint: 'Live output, players, analytics and resource usage' },
					{ bit: Perm.power, label: 'Start / stop', hint: 'Start, stop, restart, kill and send console commands' },
					{ bit: Perm.files, label: 'Edit files', hint: 'File manager, SFTP, properties, mods and plugins, and backups' },
					{ bit: Perm.env, label: 'Change server settings', hint: 'Startup variables of the server type, such as the version' },
					{ bit: Perm.admin, label: 'Full admin', hint: 'Everything above plus settings, Java image, reinstall, network, restore and delete backups' }
				]
			: [
					{ bit: Perm.console, label: 'View console', hint: 'Live output, analytics and resource usage' },
					{ bit: Perm.power, label: 'Start / stop', hint: 'Start, stop, restart, kill and send console input' },
					{ bit: Perm.files, label: 'Edit files', hint: 'File manager, SFTP, packages, deployments and backups' },
					{ bit: Perm.env, label: 'Manage env vars', hint: 'Read and change environment variables, including secrets' },
					{ bit: Perm.admin, label: 'Full admin', hint: 'Everything above plus settings, startup, restore and delete backups' }
				]
	);

	let users = $state<SubUser[]>([]);
	let email = $state('');
	let perms = $state<number>(Perm.console);
	let error = $state('');
	const path = $derived(`/bots/${botId}/users`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		users = (await api<{ users: SubUser[] }>('GET', path)).users;
	}
	let changes = $state<AuditEvent[] | null>(null);
	let transferTo = $state('');
	// Loads the people and recent changes of the bot shown (again when
	// another bot is opened here).
	$effect(() => {
		const id = botId;
		untrack(() => {
			load().catch((e) => (error = msg(e)));
			api<AuditPage>('GET', `/bots/${id}/changes?limit=8`).then((r) => (changes = r.events)).catch(() => (changes = []));
		});
	});

	async function transfer(e: SubmitEvent) {
		e.preventDefault();
		const r = await confirmWithOption({
			title: `Transfer ${botName} to ${transferTo}?`,
			body: isGame
				? 'They become the owner: they can delete the server, share it and change its ports.'
				: 'They become the owner: they can delete the bot, share it and publish ports. A linked GitHub repository is unlinked, because it uses your GitHub access; the new owner links it again with theirs.',
			confirmLabel: 'Transfer ownership',
			tone: 'danger',
			checkbox: { label: 'Keep full access for me', checked: true }
		});
		if (!r) return;
		try {
			const res = await api<{ repository_unlinked: boolean }>('POST', `/bots/${botId}/transfer`, { email: transferTo, keep_access: r.checked });
			toast(`${botName} now belongs to ${transferTo}${res.repository_unlinked ? '. The repository link was removed.' : ''}`);
			if (!r.checked && session.user?.role !== 'admin') await goto(isGame ? '/servers' : '/dashboard');
			else location.reload();
		} catch (err) {
			error = msg(err);
		}
	}

	function toggle(mask: number, bit: number): number {
		if (bit === Perm.admin) return mask & Perm.admin ? mask & ~Perm.admin : 31;
		const next = mask ^ bit;
		return next & Perm.admin ? next & ~Perm.admin : next;
	}

	async function save(em: string, p: number, added = false) {
		error = '';
		try {
			await api('PUT', path, { email: em, permissions: p });
			await load();
			toast(added ? `Shared with ${em}` : `Updated access for ${em}`);
		} catch (e) {
			error = msg(e);
		}
	}
	async function add(e: SubmitEvent) {
		e.preventDefault();
		await save(email, perms, true);
		if (!error) email = '';
	}
	// Invitation links: shared by the owner, accepted once by whoever signs in.
	type Invite = { id: string; permissions: number; created_by: string; created_at_ms: number; expires_at_ms: number };
	let invites = $state<Invite[]>([]);
	let invPerms = $state<number>(Perm.console);
	let invDays = $state(3);
	let invLink = $state('');
	async function loadInvites() {
		invites = (await api<{ invites: Invite[] }>('GET', `/bots/${botId}/invites`)).invites;
	}
	$effect(() => {
		void botId;
		untrack(() => loadInvites().catch(() => {}));
	});
	async function createInvite(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		try {
			const r = await api<{ path: string }>('POST', `/bots/${botId}/invites`, { permissions: invPerms, expires_in_days: invDays });
			invLink = location.origin + r.path;
			await loadInvites();
		} catch (err) {
			error = msg(err);
		}
	}
	async function revokeInvite(v: Invite) {
		try {
			await api('DELETE', `/bots/${botId}/invites/${v.id}`);
			toast('Invitation revoked');
			await loadInvites();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	const permNames = (m: number) => (m & Perm.admin ? 'Full admin' : options.filter((o) => m & o.bit).map((o) => o.label).join(', '));

	async function remove(u: SubUser) {
		const ok = await confirmDialog({ title: `Remove ${u.email}?`, body: `They lose access to this ${noun} immediately, including open consoles and SFTP.`, confirmLabel: 'Remove access', tone: 'danger' });
		if (!ok) return;
		error = '';
		try {
			await api('DELETE', `${path}/${u.user_id}`);
			await load();
			toast(`Removed ${u.email}`);
		} catch (e) {
			error = msg(e);
		}
	}
</script>

<SettingsSection title="People with access" description={isGame ? 'The owner and administrators always have full access. Anyone who can change files can change the world, plugins and server configuration, so share that only with people you trust.' : 'The owner and administrators always have full access. Anyone who can change files or environment variables can make the bot reveal its secrets, so share those only with people you trust.'}>
	<ul class="list-card">
		{#each users as u (u.user_id)}
			<li class="grid gap-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
				<div class="min-w-0">
					<div class="font-medium break-all">{u.email}</div>
					<div class="mt-1 flex flex-wrap gap-x-4 gap-y-1">
						{#each options as o (o.bit)}
							<label class="flex items-center gap-1.5" title={o.hint}>
								<input type="checkbox" checked={(u.permissions & o.bit) !== 0} onchange={() => save(u.email, toggle(u.permissions, o.bit))} />{o.label}
							</label>
						{/each}
					</div>
				</div>
				<button class="btn btn-sm btn-danger" onclick={() => remove(u)}>Remove access</button>
			</li>
		{:else}
			<li class="px-4 py-4 text-muted">This {noun} is not shared with anyone yet.</li>
		{/each}
	</ul>
</SettingsSection>

<SettingsSection title="Share with someone" description="Give an existing panel account access by the email they sign in with. Tick only what they need.">
	<form onsubmit={add}>
		<label class="block"><span class="label">Email of a panel user</span><input class="field" type="email" required bind:value={email} placeholder="friend@example.com" /></label>
		<div class="mt-3 grid gap-3 sm:grid-cols-2">
			{#each options as o (o.bit)}
				<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft bg-panel px-3 py-2.5 has-[:checked]:border-action/40"><input type="checkbox" class="mt-0.5" checked={(perms & o.bit) !== 0} onchange={() => (perms = toggle(perms, o.bit))} /><span>{o.label}<span class="help mt-0">{o.hint}</span></span></label>
			{/each}
		</div>
		{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
		<button class="btn btn-primary mt-4" disabled={perms === 0}>Share {noun}</button>
	</form>
</SettingsSection>

<SettingsSection title="Invite with a link" description="For someone who has an account here but whose email you do not know. The link works once, for the first person who opens it and accepts.">
	<form class="grid gap-3" onsubmit={createInvite}>
		<div class="flex flex-wrap gap-x-5 gap-y-2">
			{#each options as o (o.bit)}
				<label class="flex items-center gap-2"><input type="checkbox" checked={(invPerms & o.bit) !== 0} onchange={() => (invPerms = toggle(invPerms, o.bit))} /><span>{o.label}</span></label>
			{/each}
		</div>
		<div class="flex flex-wrap items-end gap-2">
			<label class="block"><span class="label">Expires after</span>
				<select class="field w-auto" bind:value={invDays}>{#each [1, 3, 7, 14] as d (d)}<option value={d}>{d} day{d === 1 ? '' : 's'}</option>{/each}</select>
			</label>
			<button class="btn" disabled={invPerms === 0}>Create invitation link</button>
		</div>
	</form>
	{#if invLink}
		<Notice tone="warn" class="mt-3" title="Copy the link now">
			It is shown only once. Anyone who has it can accept it.<br /><code class="break-all">{invLink}</code>
			{#snippet action()}<button class="btn btn-sm" onclick={() => navigator.clipboard?.writeText(invLink).then(() => toast('Link copied', 'success'))}>Copy</button>{/snippet}
		</Notice>
	{/if}
	{#if invites.length}
		<ul class="mt-3 list-card">
			{#each invites as v (v.id)}
				<li class="flex flex-wrap items-center gap-3 px-3 py-2">
					<div class="min-w-0 flex-1">
						<p>{permNames(v.permissions)}</p>
						<p class="text-small text-muted">Created by {v.created_by} · expires {new Date(v.expires_at_ms).toLocaleString()}</p>
					</div>
					<button class="btn btn-sm btn-danger" onclick={() => revokeInvite(v)}>Revoke</button>
				</li>
			{/each}
		</ul>
	{/if}
</SettingsSection>

<SettingsSection title="Recent changes" description="Who changed files, variables, access and settings of this {noun}. Values are never recorded.">
	{#snippet aside()}<p class="mt-2 text-small"><a class="link" href="/activity?bot={botId}&view=changes">Full history</a></p>{/snippet}
	{#if changes === null}<p class="text-muted">Loading…</p>{:else if changes.length}<ChangeList events={changes} />{:else}<p class="text-muted">Nothing recorded yet.</p>{/if}
</SettingsSection>

<SettingsSection title="Transfer ownership" description="Give this {noun} to another account on the panel, for example when someone leaves the team. It moves into their personal workspace.">
	<form class="flex flex-wrap items-end gap-2 rounded-tile border border-fail/30 bg-fail/5 px-4 py-3" onsubmit={transfer}>
		<label class="block min-w-0 flex-1 basis-64"><span class="label">Email of the new owner</span><input class="field" type="email" required bind:value={transferTo} /></label>
		<button class="btn btn-danger">Transfer…</button>
	</form>
</SettingsSection>
