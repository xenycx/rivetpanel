<script lang="ts">
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Site, SiteBackup, SiteBackupHealth } from '$lib/api/types';
	import { fmtAgo, fmtDuration, fmtWhen } from '$lib/args';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	// Backups of a site made from uploaded or edited files: copies of the
	// published files kept apart from its releases, on the panel's schedule
	// (unless switched off) or on demand. Restoring publishes a backup as a
	// new release.
	let { site, canEdit, canAdmin, onChanged }: { site: Site; canEdit: boolean; canAdmin: boolean; onChanged: () => void } = $props();

	let backups = $state<SiteBackup[] | null>(null);
	let health = $state<SiteBackupHealth | null>(null);
	let error = $state('');
	let busy = $state('');
	let now = $state(Date.now());
	const path = $derived(`/sites/${site.id}/backups`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			const r = await api<{ backups: SiteBackup[]; health: SiteBackupHealth }>('GET', path);
			backups = r.backups;
			health = r.health;
			error = '';
		} catch (e) {
			error = msg(e);
		}
		now = Date.now();
	}
	// Loads now, and again when a new release changes what is published.
	$effect(() => {
		void site.current_release;
		load();
	});

	async function create() {
		busy = 'create';
		try {
			await api('POST', path, {});
			toast('Backup created', 'success');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			busy = '';
		}
	}
	async function restore(b: SiteBackup) {
		const ok = await confirmDialog({
			title: 'Restore this backup?',
			body: `The files backed up ${fmtWhen(b.created_at_ms)} are published as a new release and visitors get them right away. The release served now stays under Releases, so you can switch back.`,
			confirmLabel: 'Restore'
		});
		if (!ok) return;
		busy = b.id;
		try {
			await api('POST', `${path}/${b.id}/restore`);
			toast('Backup restored as a new release', 'success');
			onChanged();
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			busy = '';
		}
	}
	async function remove(b: SiteBackup) {
		const ok = await confirmDialog({ title: 'Delete this backup?', body: `The backup of ${fmtWhen(b.created_at_ms)} is deleted. This cannot be undone.`, confirmLabel: 'Delete backup', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `${path}/${b.id}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}

	// Automatic backups are on by default; turning them off keeps existing
	// backups and manual ones.
	let autoOn = $state(true);
	let autoSaving = $state(false);
	$effect(() => {
		autoOn = site.auto_backup;
	});
	async function toggleAuto() {
		const on = autoOn;
		if (!on) {
			const ok = await confirmDialog({
				title: 'Turn off automatic backups?',
				body: 'This site is no longer backed up on the panel’s schedule. Existing backups are kept, and you can still create backups yourself.',
				confirmLabel: 'Turn off'
			});
			if (!ok) {
				autoOn = true;
				return;
			}
		}
		autoSaving = true;
		try {
			await api('PUT', `/sites/${site.id}/auto-backup`, { enabled: on });
			toast(on ? 'Automatic backups turned on' : 'Automatic backups turned off', 'success');
			onChanged();
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
			autoOn = !on;
		} finally {
			autoSaving = false;
		}
	}

	function menu(b: SiteBackup): MenuItem[] {
		return [{ label: 'Delete', danger: true, onselect: () => remove(b) }];
	}
</script>

{#snippet autoDetail()}
	{#if !health?.enabled}
		Not backed up on a schedule. Manual backups still work.
	{:else if !site.current_release}
		Starts once something is published.
	{:else if health.up_to_date}
		Every {fmtDuration(health.interval_ms)} when the site changed, keeps {health.keep}. The published files are backed up.
	{:else}
		Every {fmtDuration(health.interval_ms)} when the site changed, keeps {health.keep}. {health.next_due_ms <= now ? 'Next one is due now.' : `Next at ${fmtWhen(health.next_due_ms)}.`}
	{/if}
{/snippet}

<section class="card p-5" aria-labelledby="sbk-h">
	<div class="flex flex-wrap items-start justify-between gap-3">
		<div class="min-w-0">
			<h2 id="sbk-h" class="text-title font-semibold">Backups</h2>
			<p class="mt-1 max-w-measure text-small text-muted">Copies of the published files, kept apart from releases. Restoring publishes a backup as a new release.</p>
		</div>
		{#if canEdit && health?.available}
			<button class="btn btn-sm" disabled={!!busy || !site.current_release} title={site.current_release ? '' : 'Publish the site first'} onclick={create}><Icon name="plus" size={13} />{busy === 'create' ? 'Creating…' : 'Create backup'}</button>
		{/if}
	</div>

	{#if error}
		<Notice tone="fail" class="mt-4" live>{error}</Notice>
	{:else if health && !health.available}
		<p class="mt-3 text-small text-muted">{health.reason}</p>
	{:else if health}
		<div class="mt-4 grid gap-3 sm:grid-cols-3">
			<div class="stat">
				<p class="text-small text-muted">Last backup</p>
				<p class="font-medium">{health.last_success_ms ? fmtAgo(health.last_success_ms, now) : 'None yet'}</p>
				{#if health.last_success_ms}<p class="text-small text-muted">{fmtWhen(health.last_success_ms)}</p>{/if}
			</div>
			<div class="stat">
				<p class="mb-1 text-small text-muted">Automatic backups</p>
				{#if !health.interval_ms}
					<p class="font-medium">Off on this panel</p>
					<p class="text-small text-muted">An administrator can turn them on with <code>RIVET_BACKUP_INTERVAL</code>.</p>
				{:else if canAdmin}
					<Switch bind:checked={autoOn} disabled={autoSaving} label={autoOn ? 'On' : 'Off'} onchange={toggleAuto}>{@render autoDetail()}</Switch>
				{:else}
					<p class="font-medium">{health.enabled ? 'On' : 'Off'}</p>
					<p class="text-small text-muted">{@render autoDetail()}</p>
				{/if}
			</div>
			<div class="stat">
				<p class="text-small text-muted">Stored</p>
				<p class="font-medium">{fmtBytes(health.total_bytes)}</p>
				<p class="text-small text-muted">{health.count} {health.count === 1 ? 'backup' : 'backups'}</p>
			</div>
		</div>

		{#if backups?.length}
			<ul class="list-card mt-4">
				{#each backups as b (b.id)}
					<li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
						<div class="min-w-0 flex-1 basis-52">
							<p class="font-medium">{fmtWhen(b.created_at_ms)}</p>
							<p class="truncate text-small text-muted">{b.label ? b.label + ' · ' : ''}{b.kind === 'auto' ? 'Scheduled' : 'Manual'} · {b.files} {b.files === 1 ? 'file' : 'files'} · {fmtBytes(b.bytes)}{b.release_id === site.current_release ? ' · same as the published files' : ''}</p>
						</div>
						{#if canEdit}
							<a class="btn btn-sm" href="/api/v1{path}/{b.id}/download" download><Icon name="download" size={13} />Download</a>
							<button class="btn btn-sm" disabled={!!busy || b.release_id === site.current_release} title={b.release_id === site.current_release ? 'This backup holds the files served now' : ''} onclick={() => restore(b)}>{busy === b.id ? 'Restoring…' : 'Restore…'}</button>
						{/if}
						{#if canAdmin}<Menu label="More actions for this backup" items={menu(b)} />{/if}
					</li>
				{/each}
			</ul>
		{:else}
			<p class="rows-empty mt-4 rounded-tile border border-rule-soft">{site.current_release ? 'No backups yet. The schedule makes the first one soon; you can also create one now.' : 'Nothing is published yet, so there is nothing to back up.'}</p>
		{/if}
		<p class="mt-3 text-small text-muted">Manual backups: up to {health.manual_limit}. Archives are kept on this panel, apart from the site, and removed with it.</p>
	{/if}
</section>
