<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import type { Bot } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	type Token = { id: string; name: string; prefix: string; actions: string[]; bot_ids: string[] | null; created_at_ms: number; last_used_at_ms: number | null; expires_at_ms: number };
	let tokens = $state<Token[] | null>(null);
	let bots = $state<Bot[]>([]);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');
	const actionInfo: Record<string, string> = {
		read: 'Read status and history',
		power: 'Start, stop and restart',
		deploy: 'Deploy from GitHub',
		backup: 'Create backups'
	};

	async function load() {
		try {
			tokens = (await api<{ tokens: Token[] }>('GET', '/me/tokens')).tokens;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(async () => {
		await load();
		try {
			const r = await api<{ bots: Bot[] } | Bot[]>('GET', '/bots');
			bots = Array.isArray(r) ? r : r.bots;
		} catch {
			/* bot names are a convenience */
		}
	});
	const botName = (id: string) => bots.find((b) => b.id === id)?.name ?? id.slice(0, 8);

	let open = $state(false);
	let name = $state('');
	let actions = $state<string[]>(['read']);
	let allBots = $state(true);
	let chosen = $state<string[]>([]);
	let days = $state(90);
	let created = $state('');
	let busy = $state(false);
	let formError = $state('');

	function start() {
		name = '';
		actions = ['read'];
		allBots = true;
		chosen = [];
		days = 90;
		created = formError = '';
		open = true;
	}
	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			const r = await api<{ token: string }>('POST', '/me/tokens', { name, actions, bot_ids: allBots ? null : chosen, expires_in_days: days });
			created = r.token;
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function revoke(t: Token) {
		const ok = await confirmDialog({ title: `Revoke “${t.name}”?`, body: 'Scripts using it fail from the next request.', confirmLabel: 'Revoke token', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/me/tokens/${t.id}`);
			toast('Token revoked');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const origin = typeof location === 'undefined' ? '' : location.origin;
</script>

<SettingsSection title="Automation tokens" description="For scripts and CI: check status, start and stop bots, deploy and back up over HTTP. A token can do only what you choose and what your account can do at the time.">
{#if error}<Notice tone="fail" class="mb-3">{error}</Notice>{/if}
<div class="card overflow-hidden">
<div class="flex items-center justify-between gap-2 border-b border-rule-soft px-4 py-2.5">
	<p class="text-small text-muted">{tokens ? `${tokens.length} token${tokens.length === 1 ? '' : 's'}` : 'Loading…'}</p>
	<button class="btn btn-sm btn-primary" onclick={start}><Icon name="plus" size={14} />New token</button>
</div>
<ul class="divide-y divide-rule-soft">
	{#each tokens ?? [] as t (t.id)}
		<li class="flex flex-wrap items-center gap-3 px-4 py-3">
			<div class="min-w-0 flex-1">
				<p class="font-medium">{t.name} <code class="ml-1 font-mono text-[12px] text-muted">{t.prefix}…</code></p>
				<p class="mt-0.5 flex flex-wrap gap-1">
					{#each t.actions as a (a)}<span class="eyebrow rounded-control border border-rule px-1.5 py-px">{a}</span>{/each}
					<span class="text-small text-muted">· {t.bot_ids ? t.bot_ids.map(botName).join(', ') : 'all your bots'}</span>
				</p>
				<p class="text-small text-muted">{t.last_used_at_ms ? `Used ${fmtAgo(t.last_used_at_ms)}` : 'Never used'} · {t.expires_at_ms < Date.now() ? 'expired' : `expires ${fmtWhen(t.expires_at_ms)}`}</p>
			</div>
			<button class="btn btn-sm btn-danger" onclick={() => revoke(t)}>Revoke</button>
		</li>
	{:else}
		<li class="px-4 py-4 text-small text-muted">{tokens ? 'No automation tokens yet.' : 'Loading…'}</li>
	{/each}
</ul>
</div>
<p class="mt-2 text-small text-muted">The API is described in <a class="link" href="/api/v1/automation/openapi.yaml" target="_blank" rel="noopener">openapi.yaml</a>. SFTP keys never work here, and these tokens never work for SFTP.</p>
</SettingsSection>

<Dialog bind:open title={created ? 'Copy your token' : 'New automation token'} size="md">
	{#if created}
		<p>It is shown only once. Store it as a secret in your CI (for example <code>RIVET_TOKEN</code>).</p>
		<p class="my-3 rounded-control border border-rule bg-paper px-3 py-2 font-mono text-small break-all select-all">{created}</p>
		<p class="eyebrow">Try it</p>
		<pre class="mt-1 overflow-x-auto rounded-control bg-term p-3 font-mono text-small text-term-ink">curl -H "Authorization: Bearer $RIVET_TOKEN" \
  {origin}/api/v1/automation/bots</pre>
	{:else}
		<form id="tok-form" class="grid gap-4" onsubmit={create}>
			<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} placeholder="GitHub Actions deploy" /></label>
			<fieldset>
				<legend class="label">Allowed actions</legend>
				<div class="grid gap-1.5 sm:grid-cols-2">
					{#each Object.entries(actionInfo) as [a, label] (a)}
						<label class="flex items-center gap-2.5 rounded-control border px-3 py-2 {actions.includes(a) ? 'border-action' : 'border-rule'}">
							<input type="checkbox" value={a} bind:group={actions} /><span><span class="font-mono text-small">{a}</span><span class="help mt-0">{label}</span></span>
						</label>
					{/each}
				</div>
			</fieldset>
			<fieldset>
				<legend class="label">Bots</legend>
				<label class="flex items-center gap-2.5"><input type="radio" name="scope" value={true} bind:group={allBots} />All bots you can access, now and later</label>
				<label class="mt-1 flex items-center gap-2.5"><input type="radio" name="scope" value={false} bind:group={allBots} />Only these bots</label>
				{#if !allBots}
					<div class="mt-2 grid max-h-44 gap-1 overflow-y-auto rounded-control border border-rule-soft p-2">
						{#each bots as b (b.id)}<label class="flex items-center gap-2"><input type="checkbox" value={b.id} bind:group={chosen} />{b.name}</label>{:else}<p class="text-muted">No bots.</p>{/each}
					</div>
				{/if}
			</fieldset>
			<label class="block">
				<span class="label">Expires after</span>
				<select class="field" bind:value={days}>{#each [7, 30, 90, 180, 366] as d (d)}<option value={d}>{d === 366 ? '1 year' : `${d} days`}</option>{/each}</select>
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
			<button class="btn btn-primary" type="submit" form="tok-form" disabled={busy || !actions.length || (!allBots && !chosen.length)}>Create token</button>
		{/if}
	{/snippet}
</Dialog>
