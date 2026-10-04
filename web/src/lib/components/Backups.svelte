<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { can, Perm, type Backup, type BackupHealth, type Bot } from '$lib/api/types';
	import { fmtAgo, fmtDuration, fmtWhen } from '$lib/args';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot, stopped, admin, onSaved }: { bot: Bot; stopped: boolean; admin: boolean; onSaved: (b: Bot) => void } = $props();

	let backups = $state<Backup[] | null>(null);
	let health = $state<BackupHealth | null>(null);
	let error = $state('');
	let kind = $state<'' | Backup['kind']>('');
	let now = $state(Date.now());

	let createOpen = $state(false);
	let label = $state('');
	let includeEnv = $state(true);
	let consistent = $state(false);
	let creating = $state(false);

	let restoreOf = $state<Backup | null>(null);
	let restoreOpen = $state(false);
	let restoreEnv = $state(true);
	let restoring = $state(false);

	const path = $derived(`/bots/${bot.id}/backups`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const kindLabel = { manual: 'Manual', auto: 'Scheduled', pre_restore: 'Safety copy before a restore' } as const;
	const running = $derived(bot.desired_state === 'running');
	const isGame = $derived(bot.kind === 'game');
	const noun = $derived(isGame ? 'server' : 'bot');
	// A game server's startup variables are stored as its environment.
	const varsWord = $derived(isGame ? 'server settings' : 'environment variables');

	async function load() {
		try {
			const r = await api<{ backups: Backup[]; health: BackupHealth }>('GET', path);
			backups = r.backups;
			health = r.health;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible' && backups?.some((b) => b.status === 'creating')) load();
		}, 1500);
		return () => clearInterval(t);
	});

	const shown = $derived((backups ?? []).filter((b) => !kind || b.kind === kind));
	const counts = $derived({
		manual: backups?.filter((b) => b.kind === 'manual').length ?? 0,
		auto: backups?.filter((b) => b.kind === 'auto').length ?? 0,
		pre_restore: backups?.filter((b) => b.kind === 'pre_restore').length ?? 0
	});

	function openCreate() {
		label = '';
		includeEnv = true;
		consistent = false;
		createOpen = true;
	}
	async function create(e: SubmitEvent) {
		e.preventDefault();
		creating = true;
		try {
			await api('POST', path, { include_env: includeEnv, label, consistent });
			createOpen = false;
			toast(consistent ? `Stopping the ${noun} and creating a backup` : 'Creating a backup');
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		} finally {
			creating = false;
		}
	}

	function openRestore(b: Backup) {
		restoreOf = b;
		restoreEnv = b.includes_env;
		restoreOpen = true;
	}
	async function restore() {
		if (!restoreOf) return;
		restoring = true;
		try {
			onSaved(await api<Bot>('POST', `${path}/${restoreOf.id}/restore`, { restore_env: restoreEnv && restoreOf.includes_env }));
			restoreOpen = false;
			toast(`Backup restored. Start the ${noun} to use it.`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			restoring = false;
		}
	}

	async function relabel(b: Backup) {
		const v = await promptDialog({ title: 'Backup label', label: 'Label', value: b.label ?? '', placeholder: 'Before the v2 upgrade', confirmLabel: 'Save label', validate: (x) => (x.length > 80 ? 'Use at most 80 characters.' : null) });
		if (v === null) return;
		try {
			await api('PATCH', `${path}/${b.id}`, { label: v });
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function verify(b: Backup) {
		try {
			const r = await api<Backup>('POST', `${path}/${b.id}/verify`);
			toast(r.verify_error ? `Verification failed: ${r.verify_error}` : 'The backup is intact', r.verify_error ? 'fail' : 'success');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(b: Backup) {
		const ok = await confirmDialog({
			title: 'Delete this backup?',
			body: 'The archive file is deleted permanently.',
			details: [
				['Created', fmtWhen(b.created_at_ms)],
				...(b.label ? [['Label', b.label] as [string, string]] : []),
				['Size', fmtBytes(b.size_bytes)]
			],
			confirmLabel: 'Delete backup',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `${path}/${b.id}`);
			toast('Backup deleted');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function toggleAuto(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const on = input.checked;
		try {
			onSaved(await api<Bot>('PATCH', `/bots/${bot.id}`, { auto_backup: on }));
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
			input.checked = !on;
		}
	}

	function menu(b: Backup): MenuItem[] {
		const items: MenuItem[] = [];
		if (b.status === 'ready') items.push({ label: 'Verify integrity', onselect: () => verify(b), hint: b.verified_at_ms ? `Last checked ${fmtAgo(b.verified_at_ms)}` : undefined });
		if (admin) items.push({ label: 'Edit label', onselect: () => relabel(b) });
		if (admin && b.status !== 'creating') items.push('separator', { label: 'Delete', danger: true, onselect: () => remove(b) });
		return items;
	}
</script>

<!-- Protection first: when the last good copy was made and when the next one is due. -->
<section aria-label="Backup health" class="grid gap-3 sm:grid-cols-3">
	<div class="stat">
		<p class="text-small text-muted">Last successful backup</p>
		<p class="font-medium">{health?.last_success_ms ? fmtAgo(health.last_success_ms, now) : 'None yet'}</p>
		{#if health?.last_success_ms}<p class="text-small text-muted">{fmtWhen(health.last_success_ms)}</p>{/if}
	</div>
	<div class="stat">
		<p class="text-small text-muted">Schedule</p>
		{#if !health}
			<p class="text-muted">…</p>
		{:else if !health.interval_ms}
			<p class="font-medium">Off on this panel</p>
			<p class="text-small text-muted">An administrator can turn on scheduled backups.</p>
		{:else if !health.enabled}
			<p class="font-medium">Off for this {noun}</p>
		{:else}
			<p class="font-medium">Every {fmtDuration(health.interval_ms)}, keeps {health.keep}</p>
			<p class="text-small text-muted">{health.next_due_ms <= now ? 'Next one is due now.' : `Next at ${fmtWhen(health.next_due_ms)}`}</p>
		{/if}
	</div>
	<div class="stat">
		<p class="text-small text-muted">Stored</p>
		<p class="font-medium">{health ? fmtBytes(health.total_bytes) : '…'}</p>
		{#if health}<p class="text-small text-muted">{health.count} of {health.limit} backups</p>{/if}
	</div>
</section>

{#if health?.last_failure}
	<Notice tone="fail" class="mt-3" title="The latest backup failed">{health.last_failure.error ?? 'Unknown error.'} Try again, or check free disk space on the host.</Notice>
{/if}

<div class="mt-4 flex flex-wrap items-center gap-2">
	<button class="btn btn-primary" onclick={openCreate}><Icon name="plus" />Create backup</button>
	{#if admin && health?.interval_ms}
		<label class="ml-1 flex items-center gap-2"><input type="checkbox" checked={bot.auto_backup} onchange={toggleAuto} />Include in scheduled backups</label>
	{/if}
	<span class="flex-1"></span>
	{#if backups?.length}
		<select class="field w-auto" bind:value={kind} aria-label="Show">
			<option value="">All backups ({backups.length})</option>
			<option value="manual">Manual ({counts.manual})</option>
			<option value="auto">Scheduled ({counts.auto})</option>
			<option value="pre_restore">Safety copies ({counts.pre_restore})</option>
		</select>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-3">{error}</Notice>{/if}
{#if admin && !stopped}<p class="mt-3 text-small text-muted">Restoring needs a stopped bot. Creating and downloading backups works while it runs.</p>{/if}

<div class="mt-3">
	{#if backups === null && !error}
		<Skeleton rows={3} label="Loading backups" />
	{:else if backups}
		<ul class="list-card">
			{#each shown as b (b.id)}
				<li class="spine flex flex-wrap items-center gap-x-4 gap-y-2 py-3 pr-3 pl-5" data-tone={b.status === 'failed' ? 'fail' : b.status === 'creating' ? 'warn' : b.verify_error ? 'fail' : 'idle'} data-busy={b.status === 'creating'}>
					<div class="min-w-0 flex-1 basis-64">
						<p class="font-medium">{b.label ?? fmtWhen(b.created_at_ms)}</p>
						<p class="text-small text-muted">
							{kindLabel[b.kind]}{#if b.label}, {fmtWhen(b.created_at_ms)}{/if}.
							{#if b.status === 'creating'}Creating now.{:else if b.status === 'failed'}<span class="text-fail">Failed: {b.error ?? 'unknown error'}</span>{:else}{fmtBytes(b.size_bytes)}, {b.includes_env ? 'files and environment' : 'files only'}{b.consistent ? ', taken while stopped' : ''}.{/if}
						</p>
						{#if b.verify_error}<p class="text-small text-fail">Last check failed: {b.verify_error}</p>{:else if b.verified_at_ms}<p class="text-small text-run">Verified {fmtAgo(b.verified_at_ms, now)}</p>{/if}
					</div>
					<div class="flex items-center gap-1">
						{#if b.status === 'ready'}
							<a class="btn btn-sm" href="/api/v1{path}/{b.id}/download" download><Icon name="download" size={14} />Download</a>
							{#if admin}<button class="btn btn-sm" disabled={!stopped || !!b.verify_error} onclick={() => openRestore(b)} title={stopped ? '' : `Stop the ${noun} first`}>Restore…</button>{/if}
						{/if}
						{#if menu(b).length}<Menu label="More actions for this backup" items={menu(b)} />{/if}
					</div>
				</li>
			{:else}
				<li class="px-4 py-6 text-muted">{kind ? 'No backups of this kind.' : 'No backups yet. Create one before risky changes; scheduled backups appear here automatically.'}</li>
			{/each}
		</ul>
	{/if}
</div>
{#if isGame}
	<p class="mt-4 max-w-prose text-small text-muted">A backup holds the server's files (worlds, configuration, mods and plugins) and optionally its server settings (the startup variables such as the version), sealed with this panel's key and this server's ID. A downloaded archive does not expose the settings, and they can only be restored to this server on this panel.</p>
{:else}
	<p class="mt-4 max-w-prose text-small text-muted">A backup holds the bot's files without rebuildable folders such as <code>node_modules</code>, and optionally its environment variables, sealed with this server's key and this bot's ID. A downloaded archive does not expose them, and they can only be restored to this bot on this server.</p>
{/if}

<Dialog bind:open={createOpen} title="Create a backup" size="sm">
	<form id="bk-create" class="grid gap-4" onsubmit={create}>
		<label class="block"><span class="label">Label <span class="font-normal text-muted">(optional)</span></span><input class="field" maxlength="80" bind:value={label} placeholder="Before the v2 upgrade" /></label>
		<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={includeEnv} /><span>Include {varsWord}<span class="help">Stored encrypted; restorable only to this {noun} on this panel.</span></span></label>
		{#if running && can(bot, Perm.power)}
			<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={consistent} /><span>Stop the {noun} while copying<span class="help">Gives a consistent copy of files the {noun} is writing{isGame ? ', such as the world' : ''}. The {noun} is started again afterwards unless someone stops or starts it meanwhile.</span></span></label>
		{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (createOpen = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="bk-create" disabled={creating}>{consistent ? 'Stop and back up' : 'Create backup'}</button>
	{/snippet}
</Dialog>

<Dialog bind:open={restoreOpen} title="Restore this backup?" size="md">
	{#if restoreOf}
		<p>The {noun}'s current files are replaced with the files in this backup. Files that were not in the backup are removed{isGame ? '.' : '; rebuildable folders are rebuilt on the next start.'}</p>
		<dl class="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 border-y border-rule-soft py-2">
			<dt class="text-muted">Backup</dt><dd>{restoreOf.label ?? kindLabel[restoreOf.kind]}</dd>
			<dt class="text-muted">Created</dt><dd>{fmtWhen(restoreOf.created_at_ms)} ({fmtAgo(restoreOf.created_at_ms, now)})</dd>
			<dt class="text-muted">Size</dt><dd>{fmtBytes(restoreOf.size_bytes)}</dd>
		</dl>
		{#if restoreOf.includes_env}
			<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={restoreEnv} /><span>Also restore {varsWord}<span class="help">Replaces all current {isGame ? 'settings' : 'variables'} with the ones saved in this backup.</span></span></label>
		{:else}
			<p class="text-small text-muted">This backup has no {varsWord}; the current ones stay.</p>
		{/if}
		<Notice class="mt-3">A safety copy of the current files{restoreEnv && restoreOf.includes_env ? (isGame ? ' and settings' : ' and variables') : ''} is saved first, so you can undo this restore.</Notice>
	{/if}
	{#snippet footer()}
		<button class="btn" onclick={() => (restoreOpen = false)} disabled={restoring}>Cancel</button>
		<button class="btn btn-primary" onclick={restore} disabled={restoring}>{restoring ? 'Restoring…' : 'Restore backup'}</button>
	{/snippet}
</Dialog>
