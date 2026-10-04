<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Bot, Limits, NodeInfo, RuntimeInfo } from '$lib/api/types';
	import type { Blueprint, BlueprintVariable, Version } from '$lib/api/games';
	import { checkVariable } from '$lib/api/games';
	import { gameBlurb } from '$lib/gameIcons';
	import { creatable, loadWorkspaces, workspaces } from '$lib/workspaces.svelte';
	import { can, session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import GameIcon from '$lib/components/ui/GameIcon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';

	// A guided flow like New bot: game → version and game settings → name,
	// size and placement → review (licence terms) and create.
	const steps = ['Game', 'Version', 'Server', 'Review'];
	let step = $state(0);
	let heading: HTMLHeadingElement | undefined = $state();

	let blueprints = $state<Blueprint[] | null>(null);
	let limits = $state<Limits | null>(null);
	let loadError = $state('');
	let chosen = $state<Blueprint | null>(null);
	let name = $state('');
	let nameTouched = $state(false);
	let values = $state<Record<string, string>>({});
	let versions = $state<Record<string, Version[] | 'error'>>({});
	let unstable = $state(false);
	let memoryMiB = $state(2048);
	let cpus = $state(2);
	let image = $state('');
	let agreed = $state<Record<string, boolean>>({});
	let workspaceID = $state('');
	let startNow = $state(true);
	let nodes = $state<NodeInfo[]>([]);
	let nodeID = $state('');
	let busy = $state(false);
	let submitError = $state('');
	let moreOptions = $state(false);
	let shown = $state<Record<string, boolean>>({});
	let problems = $state<Record<string, string>>({});

	// Picker: search and a family filter (?family=minecraft from the "New" menu).
	let q = $state('');
	let family = $state(page.url.searchParams.get('family') ?? '');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed. Check your connection and try again.');

	async function load() {
		loadError = '';
		blueprints = null;
		loadWorkspaces();
		try {
			const [b, r] = await Promise.all([api<{ blueprints: Blueprint[] }>('GET', '/blueprints'), api<{ runtimes: RuntimeInfo[]; limits: Limits }>('GET', '/runtimes')]);
			blueprints = b.blueprints.filter((x) => x.enabled);
			limits = r.limits;
			const pre = blueprints.find((x) => x.slug === page.url.searchParams.get('type'));
			if (pre) {
				choose(pre);
				step = 1;
			}
		} catch (e) {
			loadError = msg(e);
			blueprints = [];
		}
		if (session.user?.role === 'admin' && session.features.agents) {
			api<{ nodes: NodeInfo[] }>('GET', '/nodes')
				.then((r) => {
					nodes = r.nodes.filter((n) => n.enabled && !n.draining);
					nodeID = nodes.find((n) => n.transport === 'local')?.id ?? nodes[0]?.id ?? '';
				})
				.catch(() => {});
		}
	}
	onMount(load);

	// The most common choices first, then by name.
	const preferred = ['minecraft-paper', 'minecraft-vanilla', 'minecraft-fabric', 'minecraft-forge', 'minecraft-neoforge', 'minecraft-purpur', 'minecraft-folia', 'minecraft-velocity', 'steam-valheim', 'steam-rust', 'steam-project-zomboid'];
	const rank = (b: Blueprint) => preferred.indexOf(b.slug) + 1 || 99;
	const familyOf = (b: Blueprint) => (b.category.toLowerCase().includes('minecraft') ? 'minecraft' : b.category.toLowerCase().includes('steam') ? 'steam' : 'other');
	const families = $derived.by(() => {
		const seen = new Set((blueprints ?? []).map(familyOf));
		return [
			{ id: '', label: 'All games' },
			...(seen.has('minecraft') ? [{ id: 'minecraft', label: 'Minecraft' }] : []),
			...(seen.has('steam') ? [{ id: 'steam', label: 'Steam' }] : []),
			...(seen.has('other') ? [{ id: 'other', label: 'Other' }] : [])
		];
	});
	const categories = $derived.by(() => {
		const needle = q.trim().toLowerCase();
		const out: Record<string, Blueprint[]> = {};
		for (const b of [...(blueprints ?? [])].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))) {
			if (family && familyOf(b) !== family) continue;
			if (needle && !`${b.name} ${b.category} ${b.description} ${b.slug}`.toLowerCase().includes(needle)) continue;
			(out[b.category] ??= []).push(b);
		}
		return Object.entries(out);
	});

	const maxMiB = $derived(limits ? Math.floor(limits.max_memory_bytes / 1048576) : 16384);
	const minMiB = (b: Blueprint | null) => Math.max(limits ? Math.ceil(limits.min_memory_bytes / 1048576) : 64, b?.spec.resources.min_memory_mb ?? 0);
	const tooBig = (b: Blueprint) => minMiB(b) > maxMiB;
	const minCpu = $derived(limits ? limits.min_nano_cpus / 1e9 : 0.5);
	const maxCpu = $derived(limits ? limits.max_nano_cpus / 1e9 : 4);
	const isJava = $derived(!!chosen?.spec.images.some((i) => i.java));
	const isSteam = $derived(!!chosen?.spec.install?.steamcmd);

	// Variables: the version up front; for games without a version list, the
	// everyday settings up front and the rest under "More options".
	const editable = $derived((chosen?.spec.variables ?? []).filter((v) => v.editable));
	const versionVar = $derived(editable.find((v) => v.versions));
	const mainVars = $derived(versionVar ? [] : editable.filter((v) => !v.reinstall));
	const extraVars = $derived(editable.filter((v) => v !== versionVar && !mainVars.includes(v)));
	const versionList = $derived.by(() => {
		const l = versionVar ? versions[versionVar.env] : undefined;
		return Array.isArray(l) ? (unstable ? l : l.filter((v) => v.stable)) : l;
	});
	const hasUnstable = $derived.by(() => {
		const l = versionVar ? versions[versionVar.env] : undefined;
		return Array.isArray(l) && l.some((v) => !v.stable);
	});
	const latestStable = $derived.by(() => {
		const l = versionVar ? versions[versionVar.env] : undefined;
		return Array.isArray(l) ? (l.find((v) => v.stable)?.id ?? l[0]?.id ?? '') : '';
	});
	// Settings the type's rules allow empty but the game itself refuses to start without.
	const required: Record<string, string[]> = { 'steam-valheim': ['SERVER_PASSWORD'] };
	const secretVar = (v: BlueprintVariable) => /PASSWORD|SECRET|TOKEN/i.test(v.env);

	const presets = $derived.by(() => {
		if (!chosen) return [];
		const lo = minMiB(chosen);
		const set = new Set([1024, 2048, 3072, 4096, 6144, 8192, 12288, 16384, chosen.spec.resources.memory_mb].filter((m) => m >= lo && m <= maxMiB));
		return [...set].sort((a, b) => a - b).slice(0, 6);
	});
	const portText = $derived.by(() => {
		const p = chosen?.spec.ports;
		if (!p) return '';
		const extra = p.extra ?? 0;
		const main = p.default ? `usually ${p.default}` : 'a free one';
		return extra
			? `RivetPanel reserves a game port (${main}) plus ${extra} more${p.contiguous ? ' right after it' : ''} for queries, automatically.`
			: `RivetPanel reserves a free port for players (${main}) automatically. You can add more ports later under Network.`;
	});

	function choose(b: Blueprint) {
		if (chosen?.id === b.id) return;
		chosen = b;
		values = Object.fromEntries(b.spec.variables.map((v) => [v.env, v.default]));
		agreed = {};
		image = '';
		unstable = false;
		moreOptions = false;
		problems = {};
		memoryMiB = Math.max(minMiB(b), Math.min(b.spec.resources.memory_mb, maxMiB));
		cpus = Math.max(minCpu, Math.min(b.spec.resources.cpus, maxCpu));
		if (!nameTouched) name = `${b.name} server`;
		workspaceID = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
		for (const v of b.spec.variables) if (v.versions) loadVersions(b, v);
	}
	async function loadVersions(b: Blueprint, v: BlueprintVariable) {
		delete versions[v.env];
		try {
			const list = (await api<{ versions: Version[] }>('GET', `/blueprints/${b.slug}/versions/${v.env}`)).versions;
			if (chosen?.id === b.id) versions[v.env] = list;
		} catch {
			if (chosen?.id === b.id) versions[v.env] = 'error';
		}
	}
	function pick(b: Blueprint) {
		choose(b);
		go(1);
	}

	function validate(i: number): boolean {
		const p: Record<string, string> = {};
		if (i === 0 && !chosen) p.game = 'Choose a game first.';
		if (chosen && i === 1) {
			for (const v of editable) {
				const e = checkVariable(v, values[v.env] ?? v.default) || (required[chosen.slug]?.includes(v.env) && !(values[v.env] ?? '').trim() ? `${v.name} is required: the game refuses to start without it.` : '');
				if (e) p[v.env] = e;
			}
			if (Object.keys(p).some((k) => extraVars.some((v) => v.env === k))) moreOptions = true;
		}
		if (chosen && i === 2) {
			if (!name.trim()) p.name = 'Give the server a name.';
			if (tooBig(chosen)) p.memory = `${chosen.name} needs at least ${fmtBytes(minMiB(chosen) * 1048576)} of memory, more than your limit of ${fmtBytes(maxMiB * 1048576)}.`;
			else if (memoryMiB < minMiB(chosen) || memoryMiB > maxMiB) p.memory = `Choose between ${fmtBytes(minMiB(chosen) * 1048576)} and ${fmtBytes(maxMiB * 1048576)}.`;
			if (cpus < minCpu || cpus > maxCpu) p.cpu = `Choose between ${minCpu} and ${maxCpu} cores.`;
		}
		if (chosen && i === 3) for (const a of chosen.spec.agreements) if (!agreed[a.id]) p['agree-' + a.id] = 'Accept this to run the server.';
		problems = p;
		return Object.keys(p).length === 0;
	}
	async function go(to: number) {
		if (to > step) for (let i = step; i < to; i++) if (!validate(i)) return focusProblem();
		problems = {};
		step = to;
		await Promise.resolve();
		heading?.focus();
		window.scrollTo({ top: 0 });
	}
	async function focusProblem() {
		await Promise.resolve();
		document.querySelector<HTMLElement>('[aria-invalid="true"], [data-problem]')?.focus();
	}

	async function create() {
		if (!chosen) return;
		for (let i = 0; i <= 3; i++)
			if (!validate(i)) {
				step = i;
				return focusProblem();
			}
		busy = true;
		submitError = '';
		try {
			const vars: Record<string, string> = {};
			for (const v of editable) if (values[v.env] !== undefined && values[v.env] !== v.default) vars[v.env] = values[v.env];
			const b = await api<Bot>('POST', '/games', {
				name: name.trim(),
				blueprint: chosen.slug,
				variables: vars,
				image,
				memory_bytes: memoryMiB * 1048576,
				nano_cpus: Math.round(cpus * 1e9),
				agreements: chosen.spec.agreements.filter((a) => agreed[a.id]).map((a) => a.id),
				workspace_id: workspaceID,
				node_id: nodeID
			});
			if (startNow && session.features.runner) {
				try {
					await api('POST', `/bots/${b.id}/start`);
				} catch (err) {
					toast(`Created ${b.name}, but it could not start: ${msg(err)}`, 'warn');
				}
			}
			toast(startNow && session.features.runner ? `Created ${b.name}; the first start installs the server` : `Created ${b.name}`);
			await goto(`/servers/${b.id}`);
		} catch (err) {
			// Keep every value so it can be corrected.
			submitError = msg(err);
			busy = false;
		}
	}

	const versionLabel = (v: string) => (v === 'latest' ? `Latest stable${latestStable ? ` (${latestStable})` : ''}` : v);
	const changedVars = $derived(editable.filter((v) => v !== versionVar && (values[v.env] ?? '') !== v.default));
	const allowed = $derived(session.features.games && can('bots.create'));
	const headings = $derived([
		'Which game do you want to host?',
		chosen ? (versionVar ? `Choose the ${chosen.name} version` : `Set up ${chosen.name}`) : 'Version',
		'Name and size',
		'Review and create'
	]);
</script>

<svelte:head><title>New game server · RivetPanel</title></svelte:head>

<div class="mx-auto max-w-3xl">
	<a href="/servers" class="inline-flex items-center gap-1 text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />Game servers</a>
	<h1 class="mt-1 text-page">New game server</h1>

	{#if !session.features.games}
		<div class="mt-6">
			<EmptyState title="Game servers are turned off on this panel">
				<p>An administrator can enable them with <code>RIVET_MODULES=game_servers=preview</code> and a restart; Administration → Modules shows the current state.</p>
				{#snippet actions()}{#if session.user?.role === 'admin'}<a class="btn" href="/admin/modules">Open Modules</a>{/if}<a class="btn btn-quiet" href="/dashboard">Back to the overview</a>{/snippet}
			</EmptyState>
		</div>
	{:else if !allowed}
		<div class="mt-6">
			<EmptyState title="Your role cannot create servers">
				<p>Ask an administrator for the “create bots and servers” permission. Servers shared with you appear under Game servers.</p>
				{#snippet actions()}<a class="btn" href="/servers">Game servers</a>{/snippet}
			</EmptyState>
		</div>
	{:else}
		<ol class="mt-5 hidden grid-cols-4 gap-2 sm:grid" aria-label="Steps">
			{#each steps as s, i (s)}
				<li>
					<button class="w-full border-t-[3px] pt-2 text-left {i === step ? 'border-action' : i < step ? 'border-ink/60' : 'border-rule'}" aria-current={i === step ? 'step' : undefined} disabled={i > step} onclick={() => go(i)}>
						<span class="block text-small text-muted">Step {i + 1}</span>
						<span class="flex items-center gap-1.5 font-medium {i === step ? '' : i < step ? 'text-ink/80' : 'text-muted'}">
							{#if i === 0 && chosen && step > 0}<GameIcon type={chosen} size={18} class="!bg-transparent" />{chosen.name}{:else}{s}{/if}
						</span>
					</button>
				</li>
			{/each}
		</ol>
		<p class="mt-4 text-muted sm:hidden">Step {step + 1} of 4 · {steps[step]}</p>

		<section class="mt-6" aria-labelledby="step-h">
			<h2 id="step-h" bind:this={heading} tabindex="-1" class="text-section outline-none">{headings[step]}</h2>

			{#if loadError}
				<Notice tone="fail" class="mt-3" live>{loadError} <button class="link" onclick={load}>Try again</button></Notice>
			{/if}

			{#if step === 0}
				{#if blueprints === null}
					<div class="mt-4"><Skeleton rows={4} label="Loading games" /></div>
				{:else if blueprints.length === 0 && !loadError}
					<div class="mt-4">
						<EmptyState title="No games are enabled yet">
							<p>{can('blueprints.manage') ? 'Turn on a built-in server type or import a Pterodactyl egg under Administration → Server types.' : 'An administrator has not enabled any server types yet.'}</p>
							{#snippet actions()}{#if can('blueprints.manage')}<a class="btn btn-primary" href="/admin/blueprints">Open Server types</a>{/if}{/snippet}
						</EmptyState>
					</div>
				{:else if blueprints.length}
					<div class="mt-4 flex flex-wrap items-center gap-2">
						<label class="relative min-w-0 flex-1 basis-56">
							<span class="sr-only">Search games</span>
							<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
							<input class="field pl-8" type="search" placeholder="Search games, e.g. Fabric or Valheim" bind:value={q} />
						</label>
						{#if families.length > 2}
							<div class="flex flex-wrap gap-1 rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Game family">
								{#each families as f (f.id)}
									<button class="rounded-inner px-2.5 py-1.5 text-small font-medium {family === f.id ? 'bg-paper-2 text-ink' : 'text-muted hover:text-ink'}" aria-pressed={family === f.id} onclick={() => (family = f.id)}>{f.label}</button>
								{/each}
							</div>
						{/if}
					</div>
					{#if categories.length === 0}
						<div class="mt-4">
							<EmptyState title="No game matches “{q.trim()}”" compact>
								{#snippet actions()}<button class="btn" onclick={() => { q = ''; family = ''; }}>Show every game</button>{/snippet}
							</EmptyState>
						</div>
					{/if}
					{#each categories as [cat, list] (cat)}
						<p class="eyebrow mt-6">{cat}</p>
						<div class="mt-2 grid gap-3 sm:grid-cols-2" role="radiogroup" aria-label={cat}>
							{#each list as b (b.id)}
								<button
									type="button"
									role="radio"
									aria-checked={chosen?.id === b.id}
									class="group flex items-start gap-3.5 rounded-tile border bg-panel p-4 text-left transition-colors {chosen?.id === b.id ? 'border-action ring-1 ring-action' : 'border-rule-soft hover:border-rule'}"
									onclick={() => pick(b)}
								>
									<GameIcon type={b} size={48} />
									<span class="min-w-0">
										<span class="flex flex-wrap items-center gap-x-2 text-title font-semibold">{b.name}{#if b.slug === 'minecraft-paper'}<span class="text-small font-normal text-run">Recommended</span>{/if}{#if b.source === 'custom'}<span class="text-small font-normal text-muted">Custom</span>{/if}</span>
										<span class="mt-0.5 line-clamp-2 block text-small text-muted">{gameBlurb(b.slug) || b.description}</span>
										<span class="mt-2 flex flex-wrap gap-x-3 text-small text-muted">
											<span>{fmtBytes(b.spec.resources.memory_mb * 1048576)} suggested</span>
											{#if b.spec.agreements.length}<span>Licence to accept</span>{/if}
											{#if tooBig(b)}<span class="text-warn">Above your memory limit</span>{/if}
										</span>
									</span>
								</button>
							{/each}
						</div>
					{/each}
				{/if}
			{:else if step === 1 && chosen}
				<div class="mt-4 flex items-start gap-4 rounded-tile border border-rule-soft bg-panel p-4">
					<GameIcon type={chosen} size={64} />
					<div class="min-w-0">
						<p class="text-title font-semibold">{chosen.name} <span class="text-small font-normal text-muted">· {chosen.category}</span></p>
						<p class="mt-1 text-small text-muted">{chosen.description}</p>
						<button class="link mt-1 text-small" onclick={() => go(0)}>Choose another game</button>
					</div>
				</div>

				<div class="mt-5 grid gap-5">
					{#if versionVar}
						<label class="block">
							<span class="label">{versionVar.name}</span>
							{#if Array.isArray(versionList)}
								<select class="field" bind:value={values[versionVar.env]} aria-invalid={problems[versionVar.env] ? 'true' : undefined}>
									<option value="latest">{versionLabel('latest')}</option>
									{#if values[versionVar.env] && values[versionVar.env] !== 'latest' && !versionList.some((v) => v.id === values[versionVar!.env])}<option value={values[versionVar.env]}>{values[versionVar.env]}</option>{/if}
									{#each versionList as v (v.id)}<option value={v.id}>{v.id}{v.stable ? '' : ' (pre-release)'}</option>{/each}
								</select>
								<span class="help {problems[versionVar.env] ? 'text-fail!' : ''}">{problems[versionVar.env] || 'Latest stable is right for most people. Pick an older version to match a modpack or your players’ game.'}</span>
								{#if hasUnstable}<label class="mt-2 flex items-center gap-2 text-small"><input type="checkbox" bind:checked={unstable} />Show pre-releases and snapshots</label>{/if}
							{:else}
								<input class="field font-mono" bind:value={values[versionVar.env]} placeholder="latest" aria-invalid={problems[versionVar.env] ? 'true' : undefined} />
								<span class="help {problems[versionVar.env] ? 'text-fail!' : ''}">
									{#if problems[versionVar.env]}{problems[versionVar.env]}{:else if versionList === 'error'}The version list could not be loaded. Type a version such as 1.21.11, or keep “latest”. <button class="link" onclick={() => chosen && versionVar && loadVersions(chosen, versionVar)}>Retry</button>{:else}Loading versions…{/if}
								</span>
							{/if}
						</label>
					{/if}

					{#each mainVars as v (v.env)}
						{@render variable(v)}
					{/each}

					{#if !versionVar && mainVars.length === 0}
						<p class="text-muted">This game has no settings to choose now. You can change its startup settings later.</p>
					{/if}

					{#if extraVars.length || chosen.spec.images.length > 1}
						<div>
							<button type="button" class="btn btn-quiet -ml-2" aria-expanded={moreOptions} onclick={() => (moreOptions = !moreOptions)}><Icon name={moreOptions ? 'chevronDown' : 'chevronRight'} size={14} />More options</button>
							{#if moreOptions}
								<div class="mt-3 grid gap-4 border-l-2 border-rule-soft pl-4">
									{#if chosen.spec.images.length > 1}
										<label class="block">
											<span class="label">{isJava ? 'Java version' : 'Image'}</span>
											<select class="field" bind:value={image}>
												<option value="">{isJava ? 'Automatic (the Java this version needs)' : 'Automatic'}</option>
												{#each chosen.spec.images as im (im.label)}<option value={im.label}>{im.label}</option>{/each}
											</select>
										</label>
									{/if}
									{#each extraVars as v (v.env)}{@render variable(v)}{/each}
								</div>
							{/if}
						</div>
					{/if}
				</div>
			{:else if step === 2 && chosen}
				<div class="mt-4 grid gap-5">
					<label class="block max-w-md">
						<span class="label">Server name</span>
						<input class="field" maxlength="64" bind:value={name} oninput={() => (nameTouched = true)} aria-invalid={problems.name ? 'true' : undefined} aria-describedby="name-help" />
						<span id="name-help" class="help {problems.name ? 'text-fail!' : ''}">{problems.name || 'Shown in RivetPanel only. Players see the name set in the game’s own settings.'}</span>
					</label>

					<fieldset>
						<legend class="label">Memory: <span class="font-mono">{fmtBytes(memoryMiB * 1048576)}</span></legend>
						{#if presets.length}
							<div class="mt-1 flex flex-wrap gap-1.5" role="group" aria-label="Memory presets">
								{#each presets as m (m)}
									<button type="button" class="rounded-control border px-2.5 py-1 font-mono text-small {memoryMiB === m ? 'border-action bg-action/10 text-ink' : 'border-rule-soft bg-raised text-muted hover:border-rule hover:text-ink'}" aria-pressed={memoryMiB === m} onclick={() => (memoryMiB = m)}>
										{fmtBytes(m * 1048576)}{#if m === chosen.spec.resources.memory_mb}<span class="ml-1 font-sans text-run">suggested</span>{/if}
									</button>
								{/each}
							</div>
						{/if}
						<input type="range" class="mt-3 w-full accent-[var(--color-action)]" min={minMiB(chosen)} max={Math.max(maxMiB, minMiB(chosen))} step="128" bind:value={memoryMiB} aria-label="Memory in MiB" aria-invalid={problems.memory ? 'true' : undefined} />
						<span class="help {problems.memory ? 'text-fail!' : ''}">
							{problems.memory || (isJava ? `More players, mods and view distance need more memory. About ${chosen.spec.resources.heap_percent ?? 85}% goes to the Java heap.` : `${chosen.name} needs at least ${fmtBytes(minMiB(chosen) * 1048576)}. The server is stopped if it uses more than this.`)}
						</span>
					</fieldset>

					<label class="block">
						<span class="label">CPU: <span class="font-mono">{cpus}</span> {cpus === 1 ? 'core' : 'cores'}</span>
						<input type="range" class="w-full accent-[var(--color-action)]" min={minCpu} max={maxCpu} step="0.25" bind:value={cpus} aria-invalid={problems.cpu ? 'true' : undefined} />
						<span class="help {problems.cpu ? 'text-fail!' : ''}">{problems.cpu || `Suggested ${chosen.spec.resources.cpus} ${chosen.spec.resources.cpus === 1 ? 'core' : 'cores'}. A limit, not a reservation: other servers can use idle CPU.`}</span>
					</label>

					{#if creatable().length > 1}
						<label class="block max-w-md">
							<span class="label">Workspace</span>
							<select class="field" bind:value={workspaceID}>
								{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{w.personal ? 'Personal workspace' : w.name}</option>{/each}
							</select>
							<span class="help">Members of the workspace get access according to their role.</span>
						</label>
					{/if}
					{#if nodes.length > 1}
						<label class="block max-w-md">
							<span class="label">Node</span>
							<select class="field" bind:value={nodeID}>{#each nodes as n (n.id)}<option value={n.id}>{n.name}{n.transport === 'agent' ? (n.agent?.connected ? ' · connected' : ' · offline') : ' · this host'}</option>{/each}</select>
							<span class="help">The machine that runs the server and keeps its files. An offline node accepts the server and starts it when its agent reconnects.</span>
						</label>
					{/if}

					{#if portText}
						<div class="flex items-start gap-2.5 rounded-tile border border-rule-soft bg-panel p-3.5 text-small">
							<Icon name="network" class="mt-0.5 shrink-0 text-action" />
							<p><span class="font-medium">Ports.</span> <span class="text-muted">{portText}</span></p>
						</div>
					{/if}
				</div>
			{:else if step === 3 && chosen}
				<div class="mt-4 flex items-center gap-4 rounded-tile border border-rule-soft bg-panel p-4">
					<GameIcon type={chosen} size={56} />
					<div class="min-w-0">
						<p class="truncate text-title font-semibold">{name}</p>
						<p class="text-small text-muted">{chosen.name} · {chosen.category}</p>
					</div>
				</div>
				<dl class="mt-4 grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 border-y border-rule-soft py-3">
					{#if versionVar}<dt class="text-muted">{versionVar.name}</dt><dd>{versionLabel(values[versionVar.env] || 'latest')} <button class="link ml-1 text-small" onclick={() => go(1)}>Change</button></dd>{/if}
					{#each mainVars.concat(changedVars.filter((v) => !mainVars.includes(v))) as v (v.env)}
						<dt class="text-muted">{v.name}</dt><dd class="break-all">{secretVar(v) ? (values[v.env] ? '••••••' : 'Not set') : values[v.env] || 'Not set'}</dd>
					{/each}
					<dt class="text-muted">Memory</dt><dd class="font-mono">{fmtBytes(memoryMiB * 1048576)}</dd>
					<dt class="text-muted">CPU</dt><dd class="font-mono">{cpus} {cpus === 1 ? 'core' : 'cores'}</dd>
					{#if image}<dt class="text-muted">{isJava ? 'Java' : 'Image'}</dt><dd>{image}</dd>{/if}
					{#if creatable().length > 1}<dt class="text-muted">Workspace</dt><dd>{creatable().find((w) => (w.personal ? '' : w.id) === workspaceID)?.name ?? 'Personal'}</dd>{/if}
					{#if nodes.length > 1}<dt class="text-muted">Node</dt><dd>{nodes.find((n) => n.id === nodeID)?.name ?? 'This host'}</dd>{/if}
					<dt class="text-muted">Install</dt><dd>{isSteam ? 'Downloaded from Steam with SteamCMD on the first start (several GB for some games).' : isJava ? 'Server software and the right Java are downloaded on the first start.' : 'Prepared on the first start.'}</dd>
				</dl>

				{#if chosen.spec.agreements.length}
					<div class="mt-5 grid gap-3">
						<h3 class="font-semibold">Licence terms</h3>
						{#each chosen.spec.agreements as a (a.id)}
							<label class="flex items-start gap-2.5 rounded-tile border p-3.5 {problems['agree-' + a.id] ? 'border-fail/60' : 'border-rule-soft'} bg-panel">
								<input type="checkbox" class="mt-1" bind:checked={agreed[a.id]} aria-invalid={problems['agree-' + a.id] ? 'true' : undefined} />
								<span>
									{a.text}{#if a.url}{' '}<a class="link" href={a.url} target="_blank" rel="noopener">Read it<Icon name="external" size={12} class="ml-0.5 inline" /></a>{/if}
									<span class="help {problems['agree-' + a.id] ? 'text-fail!' : ''}">{problems['agree-' + a.id] || 'Required by the game’s publisher before the server may run.'}</span>
								</span>
							</label>
						{/each}
					</div>
				{/if}

				{#if session.features.runner}
					<label class="mt-5 flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={startNow} /><span>Start the server after creating it<span class="help">You can watch the install and the console on the server page.</span></span></label>
				{:else}
					<Notice class="mt-5">This panel runs without Docker, so the server is created but cannot be started here.</Notice>
				{/if}
				{#if submitError}<Notice tone="fail" class="mt-4" live>{submitError}</Notice>{/if}
			{/if}
		</section>

		{#if blueprints?.length}
			<div class="wizard-foot">
				{#if step > 0}<button class="btn" onclick={() => go(step - 1)}><Icon name="chevronLeft" size={14} />Back</button>{/if}
				<a href="/servers" class="btn btn-quiet">Cancel</a>
				{#if step === 0}
					{#if chosen}<button class="btn btn-primary wizard-next" onclick={() => go(1)}>Continue with {chosen.name}<Icon name="chevronRight" size={14} /></button>{/if}
				{:else if step < 3}
					<button class="btn btn-primary wizard-next" onclick={() => go(step + 1)}>Continue<Icon name="chevronRight" size={14} /></button>
				{:else}
					<button class="btn btn-primary wizard-next" onclick={create} disabled={busy}><Icon name="plus" />{busy ? 'Creating…' : startNow && session.features.runner ? 'Create and start' : 'Create server'}</button>
				{/if}
			</div>
		{/if}
	{/if}
</div>

{#snippet variable(v: BlueprintVariable)}
	<label class="block">
		<span class="label">{v.name}{#if v.reinstall}<span class="ml-1.5 text-small font-normal text-muted">(changing it later reinstalls)</span>{/if}</span>
		{#if v.rules.type === 'enum' && (v.rules.options ?? []).every((o) => o === '0' || o === '1') && (v.rules.options ?? []).length === 2}
			<select class="field" bind:value={values[v.env]}><option value="1">Yes</option><option value="0">No</option></select>
		{:else if v.rules.type === 'enum'}
			<select class="field" bind:value={values[v.env]}>{#each v.rules.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}</select>
		{:else if v.rules.type === 'bool'}
			<select class="field" bind:value={values[v.env]}><option value="true">Yes</option><option value="false">No</option></select>
		{:else if secretVar(v)}
			<span class="relative block">
				<input class="field pr-10 font-mono" type={shown[v.env] ? 'text' : 'password'} autocomplete="new-password" spellcheck="false" bind:value={values[v.env]} aria-invalid={problems[v.env] ? 'true' : undefined} />
				<button type="button" class="btn btn-quiet btn-icon btn-sm absolute top-1/2 right-1 -translate-y-1/2" aria-label={shown[v.env] ? `Hide ${v.name}` : `Show ${v.name}`} aria-pressed={!!shown[v.env]} onclick={() => (shown[v.env] = !shown[v.env])}><Icon name={shown[v.env] ? 'eyeOff' : 'eye'} /></button>
			</span>
		{:else}
			<input class={v.rules.type === 'int' ? 'field font-mono' : 'field'} inputmode={v.rules.type === 'int' ? 'numeric' : undefined} spellcheck="false" bind:value={values[v.env]} aria-invalid={problems[v.env] ? 'true' : undefined} />
		{/if}
		<span class="help {problems[v.env] ? 'text-fail!' : ''}">{problems[v.env] || v.description || ''}</span>
	</label>
{/snippet}
