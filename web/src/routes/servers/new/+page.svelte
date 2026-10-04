<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Bot, Limits, NodeInfo, RuntimeInfo } from '$lib/api/types';
	import type { Blueprint, BlueprintVariable, Version } from '$lib/api/games';
	import { checkVariable } from '$lib/api/games';
	import { creatable, loadWorkspaces, workspaces } from '$lib/workspaces.svelte';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let blueprints = $state<Blueprint[] | null>(null);
	let limits = $state<Limits | null>(null);
	let error = $state('');
	let chosen = $state<Blueprint | null>(null);
	let name = $state('');
	let values = $state<Record<string, string>>({});
	let versions = $state<Record<string, Version[] | 'error'>>({});
	let memoryMiB = $state(2048);
	let cpus = $state(2);
	let image = $state('');
	let agreed = $state<Record<string, boolean>>({});
	let workspaceID = $state('');
	let startNow = $state(true);
	let nodes = $state<NodeInfo[]>([]);
	let nodeID = $state('');
	let busy = $state(false);
	let formError = $state('');
	let advanced = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	onMount(async () => {
		loadWorkspaces();
		try {
			const [b, r] = await Promise.all([
				api<{ blueprints: Blueprint[] }>('GET', '/blueprints'),
				api<{ runtimes: RuntimeInfo[]; limits: Limits }>('GET', '/runtimes')
			]);
			blueprints = b.blueprints.filter((x) => x.enabled);
			limits = r.limits;
			const want = page.url.searchParams.get('type');
			const pre = blueprints.find((x) => x.slug === want);
			if (pre) choose(pre);
			if (session.user?.role === 'admin' && session.features.agents) {
				api<{ nodes: NodeInfo[] }>('GET', '/nodes').then((r) => {
					nodes = r.nodes.filter((n) => n.enabled && !n.draining);
					nodeID = nodes.find((n) => n.transport === 'local')?.id ?? nodes[0]?.id ?? '';
				}).catch(() => {});
			}
		} catch (e) {
			error = msg(e);
		}
	});

	// The most common choices first.
	const preferred = ['minecraft-paper', 'minecraft-vanilla', 'minecraft-fabric', 'minecraft-forge', 'minecraft-neoforge', 'minecraft-purpur', 'minecraft-folia'];
	const rank = (b: Blueprint) => (preferred.indexOf(b.slug) + 1 || 99);
	const categories = $derived.by(() => {
		const out: Record<string, Blueprint[]> = {};
		for (const b of [...(blueprints ?? [])].sort((a, b) => rank(a) - rank(b))) (out[b.category] ??= []).push(b);
		return Object.entries(out);
	});
	const maxMiB = $derived(limits ? Math.floor(limits.max_memory_bytes / 1048576) : 4096);
	const minMiB = $derived(Math.max(limits ? Math.ceil(limits.min_memory_bytes / 1048576) : 64, chosen?.spec.resources.min_memory_mb ?? 0));
	const versionVar = $derived(chosen?.spec.variables.find((v) => v.versions && v.editable));
	const otherVars = $derived((chosen?.spec.variables ?? []).filter((v) => v.editable && v !== versionVar));
	const problems = $derived.by(() => {
		const p: Record<string, string> = {};
		if (!chosen) return p;
		if (!name.trim()) p.name = 'Give the server a name.';
		for (const v of chosen.spec.variables) {
			if (!v.editable) continue;
			const e = checkVariable(v, values[v.env] ?? v.default);
			if (e) p[v.env] = e;
		}
		if (memoryMiB < minMiB || memoryMiB > maxMiB) p.memory = `Choose between ${minMiB} MiB and ${maxMiB} MiB.`;
		for (const a of chosen.spec.agreements) if (!agreed[a.id]) p['agree-' + a.id] = 'Required to run this server.';
		return p;
	});

	async function choose(b: Blueprint) {
		chosen = b;
		values = Object.fromEntries(b.spec.variables.map((v) => [v.env, v.default]));
		agreed = {};
		image = '';
		memoryMiB = Math.min(b.spec.resources.memory_mb, limits ? Math.floor(limits.max_memory_bytes / 1048576) : b.spec.resources.memory_mb);
		cpus = Math.min(b.spec.resources.cpus, limits ? limits.max_nano_cpus / 1e9 : b.spec.resources.cpus);
		if (!name.trim()) name = `${b.name} server`;
		const ws = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
		workspaceID = ws;
		for (const v of b.spec.variables) if (v.versions) loadVersions(b, v);
	}
	async function loadVersions(b: Blueprint, v: BlueprintVariable) {
		try {
			versions[v.env] = (await api<{ versions: Version[] }>('GET', `/blueprints/${b.slug}/versions/${v.env}`)).versions;
		} catch {
			versions[v.env] = 'error';
		}
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (!chosen || Object.keys(problems).length) {
			formError = Object.values(problems)[0] ?? '';
			return;
		}
		busy = true;
		formError = '';
		try {
			const vars: Record<string, string> = {};
			for (const v of chosen.spec.variables) if (v.editable && values[v.env] !== undefined && values[v.env] !== v.default) vars[v.env] = values[v.env];
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
			if (startNow) {
				try {
					await api('POST', `/bots/${b.id}/start`);
				} catch (err) {
					toast(`Created, but it could not start: ${msg(err)}`, 'warn');
				}
			}
			goto(`/servers/${b.id}`);
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>New game server · RivetPanel</title></svelte:head>

<header class="flex items-center gap-3">
	<a href="/servers" class="btn btn-quiet btn-icon text-muted" aria-label="Back to game servers"><Icon name="chevronLeft" size={16} /></a>
	<div>
		<p class="eyebrow">Game servers</p>
		<h1 class="text-page">New server</h1>
	</div>
</header>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}

{#if blueprints === null && !error}
	<div class="mt-6"><Skeleton rows={4} label="Loading server types" /></div>
{:else if blueprints}
	<section class="mt-6" aria-labelledby="types">
		<h2 id="types" class="text-section font-semibold">1. Server type</h2>
		{#each categories as [cat, list] (cat)}
			<p class="eyebrow mt-4">{cat}</p>
			<div class="mt-2 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
				{#each list as b (b.id)}
					<button type="button" class="card p-4 text-left transition-colors hover:border-action/60 {chosen?.id === b.id ? 'border-action ring-1 ring-action' : ''}" aria-pressed={chosen?.id === b.id} onclick={() => choose(b)}>
						<span class="flex items-center gap-2 font-semibold"><Icon name="cube" class="text-action" />{b.name}</span>
						<span class="mt-1 line-clamp-3 block text-small text-muted">{b.description}</span>
					</button>
				{/each}
			</div>
		{/each}
	</section>

	{#if chosen}
		<form class="mt-8 grid gap-6" onsubmit={create} novalidate>
			<section class="card grid gap-4 p-5 sm:p-6" aria-labelledby="details">
				<h2 id="details" class="text-section font-semibold">2. Details</h2>
				<label class="block">
					<span class="label">Name</span>
					<input class="field" maxlength="64" bind:value={name} required aria-invalid={problems.name ? 'true' : undefined} />
				</label>
				{#if versionVar}
					{@const list = versions[versionVar.env]}
					<label class="block">
						<span class="label">{versionVar.name}</span>
						{#if Array.isArray(list)}
							<select class="field" bind:value={values[versionVar.env]}>
								<option value="latest">Latest stable{list[0] ? ` (${list[0].id})` : ''}</option>
								{#each list as v (v.id)}<option value={v.id}>{v.id}</option>{/each}
							</select>
						{:else}
							<input class="field font-mono" bind:value={values[versionVar.env]} placeholder="latest" aria-invalid={problems[versionVar.env] ? 'true' : undefined} />
							<span class="help">{list === 'error' ? 'The version list could not be loaded; type a version such as 1.21.11 or leave "latest".' : 'Loading versions…'}</span>
						{/if}
						{#if problems[versionVar.env]}<span class="help text-fail">{problems[versionVar.env]}</span>{/if}
					</label>
				{/if}
				<div class="grid gap-4 sm:grid-cols-2">
					<label class="block">
						<span class="label">Memory: {fmtBytes(memoryMiB * 1048576)}</span>
						<input type="range" class="w-full accent-[var(--color-action)]" min={minMiB} max={maxMiB} step="128" bind:value={memoryMiB} />
						<span class="help">{problems.memory ?? (chosen.spec.images.some((i) => i.java) ? `About ${chosen.spec.resources.heap_percent ?? 85}% goes to the Java heap; the rest is for Java itself.` : 'The memory limit of the server container.')}</span>
					</label>
					<label class="block">
						<span class="label">CPU: {cpus} {cpus === 1 ? 'core' : 'cores'}</span>
						<input type="range" class="w-full accent-[var(--color-action)]" min={limits ? limits.min_nano_cpus / 1e9 : 0.5} max={limits ? limits.max_nano_cpus / 1e9 : 4} step="0.25" bind:value={cpus} />
					</label>
				</div>
				{#if creatable().length > 1}
					<label class="block">
						<span class="label">Workspace</span>
						<select class="field" bind:value={workspaceID}>
							{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{w.personal ? 'Personal workspace' : w.name}</option>{/each}
						</select>
					</label>
				{/if}
				{#if nodes.length > 1}
					<label class="block">
						<span class="label">Node</span>
						<select class="field" bind:value={nodeID}>{#each nodes as n (n.id)}<option value={n.id}>{n.name}{n.transport === 'agent' ? n.agent?.connected ? ' · connected' : ' · offline' : ' · local'}</option>{/each}</select>
						<span class="help">The server files and containers stay on this host. An offline node accepts the server and starts it when its agent reconnects.</span>
					</label>
				{/if}
				<button type="button" class="btn btn-quiet w-fit" aria-expanded={advanced} onclick={() => (advanced = !advanced)}><Icon name={advanced ? 'chevronDown' : 'chevronRight'} size={14} />Advanced settings</button>
				{#if advanced}
					<div class="grid gap-4 border-l-2 border-rule-soft pl-4">
						<label class="block">
							<span class="label">{chosen.spec.images.some((i) => i.java) ? 'Java image' : 'Image'}</span>
							<select class="field" bind:value={image}>
								<option value="">{chosen.spec.images.some((i) => i.java) ? 'Automatic (the Java the version needs)' : 'Automatic (the first image)'}</option>
								{#each chosen.spec.images as im (im.label)}<option value={im.label}>{im.label}</option>{/each}
							</select>
						</label>
						{#each otherVars as v (v.env)}
							<label class="block">
								<span class="label">{v.name}</span>
								{#if v.rules.type === 'enum'}
									<select class="field" bind:value={values[v.env]}>{#each v.rules.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}</select>
								{:else if v.rules.type === 'bool'}
									<select class="field" bind:value={values[v.env]}><option value="true">Yes</option><option value="false">No</option></select>
								{:else}
									<input class="field font-mono" bind:value={values[v.env]} aria-invalid={problems[v.env] ? 'true' : undefined} />
								{/if}
								<span class="help {problems[v.env] ? 'text-fail' : ''}">{problems[v.env] ?? v.description ?? ''}</span>
							</label>
						{/each}
					</div>
				{/if}
			</section>

			<section class="card grid gap-3 p-5 sm:p-6" aria-labelledby="finish">
				<h2 id="finish" class="text-section font-semibold">3. Create</h2>
				{#each chosen.spec.agreements as a (a.id)}
					<label class="flex items-start gap-2">
						<input type="checkbox" class="mt-1" bind:checked={agreed[a.id]} />
						<span>{a.text}{#if a.url} <a class="link" href={a.url} target="_blank" rel="noopener">Read it</a>{/if}</span>
					</label>
				{/each}
				<label class="flex items-center gap-2"><input type="checkbox" bind:checked={startNow} /><span>Start the server after creating it</span></label>
				{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
				<div class="flex flex-wrap gap-2">
					<button class="btn btn-primary" disabled={busy}><Icon name="plus" />{busy ? 'Creating…' : 'Create server'}</button>
					<a class="btn" href="/servers">Cancel</a>
				</div>
			</section>
		</form>
	{/if}
{/if}
