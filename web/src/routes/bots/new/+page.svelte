<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes, MiB } from '$lib/api/client';
	import type { AddonKind, Analysis, Bot, Connection, GitHubRepo, Limits, NodeInfo, Plan, Recipe, RepoLookup, RuntimeInfo, Template, TemplateEnv } from '$lib/api/types';
	import { joinArgs, splitArgs } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import { session } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import { creatable, loadWorkspaces, workspaces } from '$lib/workspaces.svelte';

	type Source = 'template' | 'github' | 'blank';
	const steps = ['Source', 'Details', 'Setup values', 'Review'];

	const initial = page.url.searchParams.get('source') as Source | null;
	let source = $state<Source>(initial && ['template', 'github', 'blank'].includes(initial) ? initial : 'template');
	let step = $state(initial ? 1 : 0);

	let templates = $state<Template[] | null>(null);
	let runtimes = $state<RuntimeInfo[]>([]);
	let limits = $state<Limits | null>(null);
	let addonKinds = $state<AddonKind[]>([]);
	let recipes = $state<Recipe[]>([]);
	let gh = $state<Connection | null>(null);
	let repos = $state<GitHubRepo[] | null>(null);
	let branches = $state<string[]>([]);
	let loadError = $state('');

	let templateId = $state(page.url.searchParams.get('template') ?? '');
	let name = $state('');
	let nameTouched = $state(false);
	let runtime = $state('nodejs');
	let repo = $state({ full_name: '', branch: '', root_dir: '', auto_deploy: true });
	let env = $state<Record<string, string>>({});
	let extra = $state<{ name: string; value: string }[]>([]);
	let shown = $state<Record<string, boolean>>({});
	let memoryMiB = $state<number | null>(null);
	let cpus = $state<number | null>(null);
	let startNow = $state(true);
	let nodes = $state<NodeInfo[]>([]);
	let nodeId = $state('');

	// GitHub: paste any public repository, or pick one of your own.
	let ghMode = $state<'url' | 'mine'>('url');
	let repoInput = $state(page.url.searchParams.get('repo') ?? '');
	let lookup = $state<RepoLookup | null>(null);
	let lookingUp = $state(false);
	// Start/build commands and add-ons (GitHub and empty bots).
	let command = $state('');
	let buildCommand = $state('');
	let addonSel = $state<string[]>([]);
	let showAdvanced = $state(false);
	// Repository analysis.
	let analysis = $state<Analysis | null>(null);
	let analyzing = $state(false);
	let analyzeError = $state('');
	let useAI = $state(true);
	let planEnv = $state<TemplateEnv[]>([]);

	// New bots go into the workspace selected in the sidebar (personal when "all").
	let workspaceId = $state(workspaces.selected !== 'all' ? workspaces.selected : '');
	$effect(() => {
		loadWorkspaces();
	});
	// The personal workspace is the server default, so it is sent as "".
	$effect(() => {
		if (workspaceId && workspaces.list.find((w) => w.id === workspaceId)?.personal) workspaceId = '';
	});
	const targets = $derived(creatable());

	let problems = $state<Record<string, string>>({});
	let submitError = $state('');
	let busy = $state(false);
	let heading: HTMLHeadingElement | undefined = $state();

	onMount(async () => {
		try {
			const [t, r] = await Promise.all([
				api<{ templates: Template[] }>('GET', '/templates'),
				api<{ runtimes: RuntimeInfo[]; limits: Limits }>('GET', '/runtimes')
			]);
			templates = t.templates;
			runtimes = r.runtimes;
			limits = r.limits;
		} catch (e) {
			loadError = e instanceof ApiError ? e.message : 'The templates could not be loaded.';
			templates = [];
		}
		api<{ addons: AddonKind[]; available: boolean }>('GET', '/addons')
			.then((r) => (addonKinds = r.available ? r.addons : []))
			.catch(() => {});
		api<{ recipes: Recipe[] }>('GET', '/github/recipes')
			.then((r) => (recipes = r.recipes))
			.catch(() => {});
		try {
			gh = (await api<{ connections: Connection[] }>('GET', '/me/connections')).connections.find((c) => c.provider === 'github') ?? null;
		} catch {
			gh = null;
		}
		if (source === 'github' && repoInput) lookUp();
		if (session.user?.role === 'admin' && session.features.agents) {
			api<{ nodes: NodeInfo[] }>('GET', '/nodes').then((r) => {
				nodes = r.nodes.filter((n) => n.enabled && !n.draining);
				nodeId = nodes.find((n) => n.transport === 'local')?.id ?? nodes[0]?.id ?? '';
			}).catch(() => {});
		}
	});

	const tpl = $derived(templates?.find((t) => t.id === templateId));
	const rtId = $derived(source === 'template' ? (tpl?.runtime ?? '') : runtime);
	const rt = $derived(runtimes.find((r) => r.id === rtId));
	const required = $derived<TemplateEnv[]>(source === 'template' ? (tpl?.env ?? []) : planEnv);
	const effMemory = $derived(memoryMiB && memoryMiB > 0 ? Math.round(memoryMiB * MiB) : (rt?.default_memory_bytes ?? 0));
	const effCpu = $derived(cpus && cpus > 0 ? Math.round(cpus * 1e9) : (rt?.default_nano_cpus ?? 0));
	const buildMemory = $derived(source === 'template' ? (tpl?.build_memory_bytes ?? 0) : rt?.has_build || buildCommand.trim() ? Math.max(rt?.build_memory_bytes ?? 0, effMemory) : 0);
	const missingRequired = $derived(required.filter((v) => v.required && !(env[v.name] ?? '').trim()));
	const chosenAddons = $derived(addonKinds.filter((k) => addonSel.includes(k.id)));
	const addonMemory = $derived(chosenAddons.reduce((n, k) => n + k.default_memory_bytes, 0));
	const addonVars = $derived(chosenAddons.flatMap((k) => k.variables));
	const githubReady = $derived(source !== 'github' || (ghMode === 'url' ? !!lookup : !!repo.full_name));

	async function loadRepos() {
		if (repos || !gh?.linked) return;
		try {
			repos = (await api<{ repos: GitHubRepo[] }>('GET', '/me/github/repos')).repos;
		} catch (e) {
			loadError = e instanceof ApiError ? e.message : 'Your repositories could not be loaded.';
			repos = [];
		}
	}
	async function pickRepo() {
		branches = [];
		analysis = null;
		repo.branch = repos?.find((r) => r.full_name === repo.full_name)?.default_branch ?? '';
		if (!nameTouched) name = repo.full_name.split('/')[1] ?? '';
		if (!repo.full_name) return;
		try {
			branches = (await api<{ branches: string[] }>('GET', `/me/github/branches?repo=${encodeURIComponent(repo.full_name)}`)).branches;
		} catch (e) {
			problems.repo = e instanceof ApiError ? e.message : 'Branches could not be loaded.';
		}
	}
	async function lookUp(e?: Event) {
		e?.preventDefault();
		const v = repoInput.trim();
		if (!v) {
			problems = { ...problems, repo: 'Paste a GitHub link or owner/name.' };
			return;
		}
		lookingUp = true;
		problems = {};
		analysis = null;
		analyzeError = '';
		try {
			const r = await api<RepoLookup>('GET', `/github/lookup?repo=${encodeURIComponent(v)}`);
			lookup = r;
			repo.full_name = r.repo.full_name;
			repo.branch = r.branch;
			repo.root_dir = r.root_dir;
			branches = r.branches;
			if (!nameTouched) name = r.repo.full_name.split('/')[1] ?? '';
			const known = recipes.find((x) => x.repo.toLowerCase() === r.repo.full_name.toLowerCase());
			if (known && !r.root_dir) applyPlan(known.plan);
			else if (r.repo.language) guessRuntime(r.repo.language);
		} catch (err) {
			lookup = null;
			problems = { ...problems, repo: err instanceof ApiError ? err.message : 'The repository could not be found.' };
		} finally {
			lookingUp = false;
		}
	}
	function guessRuntime(lang: string) {
		const m: Record<string, string> = { JavaScript: 'nodejs', TypeScript: 'nodejs', Python: 'python', Go: 'go', Rust: 'rust', Java: 'java', Kotlin: 'java', Ruby: 'ruby' };
		if (m[lang] && runtimes.some((r) => r.id === m[lang])) runtime = m[lang];
	}
	function useRecipe(r: Recipe) {
		ghMode = 'url';
		repoInput = r.repo;
		lookUp();
	}
	async function analyze() {
		if (!repo.full_name) return;
		analyzing = true;
		analyzeError = '';
		try {
			analysis = await api<Analysis>('POST', '/github/analyze', {
				repo: repo.full_name,
				branch: repo.branch,
				root_dir: repo.root_dir,
				ai: useAI && session.features.ai
			});
			applyPlan(analysis.plan);
			if (analysis.ai.error) analyzeError = `The AI could not refine the result (${analysis.ai.error}); the detected settings are shown.`;
		} catch (err) {
			analyzeError = err instanceof ApiError ? err.message : 'The repository could not be analyzed.';
		} finally {
			analyzing = false;
		}
	}
	/** Prefills the form from a plan; the person reviews every value. */
	function applyPlan(p: Plan) {
		if (p.runtime && runtimes.some((r) => r.id === p.runtime)) runtime = p.runtime;
		command = p.argv?.length ? joinArgs(p.argv) : '';
		buildCommand = p.build_command ?? '';
		addonSel = (p.addons ?? []).filter((a) => addonKinds.length === 0 || addonKinds.some((k) => k.id === a));
		planEnv = (p.env ?? []).map((v) => ({ name: v.name, label: '', description: v.description, secret: v.secret, required: v.required, default: v.value }));
		for (const v of p.env ?? []) if (v.value && !(env[v.name] ?? '').trim()) env[v.name] = v.value;
		memoryMiB = p.memory_bytes ? Math.round(p.memory_bytes / MiB) : null;
		cpus = p.nano_cpus ? p.nano_cpus / 1e9 : null;
		showAdvanced = !!(buildCommand || command || addonSel.length);
	}
	function toggleAddon(id: string) {
		addonSel = addonSel.includes(id) ? addonSel.filter((a) => a !== id) : [...addonSel, id];
	}
	function pickTemplate(t: Template) {
		templateId = t.id;
		if (!nameTouched) name = t.name.replace(/ starter$/i, '').replace(/ \(.*\)$/, '') + ' bot';
		for (const v of t.env) if (v.default && !(v.name in env)) env[v.name] = v.default;
		memoryMiB = null;
		problems = {};
	}
	function chooseSource(s: Source) {
		source = s;
		const chosen = nodes.find((n) => n.id === nodeId);
		if (chosen?.transport === 'agent' && (s === 'template' || s === 'github') && !chosen.agent?.connected) nodeId = nodes.find((n) => n.transport === 'local')?.id ?? '';
		problems = {};
		if (s === 'github' && gh?.linked && ghMode === 'mine') loadRepos();
	}
	function chooseMode(m: 'url' | 'mine') {
		ghMode = m;
		problems = {};
		analysis = null;
		if (m === 'mine') {
			lookup = null;
			repo.full_name = '';
			loadRepos();
		} else if (lookup) {
			repo.full_name = lookup.repo.full_name;
		}
	}

	function validate(i: number): boolean {
		const p: Record<string, string> = {};
		if (i === 1) {
			if (source === 'template' && !templateId) p.template = 'Choose a template.';
			if (source === 'github') {
				if (ghMode === 'url' && !lookup) p.repo = 'Paste a repository link and press Look up.';
				else if (ghMode === 'mine' && !gh?.linked) p.repo = 'Connect GitHub first, or paste a public repository link.';
				else if (!repo.full_name) p.repo = 'Choose a repository.';
				else if (!repo.branch) p.branch = 'Choose a branch.';
			}
			if (command.trim() && splitArgs(command).length === 0) p.command = 'Enter a program and its arguments.';
			if (!name.trim()) p.name = 'Give the bot a name.';
			else if (name.trim().length > 64) p.name = 'Use at most 64 characters.';
		}
		if (i === 2) {
			for (const v of missingRequired) p['env.' + v.name] = `${v.label || v.name} is required to run this ${source === 'template' ? 'template' : 'bot'}.`;
			const seen = new Set(required.map((v) => v.name));
			extra.forEach((x, n) => {
				if (!x.name && !x.value) return;
				if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(x.name)) p['extra.' + n] = 'Use letters, digits and underscores; start with a letter.';
				else if (/^(RIVET_|LD_)|^PATH$/i.test(x.name)) p['extra.' + n] = 'This name is reserved by the panel.';
				else if (seen.has(x.name)) p['extra.' + n] = 'This variable is already listed.';
				seen.add(x.name);
			});
		}
		if (i === 3 && limits) {
			if (effMemory < Math.max(limits.min_memory_bytes, rt?.min_memory_bytes ?? 0) || effMemory > limits.max_memory_bytes)
				p.memory = `Choose between ${fmtBytes(Math.max(limits.min_memory_bytes, rt?.min_memory_bytes ?? 0))} and ${fmtBytes(limits.max_memory_bytes)}.`;
			if (effCpu < limits.min_nano_cpus || effCpu > limits.max_nano_cpus)
				p.cpu = `Choose between ${limits.min_nano_cpus / 1e9} and ${limits.max_nano_cpus / 1e9} CPU.`;
		}
		problems = p;
		return Object.keys(p).length === 0;
	}

	async function go(to: number) {
		if (to > step) for (let i = step; i < to; i++) if (!validate(i)) return focusProblem();
		problems = {};
		step = to;
		await Promise.resolve();
		heading?.focus();
	}
	async function focusProblem() {
		await Promise.resolve();
		document.querySelector<HTMLElement>('[aria-invalid="true"], [data-problem]')?.focus();
	}

	async function create() {
		for (let i = 1; i <= 3; i++)
			if (!validate(i)) {
				step = i;
				return focusProblem();
			}
		busy = true;
		submitError = '';
		const vars: Record<string, string> = {};
		for (const [k, v] of Object.entries(env)) if (v.trim()) vars[k] = v;
		for (const x of extra) if (x.name) vars[x.name] = x.value;
		const body: Record<string, unknown> = { name: name.trim(), runtime: rtId };
		if (memoryMiB && memoryMiB > 0) body.memory_bytes = effMemory;
		if (cpus && cpus > 0) body.nano_cpus = effCpu;
		if (Object.keys(vars).length) body.env = vars;
		if (source === 'template') body.template_id = templateId;
		if (source !== 'template') {
			if (command.trim()) body.argv = splitArgs(command);
			if (buildCommand.trim()) body.build_command = buildCommand;
			if (addonSel.length) body.addons = addonSel.map((kind) => ({ kind }));
			if (analysis?.plan.pids_limit) body.pids_limit = analysis.plan.pids_limit;
		}
		if (source === 'github') body.github = { ...repo, start_after_deploy: startNow && canStart };
		if (workspaceId) body.workspace_id = workspaceId;
		if (nodeId) body.node_id = nodeId;
		try {
			const b = await api<Bot>('POST', '/bots', body);
			if (startNow && canStart && source !== 'github') {
				try {
					await api('POST', `/bots/${b.id}/start`);
				} catch (e) {
					toast(`Created ${b.name}, but it could not be started: ${e instanceof ApiError ? e.message : 'request failed'}`, 'warn');
				}
			}
			toast(source === 'github' ? `Created ${b.name}; the first deployment is running` : `Created ${b.name}`);
			await goto(`/bots/${b.id}?tab=overview`);
		} catch (e) {
			// Keep everything the user entered so they can correct it.
			submitError = e instanceof ApiError ? e.message : 'The bot could not be created. Check your connection and try again.';
			busy = false;
		}
	}

	const canStart = $derived(session.features.runner && source !== 'blank' && missingRequired.length === 0);
	const sourceLabel = $derived(source === 'template' ? (tpl?.name ?? 'Template') : source === 'github' ? 'GitHub repository' : 'Empty workspace');
	const secretLike = (n: string) => /token|secret|key|password|pass|auth/i.test(n);
	const sourceBadge: Record<string, string> = { recipe: 'Verified recipe', detected: 'Detected from files', ai: 'Refined by AI' };
</script>

<svelte:head><title>New bot · RivetPanel</title></svelte:head>

<div class="mx-auto max-w-3xl">
	<a href="/dashboard" class="inline-flex items-center gap-1 text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />Bots</a>
	<h1 class="mt-1 text-page">New bot</h1>

	<ol class="mt-5 hidden grid-cols-4 gap-2 sm:grid" aria-label="Steps">
		{#each steps as s, i (s)}
			<li>
				<button
					class="w-full border-t-[3px] pt-2 text-left {i === step ? 'border-action' : i < step ? 'border-ink/60' : 'border-rule'}"
					aria-current={i === step ? 'step' : undefined}
					disabled={i > step}
					onclick={() => go(i)}
				>
					<span class="block text-small text-muted">Step {i + 1}</span>
					<span class="font-medium {i === step ? '' : i < step ? 'text-ink/80' : 'text-muted'}">{s}</span>
				</button>
			</li>
		{/each}
	</ol>
	<p class="mt-4 text-muted sm:hidden">Step {step + 1} of 4</p>

	<section class="mt-6" aria-labelledby="step-h">
		<h2 id="step-h" bind:this={heading} tabindex="-1" class="text-section outline-none">
			{['Where does the code come from?', source === 'template' ? 'Choose a template and name the bot' : source === 'github' ? 'Choose a repository' : 'Choose a language and name the bot', 'Add the values the bot needs', 'Review and create'][step]}
		</h2>

		{#if loadError}<Notice tone="fail" class="mt-3">{loadError}</Notice>{/if}

		{#if step === 0}
			<div class="mt-4 grid gap-3" role="radiogroup" aria-label="Source">
				{#each [{ id: 'template', title: 'Start from a template', body: 'A working slash-command bot in JavaScript, Python, Rust, Java or Go. Best for a first bot: you only add the token.', icon: 'box' }, { id: 'github', title: 'Deploy from GitHub', body: 'Paste a link to any public repository, or pick one of your own. The panel can work out the language, commands, variables and databases, and redeploys when the branch changes.', icon: 'github' }, { id: 'blank', title: 'Empty bot', body: 'Choose a language and upload your code through Files or SFTP.', icon: 'folder' }] as o (o.id)}
					<button
						role="radio"
						aria-checked={source === o.id}
						class="flex items-start gap-3 rounded-tile border bg-panel p-4 text-left transition-colors {source === o.id ? 'border-action ring-1 ring-action' : 'border-rule-soft hover:border-rule'}"
						onclick={() => chooseSource(o.id as Source)}
					>
						<span class="mt-0.5 grid size-8 shrink-0 place-items-center {source === o.id ? 'bg-action text-white' : 'bg-paper text-ink'}"><Icon name={o.icon as 'box'} /></span>
						<span>
							<span class="block text-title font-semibold">{o.title}{#if o.id === 'template'}<span class="ml-2 text-small font-normal text-run">Recommended</span>{/if}</span>
							<span class="mt-0.5 block text-muted">{o.body}</span>
						</span>
					</button>
				{/each}
			</div>
		{:else if step === 1}
			{#if source === 'template'}
				{#if templates === null}
					<div class="mt-4"><Skeleton rows={3} /></div>
				{:else}
					<div class="mt-4 grid gap-3 sm:grid-cols-2" role="radiogroup" aria-label="Templates" aria-invalid={problems.template ? 'true' : undefined} tabindex={problems.template ? -1 : undefined} data-problem={problems.template ? '' : undefined}>
						{#each templates as t (t.id)}
							<button
								role="radio"
								aria-checked={templateId === t.id}
								class="flex flex-col rounded-tile border bg-panel p-4 text-left {templateId === t.id ? 'border-action ring-1 ring-action' : 'border-rule-soft hover:border-rule'}"
								onclick={() => pickTemplate(t)}
							>
								<span class="flex items-baseline justify-between gap-2"><span class="text-title font-semibold">{t.name}</span><span class="text-small text-muted">{t.language}</span></span>
								<span class="mt-1 text-muted">{t.description}</span>
								<span class="mt-3 text-small text-muted">
									Needs {t.env.filter((v) => v.required).map((v) => v.name).join(', ') || 'no values'}.
									{#if t.has_build}Builds with up to {fmtBytes(t.build_memory_bytes)} of memory.{/if}
								</span>
							</button>
						{/each}
					</div>
					{#if problems.template}<p class="mt-2 text-small text-fail">{problems.template}</p>{/if}
				{/if}
			{:else if source === 'github'}
				<div class="mt-4 inline-flex rounded-tile border border-rule-soft bg-panel p-0.5" role="tablist" aria-label="Repository source">
					<button role="tab" aria-selected={ghMode === 'url'} class="rounded-tile px-3 py-1.5 text-small font-medium {ghMode === 'url' ? 'bg-action text-white' : 'text-muted hover:text-ink'}" onclick={() => chooseMode('url')}>Paste a link</button>
					<button role="tab" aria-selected={ghMode === 'mine'} class="rounded-tile px-3 py-1.5 text-small font-medium {ghMode === 'mine' ? 'bg-action text-white' : 'text-muted hover:text-ink'}" onclick={() => chooseMode('mine')}>My repositories</button>
				</div>

				{#if ghMode === 'url'}
					<form class="mt-4" onsubmit={lookUp}>
						<label class="block">
							<span class="label">Repository</span>
							<span class="flex gap-2">
								<input class="field font-mono" bind:value={repoInput} placeholder="https://github.com/owner/name or owner/name" spellcheck="false" autocomplete="off" aria-invalid={problems.repo ? 'true' : undefined} />
								<button class="btn shrink-0" disabled={lookingUp}>{lookingUp ? 'Looking up…' : 'Look up'}</button>
							</span>
							<span class="help {problems.repo ? 'text-fail!' : ''}">{problems.repo || 'Any public repository works without a GitHub connection. Links to a branch or folder (…/tree/main/bot) fill those in too.'}</span>
						</label>
					</form>
					{#if recipes.length && !lookup}
						<div class="mt-3 flex flex-wrap items-center gap-2 text-small">
							<span class="text-muted">Popular open-source bots:</span>
							{#each recipes as r (r.repo)}
								<button class="btn btn-sm" title={r.description} onclick={() => useRecipe(r)}><Icon name="star" size={13} />{r.name}<span class="text-muted">{r.language}</span></button>
							{/each}
						</div>
					{/if}
				{:else if !gh?.linked}
					<Notice tone="warn" class="mt-4" title="Connect GitHub to list your repositories">
						<p><a href="/settings/connected-accounts">Open connected accounts</a> and connect GitHub, or use <button class="text-action underline" onclick={() => chooseMode('url')}>Paste a link</button> for a public repository.</p>
					</Notice>
				{:else}
					{#if !gh.repo_access}
						<Notice class="mt-4">Only public repositories are listed until you grant repository access in <a href="/settings/connected-accounts">Connected accounts</a>.</Notice>
					{/if}
					<label class="mt-4 block">
						<span class="label">Repository</span>
						<select class="field" bind:value={repo.full_name} onchange={pickRepo} aria-invalid={problems.repo ? 'true' : undefined} disabled={repos === null}>
							<option value="" disabled>{repos === null ? 'Loading repositories…' : 'Select a repository'}</option>
							{#each repos ?? [] as r (r.full_name)}<option value={r.full_name}>{r.full_name}{r.private ? ' (private)' : ''}</option>{/each}
						</select>
						{#if problems.repo}<span class="help text-fail!">{problems.repo}</span>{/if}
					</label>
				{/if}

				{#if ghMode === 'url' && lookup}
					<div class="mt-4 surface flex items-start gap-3 p-4">
						<span class="mt-0.5 grid size-8 shrink-0 place-items-center bg-paper"><Icon name="github" /></span>
						<div class="min-w-0 flex-1">
							<a class="font-semibold break-all" href={lookup.repo.html_url} target="_blank" rel="noopener noreferrer">{lookup.repo.full_name}</a>
							{#if lookup.repo.private}<span class="ml-2 text-small text-warn">private</span>{/if}
							{#if lookup.repo.archived}<span class="ml-2 text-small text-warn">archived</span>{/if}
							{#if lookup.repo.description}<p class="mt-0.5 text-muted">{lookup.repo.description}</p>{/if}
							<p class="mt-1 flex flex-wrap gap-x-3 text-small text-muted">
								{#if lookup.repo.language}<span>{lookup.repo.language}</span>{/if}
								{#if lookup.repo.stargazers_count}<span>★ {lookup.repo.stargazers_count.toLocaleString()}</span>{/if}
								{#if lookup.repo.license?.spdx_id && lookup.repo.license.spdx_id !== 'NOASSERTION'}<span>{lookup.repo.license.spdx_id}</span>{/if}
								{#if lookup.repo.pushed_at}<span>updated {new Date(lookup.repo.pushed_at).toLocaleDateString()}</span>{/if}
							</p>
						</div>
					</div>
				{/if}

				{#if githubReady && repo.full_name}
					<div class="mt-4 grid gap-4 sm:grid-cols-2">
						<label class="block">
							<span class="label">Branch</span>
							<select class="field" bind:value={repo.branch} aria-invalid={problems.branch ? 'true' : undefined} onchange={() => (analysis = null)}>
								{#if repo.branch && !branches.includes(repo.branch)}<option value={repo.branch}>{repo.branch}</option>{/if}
								{#each branches as b (b)}<option value={b}>{b}</option>{/each}
							</select>
							{#if problems.branch}<span class="help text-fail!">{problems.branch}</span>{/if}
						</label>
						<label class="block">
							<span class="label">Folder inside the repository</span>
							<input class="field font-mono" bind:value={repo.root_dir} placeholder="/" maxlength="200" onchange={() => (analysis = null)} />
							<span class="help">Leave empty to deploy the whole repository.</span>
						</label>
						<label class="flex items-start gap-2.5 sm:col-span-2">
							<input type="checkbox" class="mt-0.5" bind:checked={repo.auto_deploy} />
							<span>Deploy automatically when this branch changes<span class="help">{ghMode === 'mine' ? 'The panel adds a webhook to the repository (or checks the branch every few minutes if it cannot).' : 'The panel checks the branch for new commits every few minutes.'}</span></span>
						</label>
					</div>

					<div class="mt-5 rounded-tile border border-rule-soft bg-panel p-4">
						<div class="flex flex-wrap items-center gap-3">
							<button class="btn btn-primary" onclick={analyze} disabled={analyzing}><Icon name="sparkle" size={14} />{analyzing ? 'Analyzing…' : analysis ? 'Analyze again' : 'Analyze repository'}</button>
							{#if session.features.ai}
								<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={useAI} />Refine with AI</label>
							{/if}
							<span class="text-small text-muted">Works out the language, start and build commands, variables, databases and resources.</span>
						</div>
						{#if analyzing}<p class="mt-2 text-small text-muted" aria-live="polite">Downloading the branch and reading its files{useAI && session.features.ai ? ', then asking the AI' : ''}…</p>{/if}
						{#if analyzeError}<Notice tone="warn" class="mt-3" live>{analyzeError}</Notice>{/if}
						{#if analysis && useAI && !analysis.ai.available && analysis.plan.source !== 'recipe'}
							<p class="mt-2 text-small text-muted">No AI provider is set up, so only file detection was used. An administrator can add one under Administration → AI.</p>
						{/if}
						{#if analysis}
							{@const p = analysis.plan}
							<div class="mt-4 border-t border-rule-soft pt-3" aria-live="polite">
								<p class="flex flex-wrap items-center gap-2">
									<span class="rounded-tile px-2 py-0.5 text-small font-medium {p.source === 'recipe' ? 'bg-run/15 text-run' : p.source === 'ai' ? 'bg-action/15 text-action' : 'bg-paper text-ink'}">{sourceBadge[p.source]}</span>
									<span class="text-small text-muted">Confidence: {p.confidence}{analysis.ai.used && analysis.ai.model ? ` · ${analysis.ai.model}` : ''} · {analysis.files.toLocaleString()} files{analysis.truncated ? ' (partly inspected)' : ''}</span>
								</p>
								{#if p.summary}<p class="mt-2">{p.summary}</p>{/if}
								<p class="mt-2 text-small text-muted">Applied below: {runtimes.find((r) => r.id === p.runtime)?.display_name ?? 'no language'}{p.argv?.length ? ', start command' : ''}{p.build_command ? ', build command' : ''}{p.addons.length ? `, ${p.addons.join(' + ')}` : ''}{p.env.length ? `, ${p.env.length} variables (next step)` : ''}{p.memory_bytes ? `, ${fmtBytes(p.memory_bytes)} memory` : ''}. Review them before creating the bot.</p>
								{#if p.setup?.length}
									<h3 class="mt-3 text-small font-semibold">Before the first start</h3>
									<ol class="mt-1 list-decimal space-y-0.5 pl-5 text-small">{#each p.setup as s, i (i)}<li>{s}</li>{/each}</ol>
								{/if}
								{#if p.notes?.length}
									<ul class="mt-3 list-disc space-y-0.5 pl-5 text-small text-muted">{#each p.notes as n, i (i)}<li>{n}</li>{/each}</ul>
								{/if}
								{#if p.evidence?.length}<p class="mt-2 text-small text-muted">Based on {p.evidence.slice(0, 8).join(', ')}.</p>{/if}
							</div>
						{/if}
					</div>
				{/if}
			{/if}

			{#if source !== 'template'}
				<label class="mt-5 block max-w-xs">
					<span class="label">Language</span>
					<select class="field" bind:value={runtime}>{#each runtimes as r (r.id)}<option value={r.id}>{r.display_name}</option>{/each}</select>
				</label>
				<div class="mt-4">
					<button type="button" class="inline-flex items-center gap-1 text-small font-medium text-action" aria-expanded={showAdvanced} onclick={() => (showAdvanced = !showAdvanced)}>
						<Icon name={showAdvanced ? 'chevronDown' : 'chevronRight'} size={14} />Start, build and databases
					</button>
					{#if showAdvanced}
						<div class="mt-3 grid gap-4">
							<label class="block">
								<span class="label">Start command</span>
								<input class="field font-mono text-[13px]" bind:value={command} spellcheck="false" placeholder={rt ? joinArgs(rt.default_argv) : ''} aria-invalid={problems.command ? 'true' : undefined} />
								<span class="help {problems.command ? 'text-fail!' : ''}">{problems.command || 'Program and arguments, run directly (no shell). Leave empty for the default shown.'}</span>
							</label>
							<label class="block">
								<span class="label">Build command <span class="font-normal text-muted">(optional)</span></span>
								<textarea class="field min-h-24 font-mono text-[13px]" bind:value={buildCommand} spellcheck="false" maxlength="4096" placeholder={rt?.has_build ? 'Empty: the default build for ' + rt.display_name : 'Shell commands, one per line'}></textarea>
								<span class="help">Runs with <code>sh</code> in the workspace before each start, with internet access but without the bot's variables. {#if analysis?.plan.build_command}Suggested from the repository: check it before creating the bot.{/if}</span>
							</label>
							{#if addonKinds.length}
								<fieldset>
									<legend class="label">Databases</legend>
									<div class="grid gap-2 sm:grid-cols-2">
										{#each addonKinds as k (k.id)}
											<label class="flex items-start gap-2.5 rounded-tile border p-3 {addonSel.includes(k.id) ? 'border-action' : 'border-rule-soft'}">
												<input type="checkbox" class="mt-0.5" checked={addonSel.includes(k.id)} onchange={() => toggleAddon(k.id)} />
												<span><span class="font-medium">{k.display_name}</span> <span class="text-small text-muted">· {fmtBytes(k.default_memory_bytes)}</span><span class="help">Host <code>{k.id}</code>; sets {k.variables.slice(0, 3).join(', ')}…</span></span>
											</label>
										{/each}
									</div>
									<span class="help">Private to this bot, without internet access; started before the bot. Their memory counts toward your limits.</span>
								</fieldset>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
			<label class="mt-5 block max-w-md">
				<span class="label">Name</span>
				<input class="field" maxlength="64" bind:value={name} oninput={() => (nameTouched = true)} placeholder="Music bot" aria-invalid={problems.name ? 'true' : undefined} aria-describedby="name-help" />
				<span id="name-help" class="help {problems.name ? 'text-fail!' : ''}">{problems.name || 'Only members of its workspace and people you share the bot with see this name.'}</span>
			</label>
			{#if targets.length > 1}
				<label class="mt-4 block max-w-md">
					<span class="label">Workspace</span>
					<select class="field" bind:value={workspaceId}>
						{#each targets as w (w.id)}<option value={w.personal ? '' : w.id}>{w.personal ? 'Personal' : w.name}</option>{/each}
					</select>
					<span class="help">Members of the workspace get access according to their role.</span>
				</label>
			{/if}
			{#if nodes.length > 1}
				<label class="mt-4 block max-w-md">
					<span class="label">Node</span>
					<select class="field" bind:value={nodeId}>
						{#each nodes as n (n.id)}<option value={n.id} disabled={n.transport === 'agent' && (source === 'template' || source === 'github') && !n.agent?.connected}>{n.name}{n.transport === 'agent' ? n.agent?.connected ? ' · connected' : ' · offline' : ' · local'}</option>{/each}
					</select>
					<span class="help">Template and GitHub bots need a connected node: template files are written to that node in one transaction, and GitHub repositories are downloaded by the panel and streamed to it. Empty bots can be created on any eligible node and filled through Files.</span>
				</label>
			{/if}
		{:else if step === 2}
			{#if tpl && source === 'template'}
				<div class="mt-4 surface p-4">
					<h3 class="font-semibold">Before the first start</h3>
					<ol class="mt-2 list-decimal space-y-1 pl-5 text-ink/90">
						{#each tpl.setup as s, i (i)}<li>{s}</li>{/each}
					</ol>
					{#if tpl.privileged_intents.length}
						<p class="mt-2 text-muted">Enable these privileged intents on the Bot page: {tpl.privileged_intents.join(', ')}.</p>
					{:else}
						<p class="mt-2 text-small text-muted">This template needs no privileged gateway intents.</p>
					{/if}
				</div>
			{:else if analysis?.plan.setup?.length}
				<div class="mt-4 surface p-4">
					<h3 class="font-semibold">Before the first start</h3>
					<ol class="mt-2 list-decimal space-y-1 pl-5 text-ink/90">{#each analysis.plan.setup as s, i (i)}<li>{s}</li>{/each}</ol>
				</div>
			{/if}
			{#if addonVars.length}
				<Notice class="mt-4" title="Set automatically by the databases">
					The bot receives {addonVars.join(', ')}. Use them in your own values as <code>{'${NAME}'}</code>, for example <code>{'${' + addonVars[0] + '}'}</code>.
				</Notice>
			{/if}
			<div class="mt-5 grid gap-4">
				{#each required as v (v.name)}
					<label class="block">
						<span class="label">{v.label || v.name} {#if v.label}<code class="ml-1 text-small font-normal text-muted">{v.name}</code>{/if}{#if !v.required}<span class="ml-1 font-normal text-muted">(optional)</span>{/if}</span>
						<span class="relative block">
							<input
								class="field pr-10 font-mono"
								type={v.secret && !shown[v.name] ? 'password' : 'text'}
								bind:value={env[v.name]}
								autocomplete="off"
								spellcheck="false"
								aria-invalid={problems['env.' + v.name] ? 'true' : undefined}
								aria-describedby="env-{v.name}-help"
							/>
							{#if v.secret}
								<button type="button" class="btn btn-quiet btn-icon btn-sm absolute top-1/2 right-1 -translate-y-1/2" aria-label={shown[v.name] ? `Hide ${v.name}` : `Show ${v.name}`} aria-pressed={!!shown[v.name]} onclick={() => (shown[v.name] = !shown[v.name])}>
									<Icon name={shown[v.name] ? 'eyeOff' : 'eye'} />
								</button>
							{/if}
						</span>
						<span id="env-{v.name}-help" class="help {problems['env.' + v.name] ? 'text-fail!' : ''}">{problems['env.' + v.name] || v.description}</span>
					</label>
				{/each}
				{#if source !== 'template'}
					<p class="text-muted">{required.length ? 'Add any other variables your code reads.' : 'Add the variables your code reads, such as DISCORD_TOKEN.'} You can also add them later under Environment.</p>
				{/if}
				{#each extra as x, i (i)}
					<div class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)_auto] sm:items-start">
						<label class="block"><span class="sr-only">Variable name</span><input class="field font-mono" placeholder="NAME" bind:value={x.name} autocomplete="off" spellcheck="false" aria-invalid={problems['extra.' + i] ? 'true' : undefined} /></label>
						<label class="block"><span class="sr-only">Value of {x.name || 'variable'}</span><input class="field font-mono" placeholder="value" type={secretLike(x.name) ? 'password' : 'text'} bind:value={x.value} autocomplete="off" spellcheck="false" /></label>
						<button type="button" class="btn btn-quiet" onclick={() => (extra = extra.filter((_, j) => j !== i))}>Remove</button>
						{#if problems['extra.' + i]}<p class="text-small text-fail sm:col-span-3">{problems['extra.' + i]}</p>{/if}
					</div>
				{/each}
				<div><button type="button" class="btn" onclick={() => (extra = [...extra, { name: extra.length || source === 'template' || required.length ? '' : 'DISCORD_TOKEN', value: '' }])}><Icon name="plus" />Add variable</button></div>
				<p class="text-small text-muted">Values are sent once over this connection and stored encrypted. They are never put in the page address or saved in this browser.</p>
			</div>
		{:else}
			<dl class="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 border-y border-rule-soft py-3">
				<dt class="text-muted">Name</dt><dd class="font-medium">{name}</dd>
				{#if targets.length > 1}<dt class="text-muted">Workspace</dt><dd>{targets.find((w) => (w.personal ? '' : w.id) === workspaceId)?.name ?? 'Personal'}</dd>{/if}
				{#if nodes.length > 1}<dt class="text-muted">Node</dt><dd>{nodes.find((n) => n.id === nodeId)?.name ?? 'Local'}</dd>{/if}
				<dt class="text-muted">Source</dt><dd>{sourceLabel}{#if source === 'github'}: <code>{repo.full_name}</code> on <code>{repo.branch}</code>{#if repo.root_dir}, folder <code>{repo.root_dir}</code>{/if}{/if}</dd>
				<dt class="text-muted">Language</dt><dd>{rt?.display_name ?? rtId}</dd>
				{#if source !== 'template'}
					<dt class="text-muted">Start</dt><dd><code class="break-all">{command.trim() || joinArgs(rt?.default_argv ?? [])}</code></dd>
					<dt class="text-muted">Build</dt><dd>{#if buildCommand.trim()}<pre class="font-mono text-[12px] whitespace-pre-wrap">{buildCommand.trim()}</pre>{:else}Default for {rt?.display_name ?? 'the language'}{/if}</dd>
					{#if chosenAddons.length}<dt class="text-muted">Databases</dt><dd>{chosenAddons.map((k) => `${k.display_name} (${fmtBytes(k.default_memory_bytes)})`).join(', ')}</dd>{/if}
				{/if}
				<dt class="text-muted">Variables</dt>
				<dd>{Object.keys(env).filter((k) => env[k]?.trim()).concat(extra.filter((x) => x.name).map((x) => x.name)).join(', ') || 'None yet'}</dd>
			</dl>
			<div class="mt-5 grid gap-4 sm:grid-cols-2">
				<label class="block">
					<span class="label">Memory limit</span>
					<span class="flex items-center gap-2"><input class="field" type="number" step="any" min="0" bind:value={memoryMiB} placeholder={rt ? String(Math.round(rt.default_memory_bytes / MiB)) : ''} aria-invalid={problems.memory ? 'true' : undefined} /><span class="text-muted">MiB</span></span>
					<span class="help {problems.memory ? 'text-fail!' : ''}">{problems.memory || `Default ${fmtBytes(rt?.default_memory_bytes ?? 0)}. The process is stopped if it uses more.`}</span>
				</label>
				<label class="block">
					<span class="label">CPU limit</span>
					<span class="flex items-center gap-2"><input class="field" type="number" step="any" min="0" bind:value={cpus} placeholder={rt ? String(rt.default_nano_cpus / 1e9) : ''} aria-invalid={problems.cpu ? 'true' : undefined} /><span class="text-muted">cores</span></span>
					<span class="help {problems.cpu ? 'text-fail!' : ''}">{problems.cpu || `Default ${(rt?.default_nano_cpus ?? 0) / 1e9} cores.`}</span>
				</label>
			</div>
			{#if addonMemory > 0}
				<p class="mt-3 text-small text-muted">Total reserved with databases: {fmtBytes(effMemory + addonMemory)}.</p>
			{/if}
			{#if buildMemory > 0}
				<Notice class="mt-4" title="Build memory is separate">
					The build runs in its own container that can use up to {fmtBytes(buildMemory)} of memory{#if buildMemory > effMemory}, more than the {fmtBytes(effMemory)} the bot itself gets{/if}. {source === 'template' ? (tpl?.first_start ?? '') : ''}
				</Notice>
			{/if}
			{#if canStart}
				<label class="mt-5 flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={startNow} /><span>{source === 'github' ? 'Start the bot after the first deployment' : 'Start the bot after creating it'}<span class="help">You can follow the {source === 'github' ? 'deployment and ' : ''}build on its overview page.</span></span></label>
			{:else if !session.features.runner}
				<Notice class="mt-5">This panel runs without Docker, so the bot is created but cannot be started here.</Notice>
			{:else if source !== 'blank'}
				<Notice tone="warn" class="mt-5">The bot is created stopped because required values are missing.</Notice>
			{/if}
			{#if submitError}<Notice tone="fail" class="mt-4" live>{submitError}</Notice>{/if}
		{/if}
	</section>

	<div class="sticky bottom-0 mt-8 flex items-center gap-2 border-t border-rule-soft bg-paper/95 py-3 backdrop-blur-sm">
		{#if step > 0}<button class="btn" onclick={() => go(step - 1)}><Icon name="chevronLeft" size={14} />Back</button>{/if}
		<a href="/dashboard" class="btn btn-quiet">Cancel</a>
		<span class="flex-1"></span>
		{#if step < 3}
			<button class="btn btn-primary" onclick={() => go(step + 1)}>Continue<Icon name="chevronRight" size={14} /></button>
		{:else}
			<button class="btn btn-primary" onclick={create} disabled={busy}>{busy ? 'Creating…' : startNow && canStart ? (source === 'github' ? 'Create, deploy and start' : 'Create and start') : 'Create bot'}</button>
		{/if}
	</div>
</div>
