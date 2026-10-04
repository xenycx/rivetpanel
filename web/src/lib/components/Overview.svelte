<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import type { GameDetail } from '$lib/api/games';
	import { api, ApiError } from '$lib/api/client';
	import { toast } from '$lib/ui/toast.svelte';
	import { can, Perm, type Backup, type Bot, type EnvVar, type RepoLink, type Template } from '$lib/api/types';
	import type { PowerAction } from '$lib/api/bots';
	import { joinArgs, fmtAgo, fmtWhen } from '$lib/args';
	import { describe } from '$lib/status';
	import Icon from '$lib/components/ui/Icon.svelte';
	import RecentOperations from '$lib/components/RecentOperations.svelte';
	import ConnectCard from '$lib/components/ConnectCard.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { helpFor } from '$lib/help';
	import { session } from '$lib/session.svelte';

	// The Manage tab: what runs (the console, passed in as `top`), its state and
	// recent activity in the wide column, and the side facts in a narrow one.
	let {
		bot,
		now,
		acting,
		onAct,
		game = null,
		top,
		bottom
	}: { bot: Bot; now: number; acting: boolean; onAct: (a: PowerAction) => void; game?: GameDetail | null; top?: Snippet; bottom?: Snippet } = $props();
	const isGame = $derived(bot.kind === 'game');
	const noun = $derived(isGame ? 'server' : 'bot');

	let template = $state<Template | null>(null);
	let env = $state<EnvVar[] | null>(null);
	let fileCount = $state<number | null>(null);
	let repo = $state<RepoLink | null>(null);
	let backups = $state<Backup[] | null>(null);
	let schedule = $state<{ interval_ms: number; enabled: boolean } | null>(null);

	const canEnv = $derived(can(bot, Perm.env));
	const canFiles = $derived(can(bot, Perm.files));
	const canPower = $derived(can(bot, Perm.power) && session.features.runner);

	async function load() {
		const jobs: Promise<unknown>[] = [];
		if (bot.template_id)
			jobs.push(api<{ templates: Template[] }>('GET', '/templates').then((r) => (template = r.templates.find((t) => t.id === bot.template_id) ?? null)));
		if (canEnv) jobs.push(api<{ vars: EnvVar[] }>('GET', `/bots/${bot.id}/env`).then((r) => (env = r.vars)));
		if (canFiles) {
			jobs.push(api<{ entries: unknown[] }>('GET', `/bots/${bot.id}/files?path=.`).then((r) => (fileCount = r.entries.length)));
			if (session.features.backups)
				jobs.push(
					api<{ backups: Backup[]; health: { interval_ms: number; enabled: boolean } }>('GET', `/bots/${bot.id}/backups`)
						.then((r) => ((backups = r.backups), (schedule = r.health)))
						.catch(() => {})
				);
			if (bot.source_type === 'github' && !isGame)
				jobs.push(api<{ linked: boolean; repo?: RepoLink }>('GET', `/bots/${bot.id}/github`).then((r) => (repo = r.linked ? r.repo! : null)).catch(() => {}));
		}
		await Promise.allSettled(jobs);
	}
	onMount(() => {
		load();
		const t = setInterval(() => document.visibilityState === 'visible' && load(), 10_000);
		return () => clearInterval(t);
	});

	const d = $derived(describe(bot, now));
	const help = $derived(helpFor(bot, { siteAdmin: session.user?.role === 'admin', buildMemory: template?.build_memory_bytes }));
	const everRan = $derived(bot.last_started_at_ms !== null);
	const missingVars = $derived(template && env ? template.env.filter((v) => v.required && !env!.some((e) => e.name === v.name)) : []);
	const lastBackup = $derived(backups?.find((b) => b.status === 'ready') ?? null);
	const lastFailedBackup = $derived(backups?.[0]?.status === 'failed' ? backups[0] : null);

	type Step = { label: string; done: boolean | null; detail: string; href?: string; cta?: string };
	const steps = $derived.by((): Step[] => {
		const code: Step =
			bot.source_type === 'template'
				? { label: 'Code is in place', done: true, detail: `${template?.name ?? 'Template'} files were added to the workspace.` }
				: bot.source_type === 'github'
					? {
							label: 'Code is deployed',
							done: repo ? !!repo.last_sha : null,
							detail: repo?.last_sha ? `Commit ${repo.last_sha.slice(0, 7)} from ${repo.full_name}.` : repo?.deploying ? 'The first deployment is running.' : repo?.last_error || 'Deploy the repository to add the code.',
							href: '?tab=deploy',
							cta: 'Open deployments'
						}
					: {
							label: 'Code is in place',
							done: fileCount === null ? null : fileCount > 0,
							detail: fileCount ? 'The workspace contains files.' : 'Upload your code in Files or connect with SFTP.',
							href: '?tab=files',
							cta: 'Open files'
						};
		const vars: Step = !canEnv
			? { label: 'Required values are set', done: null, detail: 'Someone with environment access has to add them.' }
			: template
				? {
						label: 'Required values are set',
						done: env ? missingVars.length === 0 : null,
						detail: missingVars.length ? `Missing ${missingVars.map((v) => v.name).join(', ')}.` : `${template.env.filter((v) => v.required).map((v) => v.name).join(', ')} added.`,
						href: '?tab=env',
						cta: 'Add values'
					}
				: {
						label: 'Variables are set',
						done: env ? env.length > 0 : null,
						detail: env?.length ? `${env.length} variable${env.length === 1 ? '' : 's'} saved.` : 'Add the token and anything else your code reads.',
						href: '?tab=env',
						cta: 'Open environment'
					};
		const start: Step = {
			label: 'Start command',
			done: true,
			detail: joinArgs(bot.entrypoint.length ? [...bot.entrypoint, ...bot.argv] : bot.argv),
			href: can(bot, Perm.admin) ? '?tab=startup' : undefined,
			cta: 'Change'
		};
		const ran: Step = {
			label: `${isGame ? 'Server' : 'Bot'} started successfully`,
			done: everRan ? true : ['building', 'starting', 'queued', 'retrying'].includes(bot.phase) ? null : false,
			detail: everRan
				? 'It reached the running state.'
				: bot.phase === 'building'
					? 'Building now. The first build can take a few minutes.'
					: bot.phase === 'retrying' || bot.phase === 'failed'
						? 'The last attempt failed. See the details above.'
						: 'Press Start when the steps above are complete.'
		};
		return [code, vars, start, ran];
	});
	// Game servers install themselves on the first start: no setup checklist.
	const pendingSetup = $derived(!everRan && !isGame);
	const versionVar = $derived(game?.variables.find((v) => v.versions) ?? null);
	// A game server whose port another container holds can move to a free one.
	const portBlocked = $derived(isGame && bot.state_reason === 'port_conflict' && can(bot, Perm.admin));
	let picking = $state(false);
	async function pickFreePort() {
		picking = true;
		try {
			const b = await api<Bot>('POST', `/bots/${bot.id}/allocations/pick-free`);
			const p = b.allocations?.find((a) => a.primary);
			toast(p ? `Moved to port ${p.port}.${b.desired_state === 'running' ? ' Starting again.' : ''}` : 'Moved to a free port.', 'success');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'No free port could be chosen.', 'fail');
		} finally {
			picking = false;
		}
	}
</script>

<div class="grid gap-x-8 gap-y-6 lg:grid-cols-[minmax(0,1fr)_18rem] xl:grid-cols-[minmax(0,1fr)_20rem]">
	<div class="min-w-0 space-y-6">
		{#if top}{@render top()}{/if}
		<!-- What is happening and what to do next. A failure outranks everything else. -->
		<section aria-labelledby="ov-state">
			<h2 id="ov-state" class="text-title font-semibold">Status</h2>
			<div class="mt-2 rounded-tile border border-rule-soft bg-panel p-4">
				<p class="text-section font-semibold {d.tone === 'fail' ? 'text-fail' : ''}">{d.label}</p>
				{#if d.detail}<p class="mt-1 max-w-prose">{d.detail}</p>{/if}
				{#if bot.phase === 'running' && bot.last_started_at_ms}
					<p class="mt-1 text-muted">Running since {fmtWhen(bot.last_started_at_ms)} ({fmtAgo(bot.last_started_at_ms, now).replace(' ago', '')}).</p>
				{/if}
				{#if bot.last_error && bot.state_reason !== 'port_conflict' && ['failed', 'retrying', 'stopped', 'exited'].includes(bot.phase)}
					<pre class="mt-3 max-h-60 overflow-auto bg-term p-3 font-mono text-[12.5px] leading-relaxed whitespace-pre-wrap text-term-ink">{bot.last_error}</pre>
				{/if}
				{#if help}
					<Notice tone="warn" class="mt-3" title={help.title}>
						{help.body}
						{#snippet action()}{#if help.action}<a class="btn btn-sm" href={help.action.href}>{help.action.label}</a>{/if}{/snippet}
					</Notice>
				{/if}
				{#if canPower || portBlocked}
					<div class="mt-4 flex flex-wrap gap-2">
						{#if portBlocked}
							<button class="btn btn-primary" disabled={picking} onclick={pickFreePort}><Icon name="network" size={13} />Pick a free port</button>
							<span class="sr-only">Gives the server the next free port of this node and returns the busy one to the pool.</span>
						{/if}
						{#if canPower && d.next === 'retry'}
							<button class="btn {portBlocked ? '' : 'btn-primary'}" disabled={acting} onclick={() => onAct('start')}><Icon name="play" size={12} />Start again</button>
						{:else if canPower && d.next === 'start' && bot.desired_state !== 'running'}
							<button class="btn btn-primary" disabled={acting || (pendingSetup && missingVars.length > 0)} onclick={() => onAct('start')}><Icon name="play" size={12} />Start</button>
						{/if}
						{#if can(bot, Perm.console) && ['running', 'retrying', 'failed', 'exited'].includes(bot.phase) && bot.state_reason !== 'setup_failed' && bot.state_reason !== 'build_failed'}
							<a class="btn" href="?tab=manage" data-sveltekit-noscroll><Icon name="terminal" />Open console</a>
						{/if}
						{#if bot.state_reason === 'build_failed' && can(bot, Perm.admin)}
							<a class="btn" href="?tab=startup" data-sveltekit-noscroll>Check startup settings</a>
						{/if}
					</div>
				{/if}
			</div>
		</section>

		{#if pendingSetup}
			<section aria-labelledby="ov-setup">
				<h3 id="ov-setup" class="text-title font-semibold">Setup</h3>
				<p class="text-muted">Shown until the {noun} runs for the first time.</p>
				<ol class="mt-3 list-card">
					{#each steps as s, i (s.label)}
						<li class="flex items-start gap-3 px-4 py-3">
							<span
								class="mt-0.5 grid size-6 shrink-0 place-items-center rounded-pill text-small font-semibold {s.done === true ? 'bg-run text-white' : s.done === false ? 'border-2 border-warn text-warn' : 'border border-rule text-muted'}"
								aria-hidden="true">{#if s.done === true}<Icon name="check" size={13} />{:else}{i + 1}{/if}</span
							>
							<div class="min-w-0 flex-1">
								<p class="font-medium">{s.label}<span class="sr-only">: {s.done === true ? 'done' : s.done === false ? 'to do' : 'pending'}</span></p>
								<p class="text-muted {i === 2 ? 'font-mono text-small break-all' : ''}">{s.detail}</p>
							</div>
							{#if s.href && s.done !== true}<a class="btn btn-sm shrink-0" href={s.href} data-sveltekit-noscroll>{s.cta}</a>{/if}
						</li>
					{/each}
				</ol>
				{#if template && !template.privileged_intents.length}
					<p class="mt-2 text-small text-muted">{template.name} needs no privileged gateway intents. {template.first_start}</p>
				{/if}
			</section>
		{/if}

		<RecentOperations botId={bot.id} canOutput={can(bot, Perm.console)} {isGame} />
		{#if bottom}{@render bottom()}{/if}
	</div>

	<aside class="space-y-6" aria-label="Details">
		{#if isGame}<ConnectCard {bot} {game} />{/if}
		<section>
			<h3 class="text-title font-semibold">{isGame ? 'Server type' : 'Source'}</h3>
			<div class="mt-2 text-ink/90">
				{#if isGame}
					<p>{game?.blueprint.name ?? 'Game server'}{#if bot.installed_version}{' '}<span class="text-muted">{bot.installed_version}</span>{/if}</p>
					<p class="text-small text-muted">
						{#if !bot.installed_version}{bot.install_state === 'installing' ? 'Installing now.' : bot.install_state === 'failed' ? 'The last installation failed.' : 'Installed on the first start.'}{:else if versionVar}{versionVar.value === 'latest' || !versionVar.value ? 'Follows the latest stable release when reinstalled.' : `Pinned to ${versionVar.value}.`}{/if}
						{#if game}Revision {bot.blueprint_revision ?? game.blueprint.current_revision}{game.update_available ? ', an update is available' : ''}.{/if}
					</p>
					{#if can(bot, Perm.admin)}<a class="link mt-1 inline-block text-small" href="?tab=startup" data-sveltekit-noscroll>Version and startup</a>{/if}
				{:else if bot.source_type === 'github'}
					{#if repo}
						<p><Icon name="github" class="mr-1 inline align-[-2px]" /><code>{repo.full_name}</code> on <code>{repo.branch}</code></p>
						<p class="text-muted">
							{#if repo.deploying}Deploying now.{:else if repo.last_sha}Commit <code>{repo.last_sha.slice(0, 7)}</code>, {fmtAgo(repo.last_deployed_at_ms, now)}.{:else}Not deployed yet.{/if}
						</p>
						{#if repo.last_error}<p class="text-fail">Last deployment failed: {repo.last_error}</p>{/if}
					{:else if canFiles}
						<p class="text-muted">The repository link was removed. <a class="link" href="?tab=deploy">Link a repository</a></p>
					{:else}<p class="text-muted">Deployed from GitHub.</p>{/if}
				{:else if bot.source_type === 'template'}
					<p>{template?.name ?? 'Template'}{#if template}<span class="text-muted">, version {template.version}</span>{/if}</p>
					{#if template}<p class="text-small text-muted">Tested with {template.tested_with}.</p>{/if}
				{:else}
					<p>Uploaded files</p>
					<p class="text-small text-muted">Manage the code in Files or over SFTP.</p>
				{/if}
			</div>
		</section>

		{#if canFiles && session.features.backups}
			<section>
				<h3 class="text-title font-semibold">Backups</h3>
				<div class="mt-2">
					{#if backups === null}
						<p class="text-muted">Loading…</p>
					{:else if lastBackup}
						<p>Last backup {fmtAgo(lastBackup.created_at_ms, now)}</p>
						<p class="text-small text-muted">{fmtWhen(lastBackup.created_at_ms)}. {!schedule?.interval_ms ? 'Scheduled backups are off on this panel.' : schedule.enabled ? 'Scheduled backups are on.' : `Scheduled backups are off for this ${noun}.`}</p>
					{:else}
						<p class="text-muted">No backup yet.</p>
					{/if}
					{#if lastFailedBackup}<p class="mt-1 text-small text-fail">The latest backup failed: {lastFailedBackup.error ?? 'unknown error'}</p>{/if}
					<a class="link mt-1 inline-block text-small" href="?tab=backups" data-sveltekit-noscroll>Manage backups</a>
				</div>
			</section>
		{/if}

		<section>
			<h3 class="text-title font-semibold">Details</h3>
			<dl class="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-small">
				<dt class="text-muted">Created</dt><dd>{fmtWhen(bot.created_at_ms)}</dd>
				<dt class="text-muted">Restart policy</dt><dd>{bot.restart_policy === 'never' ? 'Never restart' : `Restart after a crash${bot.restart_max_attempts ? `, up to ${bot.restart_max_attempts} times in a row` : ''}`}</dd>
				<dt class="text-muted">Network</dt><dd>{bot.network_enabled ? 'Internet access on' : 'No network'}{bot.ports.length ? `, ${bot.ports.length} published port${bot.ports.length === 1 ? '' : 's'}` : ''}</dd>
				<dt class="text-muted">{isGame ? 'Server ID' : 'Bot ID'}</dt><dd><code class="break-all">{bot.id}</code></dd>
			</dl>
		</section>
	</aside>
</div>
