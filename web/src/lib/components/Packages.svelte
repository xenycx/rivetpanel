<script lang="ts">
	import { untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Dep, Packages, PkgResult } from '$lib/api/types';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { botId, running }: { botId: string; running: boolean } = $props();

	let pk = $state<Packages | null>(null);
	let q = $state('');
	let results = $state<PkgResult[] | null>(null);
	let searching = $state(false);
	let error = $state('');
	let notice = $state('');
	let group = $state('');
	let edits = $state<Record<string, string>>({});
	const path = $derived(`/bots/${botId}/packages`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		pk = await api<Packages>('GET', path);
		edits = Object.fromEntries((pk.deps ?? []).map((d) => [d.group + d.name, d.spec]));
		group = pk.groups?.[0] ?? '';
	}
	// Loads the packages of the bot shown (again when another bot is opened here).
	$effect(() => {
		void path;
		untrack(() => load().catch((e) => (error = msg(e))));
	});

	async function apply(ops: { action: string; name: string; spec?: string; group?: string }[], done: string) {
		error = notice = '';
		try {
			pk = await api<Packages>('PUT', path, { ops });
			edits = Object.fromEntries((pk.deps ?? []).map((d) => [d.group + d.name, d.spec]));
			toast(`${done}${running ? ' Restart the bot to install the change.' : ' It is installed on the next start.'}`);
		} catch (e) {
			error = msg(e);
		}
	}

	async function search(e: SubmitEvent) {
		e.preventDefault();
		error = '';
		searching = true;
		try {
			results = (await api<{ results: PkgResult[] }>('GET', `${path}/search?q=${encodeURIComponent(q)}`)).results;
		} catch (err) {
			error = msg(err);
			results = null;
		} finally {
			searching = false;
		}
	}

	// Registry versions are plain "1.2.3"; each ecosystem wants its own range syntax.
	function specFor(version: string): string {
		switch (pk?.ecosystem) {
			case 'npm':
				return '^' + version;
			case 'pip':
				return '>=' + version;
			case 'gomod':
				return version;
			default:
				return version; // cargo: a bare version is a caret requirement
		}
	}

	const installed = (n: string) => pk?.deps.some((d) => d.name.toLowerCase() === n.toLowerCase());

	async function upgrade(d: Dep) {
		error = '';
		try {
			const { version } = await api<{ version: string }>('GET', `${path}/latest?name=${encodeURIComponent(d.name)}`);
			edits[d.group + d.name] = specFor(version);
		} catch (e) {
			error = msg(e);
		}
	}
	const groups = $derived([...new Set((pk?.deps ?? []).map((d) => d.group))]);
</script>

{#if !pk}
	{#if error}<Notice tone="fail">{error}</Notice>{:else}<Skeleton rows={3} label="Loading packages" />{/if}
{:else if !pk.supported}
	<p class="max-w-prose text-muted">The visual package manager is available for Node.js, Python, Rust and Go bots. For this runtime, manage dependencies in the file manager.</p>
{:else}
	<p class="mb-4 max-w-prose text-muted">Edits <code class="font-mono">{pk.file}</code>. {#if !pk.exists}It does not exist yet and will be created when you add a package.{/if} Dependencies are installed when the bot next starts.{#if running} <strong>The bot is running: restart it to apply changes.</strong>{/if}</p>

	{#each groups as g (g)}
		<h3 class="mt-4 text-title font-semibold">{g}</h3>
		<ul class="mt-1 list-card">
			{#each pk.deps.filter((d) => d.group === g) as d (d.group + d.name)}
				<li class="grid items-center gap-2 px-3 py-2 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_auto]">
					<code class="min-w-0 truncate font-mono text-[13px]">{d.name}</code>
					<input class="field font-mono text-[13px]" bind:value={edits[d.group + d.name]} disabled={!d.editable} aria-label="Version of {d.name}" />
					<div class="flex gap-1.5">
						{#if d.editable}
							<button class="btn" onclick={() => upgrade(d)} title="Fill in the latest version">Latest</button>
							<button class="btn" disabled={edits[d.group + d.name] === d.spec || !edits[d.group + d.name]} onclick={() => apply([{ action: 'update', name: d.name, spec: edits[d.group + d.name] }], `${d.name} updated.`)}>Update</button>
						{:else}
							<span class="text-muted">edit manually</span>
						{/if}
						<button class="btn btn-danger" onclick={() => apply([{ action: 'remove', name: d.name }], `${d.name} removed.`)}>Remove</button>
					</div>
				</li>
			{/each}
		</ul>
	{:else}
		<p class="text-muted">No dependencies listed yet.</p>
	{/each}

	<h3 class="mt-8 text-title font-semibold">Add a package</h3>
	<form class="mt-2 flex max-w-xl gap-2" onsubmit={search}>
		<input class="field" required maxlength="100" bind:value={q} placeholder={pk.ecosystem === 'npm' || pk.ecosystem === 'cargo' ? 'Search the registry' : pk.ecosystem === 'gomod' ? 'Exact module path, e.g. github.com/joho/godotenv' : 'Exact package name'} />
		<button class="btn" disabled={searching}>{searching ? 'Searching…' : 'Search'}</button>
	</form>
	{#if (pk.groups?.length ?? 0) > 1}
		<label class="mt-2 flex items-center gap-2"><span class="text-muted">Add to</span><select class="field w-auto" bind:value={group}>{#each pk.groups ?? [] as g (g)}<option>{g}</option>{/each}</select></label>
	{/if}
	{#if results}
		<ul class="mt-3 max-w-2xl list-card">
			{#each results as r (r.name)}
				<li class="flex items-center gap-3 px-3 py-2">
					<div class="min-w-0 flex-1">
						<div><span class="font-medium">{r.name}</span> <span class="text-muted">{r.version}</span></div>
						{#if r.description}<div class="truncate text-muted">{r.description}</div>{/if}
					</div>
					<button class="btn btn-primary" disabled={installed(r.name)} onclick={() => apply([{ action: 'add', name: r.name, spec: specFor(r.version), group }], `${r.name} added.`)}>{installed(r.name) ? 'Added' : 'Add'}</button>
				</li>
			{:else}
				<li class="py-3 text-muted">No packages found.</li>
			{/each}
		</ul>
	{/if}
{/if}
{#if error && pk}<Notice tone="fail" class="mt-3">{error}</Notice>{/if}
