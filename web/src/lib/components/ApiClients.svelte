<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import type { ApiClient, Bot, PermissionInfo, Workspace } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	// API clients act as this account through the whole API, limited to the
	// permissions chosen here (never more than the account holds; the server
	// re-checks the account's role on every request) and optionally to some
	// bots and workspaces.
	let clients = $state<ApiClient[] | null>(null);
	let grantable = $state<PermissionInfo[]>([]);
	let bots = $state<Bot[]>([]);
	let workspaces = $state<Workspace[]>([]);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load() {
		try {
			const r = await api<{ clients: ApiClient[]; grantable: PermissionInfo[] }>('GET', '/me/api-clients');
			clients = r.clients;
			grantable = r.grantable;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(async () => {
		await load();
		try {
			const [b, w] = await Promise.all([api<{ bots: Bot[] } | Bot[]>('GET', '/bots'), api<{ workspaces: Workspace[] }>('GET', '/workspaces')]);
			bots = Array.isArray(b) ? b : b.bots;
			workspaces = w.workspaces;
		} catch {
			/* names are a convenience */
		}
	});
	const botName = (id: string) => bots.find((b) => b.id === id)?.name ?? id.slice(0, 8);
	const wsName = (id: string) => workspaces.find((w) => w.id === id)?.name ?? id.slice(0, 8);
	const permLabel = (p: string) => grantable.find((x) => x.name === p)?.label ?? p;
	function scopeText(c: ApiClient) {
		if (!c.bot_ids && !c.workspace_ids) return 'everything the account can reach';
		return [...(c.bot_ids ?? []).map(botName), ...(c.workspace_ids ?? []).map((w) => `workspace ${wsName(w)}`)].join(', ');
	}

	let open = $state(false);
	let name = $state('');
	let perms = $state<string[]>([]);
	let limited = $state(false);
	let chosenBots = $state<string[]>([]);
	let chosenWs = $state<string[]>([]);
	let days = $state(90);
	let created = $state('');
	let busy = $state(false);
	let formError = $state('');

	function start() {
		name = '';
		perms = grantable.filter((p) => p.name === 'bots.console' || p.name === 'bots.power').map((p) => p.name);
		limited = false;
		chosenBots = [];
		chosenWs = [];
		days = 90;
		created = formError = '';
		open = true;
	}
	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			const r = await api<{ token: string }>('POST', '/me/api-clients', {
				name,
				permissions: grantable.map((p) => p.name).filter((p) => perms.includes(p)),
				bot_ids: limited ? chosenBots : null,
				workspace_ids: limited ? chosenWs : null,
				expires_in_days: days
			});
			created = r.token;
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function revoke(c: ApiClient) {
		const ok = await confirmDialog({ title: `Revoke “${c.name}”?`, body: 'Programs using it are refused from their next request.', confirmLabel: 'Revoke client', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/me/api-clients/${c.id}`);
			toast('API client revoked');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const origin = typeof location === 'undefined' ? '' : location.origin;
	const groups = [
		{ id: 'resources', title: 'Bots, servers and sites' },
		{ id: 'administration', title: 'Administration' }
	] as const;
</script>

<SettingsSection title="API clients" description="For programs that need more than the automation API: an API client uses the same API as this panel, with only the permissions you choose. It never holds more than your account does at the time, never acts as an administrator, and cannot manage your password, sessions or keys.">
{#if error}<Notice tone="fail" class="mb-3">{error}</Notice>{/if}
<div class="card overflow-hidden">
<div class="flex items-center justify-between gap-2 border-b border-rule-soft px-4 py-2.5">
	<p class="text-small text-muted">{clients ? `${clients.length} client${clients.length === 1 ? '' : 's'}` : 'Loading…'}</p>
	<button class="btn btn-sm btn-primary" onclick={start} disabled={!grantable.length}><Icon name="plus" size={14} />New API client</button>
</div>
<ul class="divide-y divide-rule-soft">
	{#each clients ?? [] as c (c.id)}
		{@const expired = c.expires_at_ms !== null && c.expires_at_ms < Date.now()}
		<li class="flex flex-wrap items-center gap-3 px-4 py-3">
			<div class="min-w-0 flex-1">
				<p class="font-medium">{c.name} <code class="ml-1 font-mono text-[12px] text-muted">{c.prefix}…</code></p>
				<p class="mt-0.5 flex flex-wrap gap-1">
					{#each c.permissions as p (p)}<span class="eyebrow rounded-control border border-rule px-1.5 py-px" title={permLabel(p)}>{p}</span>{/each}
				</p>
				<p class="text-small text-muted">Limited to {scopeText(c)}</p>
				<p class="text-small text-muted">{c.last_used_at_ms ? `Used ${fmtAgo(c.last_used_at_ms)}` : 'Never used'} · {c.expires_at_ms === null ? 'does not expire' : expired ? 'expired' : `expires ${fmtWhen(c.expires_at_ms)}`}</p>
			</div>
			<button class="btn btn-sm btn-danger" onclick={() => revoke(c)}>Revoke</button>
		</li>
	{:else}
		<li class="px-4 py-4 text-small text-muted">{clients ? 'No API clients yet.' : 'Loading…'}</li>
	{/each}
</ul>
</div>
</SettingsSection>

<Dialog bind:open title={created ? 'Copy your API client token' : 'New API client'} size="lg">
	{#if created}
		<p>It is shown only once; only a hash is kept. Store it as a secret.</p>
		<p class="my-3 rounded-control border border-rule bg-paper px-3 py-2 font-mono text-small break-all select-all">{created}</p>
		<p class="eyebrow">Try it</p>
		<pre class="mt-1 overflow-x-auto rounded-control bg-term p-3 font-mono text-small text-term-ink">curl -H "Authorization: Bearer $RIVET_CLIENT" \
  {origin}/api/v1/bots</pre>
	{:else}
		<form id="client-form" class="grid gap-4" onsubmit={create}>
			<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} placeholder="Status dashboard" /></label>
			{#each groups as g (g.id)}
				{@const items = grantable.filter((p) => p.group === g.id)}
				{#if items.length}
					<fieldset>
						<legend class="label">{g.title}</legend>
						<div class="grid gap-1.5 sm:grid-cols-2">
							{#each items as p (p.name)}
								<label class="flex items-start gap-2.5 rounded-control border px-3 py-2 {perms.includes(p.name) ? 'border-action' : 'border-rule'}">
									<input type="checkbox" class="mt-0.5" value={p.name} bind:group={perms} /><span>{p.label}<span class="help mt-0">{p.description}</span></span>
								</label>
							{/each}
						</div>
					</fieldset>
				{/if}
			{/each}
			<p class="help mt-0">Only permissions your account holds are offered. If your role later loses one, the client loses it too.</p>
			<fieldset>
				<legend class="label">Reach</legend>
				<label class="flex items-center gap-2.5"><input type="radio" name="reach" value={false} bind:group={limited} />Everything your account can reach</label>
				<label class="mt-1 flex items-center gap-2.5"><input type="radio" name="reach" value={true} bind:group={limited} />Only these bots and workspaces</label>
				{#if limited}
					<p class="help">A limited client is refused (403) everywhere else, including the administration area and sites.</p>
					<div class="mt-2 grid gap-3 sm:grid-cols-2">
						<div class="grid max-h-44 content-start gap-1 overflow-y-auto rounded-control border border-rule-soft p-2">
							<p class="eyebrow">Bots and servers</p>
							{#each bots as b (b.id)}<label class="flex items-center gap-2"><input type="checkbox" value={b.id} bind:group={chosenBots} />{b.name}</label>{:else}<p class="text-muted">No bots.</p>{/each}
						</div>
						<div class="grid max-h-44 content-start gap-1 overflow-y-auto rounded-control border border-rule-soft p-2">
							<p class="eyebrow">Workspaces (every bot in them)</p>
							{#each workspaces as w (w.id)}<label class="flex items-center gap-2"><input type="checkbox" value={w.id} bind:group={chosenWs} />{w.name}</label>{:else}<p class="text-muted">No workspaces.</p>{/each}
						</div>
					</div>
				{/if}
			</fieldset>
			<label class="block">
				<span class="label">Expires after</span>
				<select class="field" bind:value={days}>{#each [7, 30, 90, 180, 366, 0] as d (d)}<option value={d}>{d === 0 ? 'Never' : d === 366 ? '1 year' : `${d} days`}</option>{/each}</select>
			</label>
			{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
		</form>
	{/if}
	{#snippet footer()}
		{#if created}
			<button class="btn" onclick={() => navigator.clipboard?.writeText(created).then(() => toast('Token copied', 'success'))}><Icon name="copy" size={14} />Copy</button>
			<button class="btn btn-primary" onclick={() => (open = false)}>Done</button>
		{:else}
			<button class="btn" onclick={() => (open = false)}>Cancel</button>
			<button class="btn btn-primary" type="submit" form="client-form" disabled={busy || !perms.length || (limited && !chosenBots.length && !chosenWs.length)}>Create client</button>
		{/if}
	{/snippet}
</Dialog>
