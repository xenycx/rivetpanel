<script lang="ts">
	import { untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { roleHelp, roleText, type AccountInvite, type NodeInfo, type Role, type Workspace, type WorkspaceQuota } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { inviteRoleName, limitsText } from '$lib/invites';
	import { can, canGrantRole, session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { wsName } from '$lib/workspaces.svelte';
	import ResourcePicker from '$lib/components/ResourcePicker.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	// Creates an account invitation with its setup: the role, limits for the
	// invitee's personal workspace, the node their apps go to and a team
	// workspace to join. The server checks every part again.
	let { open = $bindable(false), roles, oncreated }: { open?: boolean; roles: Role[]; oncreated?: (invite: AccountInvite) => void } = $props();

	type CountKey = 'max_apps' | 'max_bots' | 'max_games' | 'max_web_apps' | 'max_sites';
	type MemberRole = 'admin' | 'developer' | 'viewer';
	const fields: { key: CountKey; label: string }[] = [
		{ key: 'max_bots', label: 'Bots' },
		{ key: 'max_games', label: 'Game servers' },
		{ key: 'max_web_apps', label: 'Web apps' },
		{ key: 'max_apps', label: 'Apps in total' },
		{ key: 'max_sites', label: 'Sites' }
	];
	const memberRoles: MemberRole[] = ['admin', 'developer', 'viewer'];
	const expiries = [1, 3, 7, 14, 30];
	const emptyCounts = (): Record<CountKey, string> => ({ max_apps: '', max_bots: '', max_games: '', max_web_apps: '', max_sites: '' });

	const isAdmin = $derived(session.user?.role === 'admin');
	const pickNode = $derived(can('nodes.manage') && session.features.agents);
	const adminWorkspaces = $derived(can('workspaces.view') || can('workspaces.manage'));

	let email = $state('');
	let days = $state(7);
	let sendEmail = $state(false);
	let role = $state('user');
	let setLimits = $state(false);
	// Number inputs bind numbers, or null/"" when emptied.
	let counts = $state<Record<CountKey, number | string | null>>(emptyCounts());
	let memory = $state<number | null>(null);
	let cpu = $state<number | null>(null);
	let nodeId = $state('');
	let workspaceId = $state('');
	let workspaceRole = $state<MemberRole>('developer');
	let error = $state('');
	let busy = $state(false);
	let result = $state<{ invite: AccountInvite; link: string; emailed: boolean; emailError: string; sentTo: string } | null>(null);

	let nodes = $state<NodeInfo[] | null>(null);
	let nodesError = $state('');
	let teams = $state<Workspace[] | null>(null);
	let teamsError = $state('');

	const fallback = (id: 'user' | 'admin'): Role => ({ id, name: id === 'admin' ? 'Administrator' : 'User', description: '', permissions: [], system: true, editable: id !== 'admin', users: 0, created_at_ms: 0, updated_at_ms: 0 });
	// The built-in User role, the custom roles this account may give, and
	// Administrator for built-in administrators only.
	const roleOptions = $derived.by(() => {
		const user = roles.find((r) => r.id === 'user') ?? fallback('user');
		const custom = roles.filter((r) => !r.system && r.id !== 'user' && r.id !== 'admin' && canGrantRole(r));
		const admin = isAdmin ? [roles.find((r) => r.id === 'admin') ?? fallback('admin')] : [];
		return [user, ...custom, ...admin];
	});
	const roleDescription = (r: Role) =>
		r.description || (r.id === 'admin' ? 'Every permission, including every app and account on this panel.' : r.id === 'user' ? 'The standard account permissions.' : `${r.permissions.length} permission${r.permissions.length === 1 ? '' : 's'}.`);
	// Only roles whose permissions are known can be checked; the built-in User row without them (a failed load) is left to the server.
	const roleAllowed = (r: Role) => r.id === 'admin' ? isAdmin : r.id === 'user' && !roles.some((x) => x.id === 'user') ? true : canGrantRole(r);

	// Roles can arrive after the dialog opened: keep the choice on a role this account may give.
	$effect(() => {
		const opts = roleOptions;
		const cur = untrack(() => role);
		if (opts.some((r) => r.id === cur && roleAllowed(r))) return;
		const first = opts.find(roleAllowed);
		if (first) role = first.id;
	});

	const nodeState = (n: NodeInfo) => (n.transport === 'local' ? 'Local' : n.agent?.connected ? 'Online' : 'Offline');
	const nodeUnavailable = (n: NodeInfo) => !n.enabled || n.draining;
	const nodeLabel = (n: NodeInfo) => `${n.name} · ${nodeState(n)}${!n.enabled ? ' · disabled' : n.draining ? ' · draining' : ''}`;
	const defaultNode = $derived(nodes?.find((n) => n.default)?.name ?? '');
	const teamLabel = (w: Workspace) => (adminWorkspaces && w.owner_email ? `${wsName(w)} · ${w.owner_email}` : wsName(w));

	// Each opening starts a fresh form and refreshes the node and workspace lists.
	let wasOpen = false;
	$effect(() => {
		const o = open;
		if (o && !wasOpen) untrack(start);
		wasOpen = o;
	});
	function start() {
		email = '';
		days = 7;
		sendEmail = false;
		role = roleOptions.find(roleAllowed)?.id ?? 'user';
		setLimits = false;
		counts = emptyCounts();
		memory = cpu = null;
		nodeId = workspaceId = '';
		workspaceRole = 'developer';
		error = '';
		result = null;
		if (pickNode) {
			nodesError = '';
			api<{ nodes: NodeInfo[] }>('GET', '/nodes')
				.then((r) => (nodes = r.nodes))
				.catch((e) => (nodesError = e instanceof ApiError ? e.message : 'The nodes could not be loaded.'));
		}
		if (session.features.workspaces) {
			teamsError = '';
			const admin = adminWorkspaces;
			api<{ workspaces: Workspace[] }>('GET', admin ? '/admin/workspaces' : '/workspaces')
				.then((r) => (teams = r.workspaces.filter((w) => !w.personal && (admin || w.role === 'owner' || w.role === 'admin'))))
				.catch((e) => (teamsError = e instanceof ApiError ? e.message : 'The workspaces could not be loaded.'));
		}
	}

	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));
	function quotaBody(): WorkspaceQuota | null | string {
		if (!isAdmin || !setLimits) return null;
		const q = Object.fromEntries(fields.map((f) => [f.key, num(counts[f.key])])) as Record<CountKey, number | null>;
		if (Object.values(q).some((n) => n != null && (!Number.isInteger(n) || n < 0))) return 'Limits must be whole numbers of 0 or more; leave a field empty for the panel default.';
		const full: WorkspaceQuota = { ...q, memory_bytes: memory, nano_cpus: cpu };
		return Object.values(full).every((n) => n == null) ? null : full;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		const quota = quotaBody();
		if (typeof quota === 'string') {
			error = quota;
			return;
		}
		const to = email.trim();
		const send = session.features.mail && sendEmail && !!to;
		busy = true;
		try {
			const r = await api<{ invite: AccountInvite; path: string; emailed: boolean; email_error: string }>('POST', '/admin/account-invites', {
				email: to,
				role,
				expires_in_days: days,
				send_email: send,
				quota,
				node_id: pickNode ? nodeId : '',
				workspace_id: workspaceId,
				workspace_role: workspaceId ? workspaceRole : ''
			});
			result = { invite: r.invite, link: location.origin + r.path, emailed: r.emailed, emailError: r.email_error, sentTo: to };
			toast(r.emailed ? 'Invitation created and emailed' : 'Invitation created', 'success');
			oncreated?.(r.invite);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The invitation could not be created.';
		} finally {
			busy = false;
		}
	}

	async function copy() {
		if (!result) return;
		try {
			await navigator.clipboard.writeText(result.link);
			toast('Invitation link copied');
		} catch {
			toast('The link could not be copied; select it and copy it yourself.', 'fail');
		}
	}

	const summary = $derived.by(() => {
		const v = result?.invite;
		if (!v) return [];
		const limits = limitsText(v.quota);
		return [
			{ label: 'Who', value: v.email || 'Anyone with the link (single use)' },
			{ label: 'Role', value: inviteRoleName(v) },
			{ label: 'Limits', value: limits ? `${limits} for their personal workspace` : 'Panel defaults' },
			...(session.features.agents ? [{ label: 'Node', value: v.node_id ? v.node_name || 'A pinned node' : 'Panel default' }] : []),
			{ label: 'Team workspace', value: v.workspace_id ? `${v.workspace_name || 'A team workspace'} as ${v.workspace_role ? roleText[v.workspace_role] : 'member'}` : 'None' },
			{ label: 'Expires', value: fmtWhen(v.expires_at_ms) }
		];
	});
</script>

<Dialog bind:open title={result ? 'Invitation created' : 'Invite a user'} size="lg">
	{#if result}
		<div class="grid gap-4">
			{#if result.emailed}
				<Notice tone="success">We emailed the link to {result.sentTo}. It is also here in case you need it.</Notice>
			{:else if result.emailError}
				<Notice tone="warn">The email was not sent: {result.emailError}. Send the link yourself.</Notice>
			{:else}
				<p class="text-muted">This link is shown once. Send it to the person you want to invite.</p>
			{/if}
			<div class="flex flex-col gap-2 sm:flex-row">
				<label class="min-w-0 flex-1"><span class="sr-only">Invitation link</span><input class="field font-mono text-small" readonly value={result.link} onfocus={(e) => e.currentTarget.select()} /></label>
				<button class="btn" type="button" onclick={copy}><Icon name="copy" />Copy link</button>
			</div>
			<section aria-labelledby="invite-gets-h">
				<h3 id="invite-gets-h" class="font-semibold">What they get</h3>
				<dl class="mt-2 grid gap-x-4 gap-y-1.5 text-small sm:grid-cols-[max-content_1fr]">
					{#each summary as s (s.label)}
						<dt class="text-muted">{s.label}</dt>
						<dd class="mb-1.5 min-w-0 break-words sm:mb-0">{s.value}</dd>
					{/each}
				</dl>
			</section>
		</div>
	{:else}
		<form id="user-invite" class="grid gap-6" onsubmit={submit}>
			<fieldset class="grid gap-4">
				<legend class="mb-2 font-semibold">Who and for how long</legend>
				<label class="block">
					<span class="label">Email (optional)</span>
					<input class="field" type="email" bind:value={email} autocomplete="off" placeholder="name@example.com" />
					<span class="help">Locks the link to this address. Leave it empty for a single-use link anyone can open.</span>
				</label>
				<label class="block sm:max-w-60">
					<span class="label">Expires after</span>
					<select class="field" bind:value={days}>
						{#each expiries as d (d)}<option value={d}>{d} day{d === 1 ? '' : 's'}</option>{/each}
					</select>
				</label>
				{#if session.features.mail}
					<label class="flex items-start gap-2.5">
						<input type="checkbox" class="mt-0.5" bind:checked={sendEmail} disabled={!email.trim()} />
						<span>Email the link to this address<span class="help">{email.trim() ? 'The link is also shown here afterwards.' : 'Enter an email address above to send the link.'}</span></span>
					</label>
				{/if}
			</fieldset>

			<fieldset class="grid gap-2">
				<legend class="mb-2 font-semibold">Role</legend>
				{#each roleOptions as r (r.id)}
					{@const allowed = roleAllowed(r)}
					<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft px-3 py-2 {allowed ? '' : 'opacity-60'}">
						<input type="radio" name="invite-role" class="mt-1" value={r.id} bind:group={role} disabled={!allowed} />
						<span class="min-w-0">{r.name}{#if r.system}<span class="pill ml-2" data-tone="idle">Built in</span>{/if}<span class="help">{roleDescription(r)}{allowed ? '' : ' Holds permissions you do not have.'}</span></span>
					</label>
				{/each}
				<p class="help">Custom roles are managed under Roles. You can only give roles whose permissions you hold.</p>
			</fieldset>

			{#if isAdmin}
				<fieldset class="grid gap-4">
					<legend class="mb-2 font-semibold">Limits for their personal workspace</legend>
					<Switch label="Set limits" bind:checked={setLimits}>Off: the panel's <a class="link" href="/admin/settings#limits">default limits</a> apply. On: leave a field empty for the default; 0 allows none.</Switch>
					{#if setLimits}
						<div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
							{#each fields as f (f.key)}
								<label class="block">
									<span class="label">{f.label}</span>
									<input class="field font-mono" type="number" min="0" step="1" placeholder="Default" bind:value={counts[f.key]} />
								</label>
							{/each}
						</div>
						<div>
							<span class="label" id="invite-memory-l">Memory</span>
							<ResourcePicker kind="memory" id="invite-memory" labelledby="invite-memory-l" allowEmpty emptyLabel="Panel default" value={memory} onchange={(x) => (memory = x)} />
						</div>
						<div>
							<span class="label" id="invite-cpu-l">CPU</span>
							<ResourcePicker kind="cpu" id="invite-cpu" labelledby="invite-cpu-l" allowEmpty emptyLabel="Panel default" value={cpu} onchange={(x) => (cpu = x)} />
						</div>
					{/if}
				</fieldset>
			{/if}

			{#if pickNode}
				<fieldset class="grid gap-2">
					<legend class="mb-2 font-semibold">Node for their apps</legend>
					{#if nodesError}
						<Notice tone="fail">{nodesError}</Notice>
					{:else}
						<label class="block">
							<span class="label">Node</span>
							<select class="field" bind:value={nodeId} disabled={!nodes}>
								<option value="">Panel default{defaultNode ? ` (${defaultNode})` : ''}</option>
								{#each nodes ?? [] as n (n.id)}<option value={n.id} disabled={nodeUnavailable(n)}>{nodeLabel(n)}</option>{/each}
							</select>
							<span class="help">Their personal workspace is pinned to this node, so new apps they create there go to it.</span>
						</label>
					{/if}
				</fieldset>
			{/if}

			{#if session.features.workspaces}
				<fieldset class="grid gap-4">
					<legend class="mb-2 font-semibold">Team workspace <span class="font-normal text-muted">(optional)</span></legend>
					{#if teamsError}
						<Notice tone="fail">{teamsError}</Notice>
					{:else if teams && !teams.length}
						<p class="text-small text-muted">There are no team workspaces you administer.</p>
					{:else}
						<div class="grid gap-4 sm:grid-cols-2">
							<label class="block">
								<span class="label">Workspace</span>
								<select class="field" bind:value={workspaceId} disabled={!teams}>
									<option value="">None</option>
									{#each teams ?? [] as w (w.id)}<option value={w.id}>{teamLabel(w)}</option>{/each}
								</select>
							</label>
							{#if workspaceId}
								<label class="block">
									<span class="label">Their role there</span>
									<select class="field" bind:value={workspaceRole}>
										{#each memberRoles as m (m)}<option value={m}>{roleText[m]}</option>{/each}
									</select>
									<span class="help">{roleHelp[workspaceRole]}.</span>
								</label>
							{/if}
						</div>
						<p class="help">They join it as a member as soon as the account is created.</p>
					{/if}
				</fieldset>
			{/if}

			{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
		</form>
	{/if}
	{#snippet footer()}
		{#if result}
			<button class="btn" type="button" onclick={start}>Invite someone else</button>
			<button class="btn btn-primary" type="button" onclick={() => (open = false)}>Done</button>
		{:else}
			<button class="btn" type="button" onclick={() => (open = false)}>Cancel</button>
			<button class="btn btn-primary" type="submit" form="user-invite" disabled={busy}>{busy ? 'Creating…' : 'Create invitation'}</button>
		{/if}
	{/snippet}
</Dialog>
