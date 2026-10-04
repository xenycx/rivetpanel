<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot, Connection, GitHubRepo, OpPage, Operation, RepoLink, RepoLookup } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import OperationRow from '$lib/components/OperationRow.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import GitHubPublish from '$lib/components/GitHubPublish.svelte';
	import { session } from '$lib/session.svelte';

	let { bot, admin }: { bot: Bot; admin: boolean } = $props();

	type Preview = { head_sha: string; ahead_by: number; behind_by: number; commits: { sha: string; message: string; author: string }[]; files: { path: string; status: string }[]; files_trimmed: boolean };

	let link = $state<RepoLink | null>(null);
	let loaded = $state(false);
	let conn = $state<Connection | null>(null);
	let repos = $state<GitHubRepo[]>([]);
	let branches = $state<string[]>([]);
	let form = $state({ full_name: '', branch: '', root_dir: '', auto_deploy: true });
	let error = $state('');
	let secret = $state('');
	let history = $state<Operation[]>([]);
	let preview = $state<Preview | null>(null);
	let previewError = $state('');
	let previewing = $state(false);
	let settingsOpen = $state(false);
	let now = $state(Date.now());
	let saving = $state(false);
	const path = $derived(`/bots/${bot.id}/github`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const short = (s: string) => s.slice(0, 7);

	async function load() {
		const g = await api<{ linked: boolean; repo?: RepoLink }>('GET', path);
		link = g.linked ? g.repo! : null;
		if (link && !settingsOpen) form = { full_name: link.full_name, branch: link.branch, root_dir: link.root_dir, auto_deploy: link.auto_deploy };
		loaded = true;
		history = (await api<OpPage>('GET', `/bots/${bot.id}/operations?kind=deploy,rollback,publish&limit=15`)).operations;
	}

	onMount(() => {
		if (!available) return;
		init();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible' && (link?.deploying || history.some((o) => o.status === 'running' || o.status === 'queued'))) load().catch(() => {});
		}, 2000);
		return () => clearInterval(t);
	});

	async function init() {
		try {
			conn = (await api<{ connections: Connection[] }>('GET', '/me/connections')).connections.find((c) => c.provider === 'github') ?? null;
			await load();
			if (!link) settingsOpen = true;
			if (conn?.linked && admin) repos = (await api<{ repos: GitHubRepo[] }>('GET', '/me/github/repos')).repos;
		} catch (e) {
			error = msg(e);
			loaded = true;
		}
	}

	// Accepts one of the person's repositories or any public one (owner/name or
	// a GitHub link), resolved through the lookup endpoint.
	async function pickRepo() {
		branches = [];
		error = '';
		if (!form.full_name.trim()) return;
		try {
			const r = await api<RepoLookup>('GET', `/github/lookup?repo=${encodeURIComponent(form.full_name.trim())}`);
			form.full_name = r.repo.full_name;
			form.branch = r.branch;
			if (r.root_dir) form.root_dir = r.root_dir;
			branches = r.branches;
		} catch (e) {
			error = msg(e);
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		error = secret = '';
		saving = true;
		try {
			const r = await api<{ repo: RepoLink }>('PUT', path, form);
			link = r.repo;
			secret = r.repo.secret ?? '';
			settingsOpen = !!secret;
			toast(`Linked ${r.repo.full_name}`);
			preview = null;
		} catch (err) {
			error = msg(err);
		} finally {
			saving = false;
		}
	}
	async function deploy(sha?: string, label?: string) {
		if (sha) {
			const ok = await confirmDialog({
				title: `Deploy ${short(sha)} again?`,
				body: 'The tracked files are replaced with this commit. Files your bot created (data, logs) stay. A running bot restarts on it.',
				details: label ? [['Commit', label]] : undefined,
				confirmLabel: 'Deploy this commit'
			});
			if (!ok) return;
		}
		error = '';
		try {
			await api('POST', `${path}/deploy`, sha ? { sha } : {});
			toast(sha ? `Deploying ${short(sha)}` : 'Deploying the latest commit');
			preview = null;
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function loadPreview() {
		previewing = true;
		previewError = '';
		try {
			preview = await api<Preview>('GET', `${path}/preview`);
		} catch (e) {
			previewError = msg(e);
		} finally {
			previewing = false;
		}
	}
	async function unlink() {
		const ok = await confirmDialog({
			title: `Unlink ${link?.full_name}?`,
			body: 'The bot stops following this repository and the webhook is removed. Files that were already deployed stay in the workspace. A deployment in progress is cancelled.',
			confirmLabel: 'Unlink repository',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', path);
			link = null;
			secret = '';
			settingsOpen = true;
			toast('Repository unlinked');
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}

	const latest = $derived(history[0]);
	const statusColor = { A: 'text-run', M: 'text-warn', D: 'text-fail' };
	const fileMark = (s: string) => (s === 'added' ? 'A' : s === 'removed' ? 'D' : 'M');
	const wanted = page.url.searchParams.get('op');
	// Public repositories deploy without GitHub sign-in (features.public_repos);
	// sign-in (features.deploy) adds private repositories, webhooks and publishing.
	const available = $derived(session.features.deploy || session.features.public_repos);
	const signIn = $derived(session.features.deploy);
</script>

{#if !available}
	<EmptyState title="GitHub deployments are not available">
		<p>This panel has no GitHub deployment service. Upload code under Files or over SFTP.</p>
	</EmptyState>
{:else if !loaded}
	<Skeleton rows={3} label="Loading deployments" />
{:else}
	{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}

	{#if link}
		<!-- The release: what runs now and what would come next. -->
		<section class="spine overflow-hidden rounded-tile border border-rule-soft bg-panel py-4 pr-4 pl-6" data-tone={link.deploying ? 'warn' : link.last_error ? 'fail' : link.last_sha ? 'run' : 'idle'} data-busy={link.deploying} aria-label="Current release">
			<div class="flex flex-wrap items-start gap-3">
				<div class="min-w-0 flex-1">
					<p class="flex items-center gap-2 text-title font-semibold"><Icon name="github" /><span class="break-all">{link.full_name}</span></p>
					<p class="text-muted">Branch <code>{link.branch}</code>{link.root_dir ? `, folder /${link.root_dir}` : ''}. {link.auto_deploy ? (link.hook_created ? 'Deploys on every push.' : link.polling ? 'Checks for new commits every few minutes.' : 'Waiting for the webhook to be added.') : 'Deploys when you ask.'}</p>
					{#if link.pending_push_at_ms && !link.deploying}
						<p class="mt-1 text-small text-warn">A push arrived {fmtAgo(link.pending_push_at_ms, now)} while this server's node was offline. The newest commit deploys when the node reconnects.</p>
					{/if}
					<p class="mt-2">
						{#if link.deploying}
							<span class="font-medium text-warn">Deploying now</span>{#if latest?.status === 'running'}: {latest.stage}{/if}
						{:else if link.last_sha}
							Running commit <code class="font-medium">{short(link.last_sha)}</code>, deployed {fmtAgo(link.last_deployed_at_ms, now)}
						{:else}
							Not deployed yet
						{/if}
					</p>
					{#if link.last_error && !link.deploying}<p class="text-fail">Last deployment failed: {link.last_error}</p>{/if}
				</div>
				<div class="flex flex-wrap gap-2">
					<button class="btn" onclick={loadPreview} disabled={previewing || link.deploying}>{previewing ? 'Checking…' : 'What changed?'}</button>
					<button class="btn btn-primary" onclick={() => deploy()} disabled={link.deploying}><Icon name="rocket" />Deploy latest commit</button>
				</div>
			</div>

			{#if previewError}<Notice tone="fail" class="mt-3">{previewError}</Notice>{/if}
			{#if preview}
				<div class="mt-4 border-t border-rule-soft pt-3">
					{#if link.last_sha && preview.head_sha === link.last_sha}
						<p class="text-muted">Up to date: <code>{short(preview.head_sha)}</code> is already deployed.</p>
					{:else}
						<p class="font-medium">
							{#if !link.last_sha}The first deployment uses <code>{short(preview.head_sha)}</code>.{:else}{preview.ahead_by} new commit{preview.ahead_by === 1 ? '' : 's'} since <code>{short(link.last_sha)}</code>{preview.behind_by ? `, and the branch dropped ${preview.behind_by} (force push)` : ''}.{/if}
						</p>
						{#if preview.commits.length}
							<ul class="mt-2 space-y-1">
								{#each preview.commits as c (c.sha)}<li class="flex gap-2"><code class="shrink-0 text-muted">{short(c.sha)}</code><span class="min-w-0 truncate">{c.message}</span><span class="shrink-0 text-small text-muted">{c.author}</span></li>{/each}
							</ul>
						{/if}
						{#if preview.files.length}
							<details class="mt-2">
								<summary class="text-action">{preview.files.length}{preview.files_trimmed ? '+' : ''} changed file{preview.files.length === 1 ? '' : 's'}</summary>
								<ul class="mt-1 max-h-56 overflow-auto font-mono text-small">
									{#each preview.files as f (f.path)}<li><span class={statusColor[fileMark(f.status)]} title={f.status}>{fileMark(f.status)}</span> {f.path}</li>{/each}
								</ul>
							</details>
						{/if}
						<p class="mt-2 text-small text-muted">Only files tracked in the repository change. Files your bot created, and <code>.env</code> files that are not in the repository, stay.</p>
					{/if}
				</div>
			{/if}
		</section>

		{#if secret || (link.auto_deploy && !link.hook_created && !link.polling)}
			<Notice tone="warn" class="mt-4" title="Add this webhook in the repository settings">
				<dl class="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
					<dt class="text-muted">Payload URL</dt><dd><code class="break-all">{link.webhook_url}</code></dd>
					<dt class="text-muted">Content type</dt><dd><code>application/json</code></dd>
					<dt class="text-muted">Secret</dt><dd>{#if secret}<code class="break-all">{secret}</code>{:else}<span class="text-muted">shown once, after you save the settings again</span>{/if}</dd>
					<dt class="text-muted">Events</dt><dd>Just the push event</dd>
				</dl>
				<p class="mt-1 text-small text-muted">GitHub did not accept the webhook automatically. The connected account may lack admin rights on the repository.{#if link.polling} Until it is added, the panel checks the branch every few minutes.{/if}</p>
			</Notice>
		{/if}

		<section class="mt-8" aria-labelledby="dep-history">
			<h3 id="dep-history" class="text-title font-semibold">History</h3>
			{#if history.length}
				<ul class="mt-2 list-card">
					{#each history as op, i (op.id)}
						<OperationRow {op} {now} initiallyOpen={op.id === wanted}>
							{#snippet actions()}
								{#if op.status === 'succeeded' && op.source_ref && op.source_ref !== link?.last_sha && !link?.deploying}
									<button class="btn btn-sm" onclick={() => deploy(op.source_ref!, op.message ?? undefined)}>{i > 0 ? 'Roll back to this' : 'Redeploy'}</button>
								{/if}
							{/snippet}
						</OperationRow>
					{/each}
				</ul>
				<p class="mt-2 text-small text-muted">Rolling back redeploys an earlier commit's tracked files. Data your bot wrote and environment variables are not rolled back.</p>
			{:else}
				<p class="mt-2 text-muted">Each deployment is listed here with its commit, trigger and result.</p>
			{/if}
		</section>
	{/if}

	{#if signIn}
		<div class="mt-6">
			<GitHubPublish {bot} {link} {conn} {admin} onqueued={() => load().catch(() => {})} />
		</div>
	{/if}
	{#if !link && history.some((o) => o.kind === 'publish')}
		<ul class="mt-4 list-card">
			{#each history.filter((o) => o.kind === 'publish').slice(0, 3) as op (op.id)}<OperationRow {op} {now} />{/each}
		</ul>
	{/if}

	{#if admin}
		<section class="mt-8" aria-labelledby="dep-settings">
			{#if link}
				<button class="flex items-center gap-2 text-title font-semibold" aria-expanded={settingsOpen} onclick={() => (settingsOpen = !settingsOpen)}>
					<Icon name="chevronRight" class="transition-transform {settingsOpen ? 'rotate-90' : ''}" /><span id="dep-settings">Repository settings</span>
				</button>
			{:else}
				<h3 id="dep-settings" class="text-section">Deploy from GitHub</h3>
				<p class="mt-1 max-w-prose text-muted">Link a repository to download a branch into this bot's workspace. Nothing from the repository runs on the host; the bot builds it in its own container.</p>
			{/if}
			{#if settingsOpen}
				{#if !signIn}
					<Notice class="mt-3">Any public repository works; the branch is checked every few minutes. Private repositories, instant webhook deployments and publishing need GitHub sign-in to be configured on this panel (see docs/oauth.md).</Notice>
				{:else if !conn?.linked}
					<Notice class="mt-3">Any public repository works. To use private ones and have pushes deploy instantly through a webhook, <a href="/settings/connected-accounts">connect GitHub</a>; otherwise the branch is checked every few minutes.</Notice>
				{:else if !conn.repo_access}
					<Notice class="mt-3">Only public repositories are available until you grant repository access in <a href="/settings/connected-accounts">Connected accounts</a>. That also lets the panel add the webhook for you.</Notice>
				{/if}
				<form class="mt-4 grid max-w-2xl gap-4" onsubmit={save}>
					<label class="block">
						<span class="label">Repository</span>
						<input class="field font-mono" required bind:value={form.full_name} onchange={pickRepo} list="dep-repos" placeholder="owner/name or https://github.com/owner/name" spellcheck="false" autocomplete="off" />
						<datalist id="dep-repos">{#each repos as r (r.full_name)}<option value={r.full_name}>{r.private ? 'private' : ''}</option>{/each}</datalist>
						<span class="help">Pick one of yours or paste any public repository.</span>
					</label>
					<div class="grid gap-4 sm:grid-cols-2">
						<label class="block">
							<span class="label">Branch</span>
							<select class="field" required bind:value={form.branch}>
								{#if form.branch && !branches.includes(form.branch)}<option value={form.branch}>{form.branch}</option>{/if}
								{#each branches as b (b)}<option value={b}>{b}</option>{/each}
							</select>
						</label>
						<label class="block">
							<span class="label">Folder inside the repository</span>
							<input class="field font-mono" bind:value={form.root_dir} placeholder="/" maxlength="200" />
							<span class="help">Leave empty for the whole repository.</span>
						</label>
					</div>
					<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={form.auto_deploy} /><span>Deploy automatically when this branch changes<span class="help">The panel adds a webhook when your GitHub account administers the repository, and otherwise checks the branch every few minutes.</span></span></label>
					<div class="flex flex-wrap gap-2">
						<button class="btn btn-primary" disabled={saving}>{link ? 'Save settings' : 'Link repository'}</button>
						{#if link}<button type="button" class="btn btn-danger" onclick={unlink}>Unlink repository</button>{/if}
					</div>
				</form>
			{/if}
		</section>
	{:else if !link}
		<p class="text-muted">This bot is not linked to a repository. Someone with full access to the bot can link one.</p>
	{/if}
{/if}
