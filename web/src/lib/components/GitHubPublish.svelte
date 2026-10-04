<script lang="ts">
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Bot, Connection, RepoLink } from '$lib/api/types';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	// Sends the bot's current files to GitHub: into a new repository that the
	// bot is then linked to, or as a commit on the linked branch.
	let { bot, link, conn, admin, onqueued }: { bot: Bot; link: RepoLink | null; conn: Connection | null; admin: boolean; onqueued: () => void } = $props();

	type Plan = { files: number; bytes: number; ignored: number; skipped: { path: string; reason: string }[]; sample: { path: string; size: number }[]; truncated: boolean };
	type Owner = { login: string; org: boolean };
	let open = $state(false);
	let plan = $state<Plan | null>(null);
	let planError = $state('');
	let owners = $state<Owner[]>([]);
	let busy = $state(false);
	let error = $state('');
	const slug = (s: string) => s.replace(/[^A-Za-z0-9_.-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 100) || 'bot';
	let form = $state({ owner: '', name: '', description: '', private: true, auto_deploy: false });
	let message = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const ready = $derived(!!conn?.linked && !!conn.repo_access);

	async function start() {
		open = true;
		error = planError = '';
		plan = null;
		form = { owner: '', name: slug(bot.name), description: '', private: true, auto_deploy: false };
		message = '';
		api<Plan>('GET', `/bots/${bot.id}/github/push-plan`)
			.then((p) => (plan = p))
			.catch((e) => (planError = msg(e)));
		if (!link && ready) {
			api<{ owners: Owner[] }>('GET', '/me/github/owners')
				.then((r) => {
					owners = r.owners;
					form.owner = r.owners[0]?.login ?? '';
				})
				.catch(() => {});
		}
	}
	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			if (link) {
				await api('POST', `/bots/${bot.id}/github/push`, { message });
				toast(`Pushing to ${link.full_name}`);
			} else {
				const r = await api<{ repo: { full_name: string } }>('POST', `/bots/${bot.id}/github/publish`, form);
				toast(`Created ${r.repo.full_name}; pushing the files`, 'success');
			}
			open = false;
			onqueued();
		} catch (err) {
			error = msg(err);
		} finally {
			busy = false;
		}
	}
</script>

{#if link || admin}
	<section class="card flex flex-wrap items-center gap-4 p-5" aria-label="Push to GitHub">
		<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2"><Icon name="commit" /></span>
		<div class="min-w-0 flex-1 basis-64">
			<p class="font-semibold">{link ? 'Push changes to GitHub' : 'Publish to a new GitHub repository'}</p>
			<p class="text-small text-muted">
				{#if link}Commit the bot's current files to <code>{link.full_name}</code> on <code>{link.branch}</code>, for example after editing them here or over SFTP.{:else}Create a repository from this bot's files and link the bot to it, so future changes can be deployed from GitHub.{/if}
			</p>
		</div>
		{#if !conn?.linked}
			<a class="btn" href="/settings/connected-accounts"><Icon name="github" />Connect GitHub</a>
		{:else if !conn.repo_access}
			<a class="btn" href="/settings/connected-accounts" title="Pushing needs the repo scope">Grant repository access</a>
		{:else}
			<button class="btn" onclick={start}><Icon name="github" />{link ? 'Push to GitHub' : 'Publish to GitHub'}</button>
		{/if}
	</section>
{/if}

<Dialog bind:open title={link ? `Push to ${link.full_name}` : 'Publish to GitHub'} size="lg">
	<form id="gh-publish" class="grid gap-4" onsubmit={submit}>
		{#if link}
			<label class="block"><span class="label">Commit message</span><input class="field" maxlength="2000" bind:value={message} placeholder="Update from RivetPanel" /></label>
			<p class="text-small text-muted">Files inside {link.root_dir ? `/${link.root_dir}` : 'the repository'} are replaced by the bot's files; nothing is force-pushed. If someone pushed meanwhile, the push stops and you can deploy their changes first.</p>
		{:else}
			<div class="grid gap-4 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
				<label class="block"><span class="label">Owner</span>
					<select class="field" bind:value={form.owner}>{#each owners as o (o.login)}<option value={o.login}>{o.login}{o.org ? ' (organization)' : ''}</option>{/each}</select>
				</label>
				<label class="block"><span class="label">Repository name</span><input class="field font-mono" required maxlength="100" bind:value={form.name} /></label>
			</div>
			<label class="block"><span class="label">Description <span class="font-normal text-muted">(optional)</span></span><input class="field" maxlength="350" bind:value={form.description} placeholder="{bot.name} (published from RivetPanel)" /></label>
			<div class="grid gap-2 sm:grid-cols-2">
				<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft p-3"><input type="checkbox" class="mt-0.5" bind:checked={form.private} /><span>Private repository<span class="help mt-0">Recommended: bot code often names servers and channels.</span></span></label>
				<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft p-3"><input type="checkbox" class="mt-0.5" bind:checked={form.auto_deploy} /><span>Deploy on every push<span class="help mt-0">Adds a webhook so pushes to GitHub redeploy this bot.</span></span></label>
			</div>
		{/if}

		<div class="rounded-tile border border-rule-soft bg-paper/40 p-4">
			<p class="flex items-center gap-2 font-medium"><Icon name="file" size={14} />What will be pushed</p>
			{#if planError}
				<Notice tone="fail" class="mt-2">{planError}</Notice>
			{:else if !plan}
				<p class="mt-2 text-small text-muted">Collecting files…</p>
			{:else}
				<p class="mt-1 text-small text-muted">{plan.files} files, {fmtBytes(plan.bytes)}. {plan.ignored} left out by <code>.gitignore</code> and the built-in rules (dependencies, <code>.git</code>, <code>.env</code> files; <code>.env.example</code> is kept).</p>
				<ul class="mt-2 max-h-48 overflow-y-auto rounded-control border border-rule-soft bg-raised px-3 py-2 font-mono text-[12px] leading-5">
					{#each plan.sample as f (f.path)}<li class="flex justify-between gap-3"><span class="truncate">{f.path}</span><span class="shrink-0 text-muted">{fmtBytes(f.size)}</span></li>{/each}
					{#if plan.truncated}<li class="text-muted">… and {plan.files - plan.sample.length} more</li>{/if}
				</ul>
				{#if plan.skipped.length}
					<details class="mt-2 text-small">
						<summary class="text-warn">{plan.skipped.length} file{plan.skipped.length === 1 ? '' : 's'} skipped</summary>
						<ul class="mt-1 font-mono text-[12px]">{#each plan.skipped as s (s.path)}<li>{s.path} <span class="text-muted">({s.reason})</span></li>{/each}</ul>
					</details>
				{/if}
				<p class="mt-2 text-small text-muted">Check the list for secrets such as tokens in config files. Environment variables set in the panel are never pushed.</p>
			{/if}
		</div>
		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="gh-publish" disabled={busy || !plan || plan.files === 0}>{link ? 'Push files' : 'Create repository and push'}</button>
	{/snippet}
</Dialog>
