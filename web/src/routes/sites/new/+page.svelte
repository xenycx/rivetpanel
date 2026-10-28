<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, MiB } from '$lib/api/client';
	import type { AddonKind, Connection, GitHubRepo, RepoLookup, Site, SitesInfo, WebAnalysis, WebKind, WebPlan } from '$lib/api/types';
	import { joinArgs, splitArgs } from '$lib/args';
	import { can } from '$lib/session.svelte';
	import { creatable, loadWorkspaces, workspaces, wsName } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	// A site deployed from a GitHub repository: published as it is, built
	// into static files, or run as an app behind the site's addresses.
	let info = $state<SitesInfo | null>(null);
	let presets = $state<WebPlan[]>([]);
	let publicPrefixes = $state<string[]>([]);
	let addonKinds = $state<AddonKind[]>([]);
	let gh = $state<Connection | null>(null);
	let repos = $state<GitHubRepo[]>([]);
	let loadError = $state('');

	let repoInput = $state(page.url.searchParams.get('repo') ?? '');
	let lookup = $state<RepoLookup | null>(null);
	let repo = $state({ full_name: '', branch: '', root_dir: '', auto_deploy: true });
	let branches = $state<string[]>([]);
	let busy = $state('');
	let repoError = $state('');
	let analysis = $state<WebAnalysis | null>(null);

	let kind = $state<WebKind>('static');
	let framework = $state('static');
	let runtime = $state('nodejs');
	let buildCommand = $state('');
	let outputDir = $state('.');
	let spa = $state(false);
	let command = $state('');
	let port = $state(3000);
	let healthPath = $state('/');
	let releaseCommand = $state('');
	let memoryMiB = $state<number | null>(null);
	let bodyMiB = $state<number | null>(null);
	let env = $state<{ name: string; value: string; description?: string; required?: boolean; secret?: boolean }[]>([]);
	let addonSel = $state<string[]>([]);
	let notes = $state<string[]>([]);

	let name = $state('');
	let nameTouched = $state(false);
	let slug = $state('');
	let slugTouched = $state(false);
	let domainId = $state('');
	let workspaceId = $state(workspaces.selected !== 'all' ? workspaces.selected : '');
	let submitError = $state('');

	const msg = (e: unknown, fallback = 'The request failed.') => (e instanceof ApiError ? e.message : fallback);
	const suggest = (n: string) => n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30);
	const bases = $derived(info?.domains ?? []);
	const preview = $derived((bases.find((b) => b.id === domainId)?.example_url ?? info?.example_url ?? '').replace('://example.', `://${(slugTouched ? slug : suggest(name)) || 'your-site'}.`));
	const kinds: { id: WebKind; label: string; help: string }[] = [
		{ id: 'static', label: 'Static files', help: 'The files in the repository are the site.' },
		{ id: 'static_build', label: 'Static build', help: 'A build runs on each deploy and its output folder is published.' },
		{ id: 'app', label: 'App (server)', help: 'A server process runs and receives every request: Node.js, PHP, Python, Go, Rust…' }
	];
	const forKind = $derived(presets.filter((p) => p.kind === kind));
	const appsOff = $derived(kind === 'app' && info && !info.apps_enabled);
	const ready = $derived(!!repo.full_name && !!repo.branch && !!name.trim() && !appsOff && (kind !== 'app' || splitArgs(command).length > 0) && (kind !== 'static_build' || !!buildCommand.trim()));

	$effect(() => {
		loadWorkspaces();
	});
	$effect(() => {
		if (workspaceId && workspaces.list.find((w) => w.id === workspaceId)?.personal) workspaceId = '';
	});

	onMount(async () => {
		try {
			info = await api<SitesInfo>('GET', '/sites-info');
			domainId = info.domains?.find((b) => b.primary)?.id ?? '';
			const p = await api<{ presets: WebPlan[]; public_build_prefixes: string[] }>('GET', '/site-presets');
			presets = p.presets;
			publicPrefixes = p.public_build_prefixes;
		} catch (e) {
			loadError = msg(e, 'Site hosting settings could not be loaded.');
		}
		api<{ addons: AddonKind[]; available: boolean }>('GET', '/addons')
			.then((r) => (addonKinds = r.available ? r.addons : []))
			.catch(() => {});
		try {
			gh = (await api<{ connections: Connection[] }>('GET', '/me/connections')).connections.find((c) => c.provider === 'github') ?? null;
			if (gh?.linked) repos = (await api<{ repos: GitHubRepo[] }>('GET', '/me/github/repos')).repos;
		} catch {
			gh = null;
		}
		if (repoInput) lookUp();
	});

	async function lookUp(e?: Event) {
		e?.preventDefault();
		const v = repoInput.trim();
		if (!v) return;
		busy = 'lookup';
		repoError = '';
		analysis = null;
		try {
			lookup = await api<RepoLookup>('GET', `/github/lookup?repo=${encodeURIComponent(v)}`);
			repo = { ...repo, full_name: lookup.repo.full_name, branch: lookup.branch, root_dir: lookup.root_dir };
			branches = lookup.branches;
			if (!nameTouched) name = lookup.repo.full_name.split('/')[1] ?? '';
			await analyze();
		} catch (err) {
			lookup = null;
			repoError = msg(err, 'The repository could not be found.');
		} finally {
			busy = '';
		}
	}

	async function analyze() {
		busy = 'analyze';
		repoError = '';
		try {
			analysis = await api<WebAnalysis>('POST', '/sites/analyze', { repo: repo.full_name, branch: repo.branch, root_dir: repo.root_dir });
			apply(analysis.plan);
		} catch (err) {
			repoError = msg(err, 'The repository could not be analyzed.');
		} finally {
			busy = '';
		}
	}

	function apply(p: WebPlan, keepEnv = false) {
		kind = p.kind;
		framework = p.framework;
		runtime = p.runtime;
		buildCommand = p.build_command;
		outputDir = p.output_dir || '.';
		spa = p.spa;
		command = joinArgs(p.argv ?? []);
		port = p.port || 3000;
		healthPath = p.health_path || '/';
		releaseCommand = p.release_command;
		memoryMiB = p.memory_bytes ? Math.round(p.memory_bytes / MiB) : null;
		notes = p.notes ?? [];
		addonSel = p.addons ?? [];
		if (!keepEnv) env = (p.env ?? []).map((v) => ({ name: v.name, value: v.value, description: v.description, required: v.required, secret: v.secret }));
		else for (const v of p.env ?? []) if (!env.some((x) => x.name === v.name)) env = [...env, { name: v.name, value: v.value, description: v.description }];
	}
	function choosePreset(id: string) {
		const p = presets.find((x) => x.framework === id);
		if (p) apply(p, true);
	}
	function chooseKind(k: WebKind) {
		if (k === kind) return;
		const p = presets.find((x) => x.kind === k);
		if (p) apply(p, true);
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = 'create';
		submitError = '';
		const vars: Record<string, string> = {};
		for (const v of env) if (v.name.trim()) vars[v.name.trim()] = v.value;
		try {
			const s = await api<Site>('POST', '/sites/from-repo', {
				name: name.trim(),
				slug: slugTouched ? slug : '',
				domain_id: domainId,
				workspace_id: workspaceId,
				kind,
				framework,
				repo,
				runtime,
				build_command: kind === 'static' ? '' : buildCommand,
				output_dir: outputDir,
				spa,
				argv: kind === 'app' ? splitArgs(command) : [],
				port: kind === 'app' ? port : 0,
				health_path: healthPath,
				release_command: kind === 'app' ? releaseCommand : '',
				memory_bytes: kind === 'app' && memoryMiB ? Math.round(memoryMiB * MiB) : 0,
				max_body_bytes: kind === 'app' && bodyMiB ? Math.round(bodyMiB * MiB) : 0,
				env: vars,
				addons: kind === 'app' ? addonSel.map((k) => ({ kind: k })) : []
			});
			goto(`/sites/${s.id}?tab=deployments`);
		} catch (err) {
			submitError = msg(err, 'The site could not be created.');
		} finally {
			busy = '';
		}
	}
</script>

<svelte:head><title>New site from GitHub · RivetPanel</title></svelte:head>

<a href="/sites" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All sites</a>
<header class="page-head">
	<h1 class="font-semibold">New site from GitHub</h1>
	<p class="text-small text-muted">Static files, a static build, or a running app. Every push can deploy automatically.</p>
</header>

{#if loadError}<Notice tone="fail" class="mt-4" live>{loadError}</Notice>{/if}
{#if info && !info.builds_enabled}
	<Notice tone="warn" class="mt-4">Building sites from repositories is not available on this panel: GitHub sign-in and the local runner (Docker) must be set up. You can still <a class="link" href="/sites?new=1">create a site and upload files</a>.</Notice>
{:else if !can('sites.create')}
	<Notice tone="warn" class="mt-4">Your account cannot create sites.</Notice>
{:else}
	<form class="mt-5 grid max-w-4xl gap-6" onsubmit={create}>
		<!-- 1. Repository -->
		<section class="card p-5" aria-labelledby="repo-h">
			<h2 id="repo-h" class="text-title font-semibold">1. Repository</h2>
			<div class="mt-3 flex flex-wrap gap-2">
				<label class="min-w-0 flex-1 basis-72"><span class="sr-only">Repository</span>
					<input class="field font-mono" list="site-repos" bind:value={repoInput} placeholder="owner/name or https://github.com/owner/name" onkeydown={(e) => e.key === 'Enter' && lookUp(e)} />
					<datalist id="site-repos">{#each repos as r (r.full_name)}<option value={r.full_name}>{r.private ? 'private' : 'public'}</option>{/each}</datalist>
				</label>
				<button type="button" class="btn" onclick={lookUp} disabled={!!busy || !repoInput.trim()}>{busy === 'lookup' ? 'Looking up…' : 'Look up'}</button>
			</div>
			{#if !gh?.linked}<p class="help">Only public repositories work until you <a class="link" href="/settings/connected-accounts">connect GitHub</a>; private repositories deploy with your connected account.</p>{/if}
			{#if repoError}<Notice tone="fail" class="mt-3" live>{repoError}</Notice>{/if}
			{#if lookup}
				<div class="mt-4 grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end">
					<label class="block"><span class="label">Branch</span>
						<select class="field font-mono" bind:value={repo.branch} onchange={analyze}>{#each branches as b (b)}<option value={b}>{b}</option>{/each}</select>
					</label>
					<label class="block"><span class="label">Folder</span><input class="field font-mono" bind:value={repo.root_dir} placeholder="/ (repository root)" onchange={analyze} /></label>
					<button type="button" class="btn" onclick={analyze} disabled={!!busy}><Icon name="search" size={14} />{busy === 'analyze' ? 'Analyzing…' : 'Analyze again'}</button>
				</div>
				{#if analysis}
					<p class="mt-3 text-small text-muted"><Icon name="check" size={13} class="inline text-run" /> Detected <strong class="text-ink">{analysis.plan.label}</strong>{analysis.plan.evidence?.length ? ` from ${analysis.plan.evidence.join(', ')}` : ''} at <code>{analysis.sha.slice(0, 7)}</code>. {analysis.plan.summary}</p>
				{/if}
			{/if}
		</section>

		{#if lookup}
			<!-- 2. How it is hosted -->
			<section class="card p-5" aria-labelledby="kind-h">
				<h2 id="kind-h" class="text-title font-semibold">2. How it is hosted</h2>
				<div class="mt-3 grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Site type">
					{#each kinds as k (k.id)}
						<button type="button" role="radio" aria-checked={kind === k.id} class="rounded-tile border p-3 text-left transition-colors {kind === k.id ? 'border-action bg-action/8' : 'border-rule hover:border-action/60'}" onclick={() => chooseKind(k.id)}>
							<span class="font-semibold">{k.label}</span>
							<span class="mt-1 block text-small text-muted">{k.help}</span>
						</button>
					{/each}
				</div>
				{#if appsOff}<Notice tone="warn" class="mt-3">Apps need the panel's local runner (Docker); this panel can host static sites only.</Notice>{/if}
				{#each notes as n (n)}<Notice tone="warn" class="mt-3">{n}</Notice>{/each}

				<div class="mt-4 grid gap-4 sm:grid-cols-2">
					<label class="block sm:col-span-2"><span class="label">Framework</span>
						<select class="field" value={framework} onchange={(e) => choosePreset(e.currentTarget.value)}>
							{#each forKind as p (p.framework)}<option value={p.framework}>{p.label}</option>{/each}
						</select>
					</label>
					{#if kind !== 'static'}
						<label class="block sm:col-span-2"><span class="label">Build command</span>
							<textarea class="field font-mono text-small" rows="3" bind:value={buildCommand} placeholder={kind === 'app' ? 'Empty: the runtime default' : 'npm ci && npm run build'}></textarea>
							<span class="help">Runs in a build container on every deploy, line by line, stopping at the first error. It gets no secrets: only variables starting with {publicPrefixes.join(', ')}.</span>
						</label>
					{/if}
					{#if kind !== 'app'}
						<label class="block"><span class="label">{kind === 'static' ? 'Folder to publish' : 'Output folder'}</span><input class="field font-mono" bind:value={outputDir} placeholder="dist" /></label>
						<label class="flex items-start gap-2.5 self-end pb-2"><input type="checkbox" class="mt-0.5" bind:checked={spa} /><span>Single-page application<span class="help mt-0">Unknown paths serve <code>index.html</code>.</span></span></label>
					{:else}
						<label class="block sm:col-span-2"><span class="label">Start command</span><input class="field font-mono" required bind:value={command} placeholder="node build" /><span class="help">Runs from the repository folder. The app must listen on <code>$PORT</code> on all interfaces (<code>$HOST</code> is 0.0.0.0).</span></label>
						<label class="block"><span class="label">Port</span><input class="field font-mono" type="number" min="1" max="65535" bind:value={port} /></label>
						<label class="block"><span class="label">Health check path</span><input class="field font-mono" bind:value={healthPath} placeholder="/" /><span class="help">Requested every 30 s; three failures restart the app.</span></label>
						<label class="block sm:col-span-2"><span class="label">Release command <span class="font-normal text-muted">(optional)</span></span><input class="field font-mono" bind:value={releaseCommand} placeholder="npx prisma migrate deploy" /><span class="help">Runs before every start with the app's variables and add-ons, for database migrations. A failure stops the start.</span></label>
						<label class="block"><span class="label">Memory (MiB)</span><input class="field font-mono" type="number" min="64" step="64" bind:value={memoryMiB} placeholder="runtime default" /></label>
						<label class="block"><span class="label">Request body limit (MiB)</span><input class="field font-mono" type="number" min="1" bind:value={bodyMiB} placeholder="panel default" /><span class="help">Raise it for large uploads.</span></label>
					{/if}
				</div>
				{#if kind === 'app'}
					<p class="mt-4 rounded-tile bg-paper-2 px-3 py-2 text-small text-muted"><Icon name="archive" size={13} class="inline" /> <code>/data</code> is the app's persistent folder: it survives deploys and is included in backups. Keep uploads and SQLite files there (<code>$DATA_DIR</code>).</p>
				{/if}
			</section>

			<!-- 3. Variables and add-ons -->
			<section class="card p-5" aria-labelledby="env-h">
				<h2 id="env-h" class="text-title font-semibold">3. Variables{kind === 'app' && addonKinds.length ? ' and add-ons' : ''}</h2>
				<p class="mt-1 text-small text-muted">Stored encrypted. {kind === 'app' ? 'The app receives all of them.' : `A static build receives only the public ones (${publicPrefixes.join(', ')}).`}</p>
				<div class="mt-3 grid gap-2">
					{#each env as v, i (i)}
						<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] gap-2">
							<input class="field font-mono" aria-label="Name" bind:value={v.name} placeholder="NAME" />
							<input class="field font-mono" aria-label="Value of {v.name}" type={v.secret ? 'password' : 'text'} bind:value={v.value} placeholder={v.description || (v.required ? 'required' : 'value')} />
							<button type="button" class="btn btn-quiet btn-icon" aria-label="Remove {v.name}" onclick={() => (env = env.filter((_, j) => j !== i))}><Icon name="x" size={14} /></button>
						</div>
					{/each}
					<div><button type="button" class="btn btn-sm" onclick={() => (env = [...env, { name: '', value: '' }])}><Icon name="plus" size={13} />Add variable</button></div>
				</div>
				{#if kind === 'app' && addonKinds.length}
					<fieldset class="mt-5">
						<legend class="label">Add-ons</legend>
						<div class="flex flex-wrap gap-3">
							{#each addonKinds as k (k.id)}
								<label class="flex items-center gap-2"><input type="checkbox" value={k.id} bind:group={addonSel} />{k.display_name}</label>
							{/each}
						</div>
						<span class="help">A private database next to the app; its connection variables (such as <code>DATABASE_URL</code>) are added for you.</span>
					</fieldset>
				{/if}
			</section>

			<!-- 4. Address -->
			<section class="card p-5" aria-labelledby="addr-h">
				<h2 id="addr-h" class="text-title font-semibold">4. Name and address</h2>
				<div class="mt-3 grid gap-4 sm:grid-cols-2">
					<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} oninput={() => (nameTouched = true)} /></label>
					<label class="block"><span class="label">Address</span>
						<div class="flex items-stretch">
							<input class="field min-w-0 font-mono {bases.length > 1 ? 'rounded-r-none' : ''}" maxlength="40" value={slugTouched ? slug : suggest(name)} oninput={(e) => { slugTouched = true; slug = e.currentTarget.value.toLowerCase(); }} />
							{#if bases.length > 1}<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={domainId} aria-label="Sites domain">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}</select>{/if}
						</div>
						<span class="help">Served at <code class="text-ink">{preview}</code>; add your own domain afterwards.</span>
					</label>
					{#if creatable().length > 1}
						<label class="block"><span class="label">Workspace</span>
							<select class="field" bind:value={workspaceId}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{wsName(w)}</option>{/each}</select>
						</label>
					{/if}
					<label class="flex items-start gap-2.5 self-end pb-2"><input type="checkbox" class="mt-0.5" bind:checked={repo.auto_deploy} /><span>Deploy on every push<span class="help mt-0">To <code>{repo.branch}</code>, through a GitHub webhook (or by checking the branch every few minutes).</span></span></label>
				</div>
			</section>

			{#if submitError}<Notice tone="fail" live>{submitError}</Notice>{/if}
			<div class="flex flex-wrap gap-2">
				<button class="btn btn-primary" disabled={!ready || !!busy}><Icon name="rocket" size={14} />{busy === 'create' ? 'Creating…' : 'Create and deploy'}</button>
				<a class="btn" href="/sites">Cancel</a>
			</div>
		{/if}
	</form>
{/if}
