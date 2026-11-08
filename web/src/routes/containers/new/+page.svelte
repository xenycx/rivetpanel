<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { splitArgs } from '$lib/args';
	import type { Bot, Capacity, Limits, Volume } from '$lib/api/types';
	import { boundText, budgetWarning, clampMax, countBlock, room } from '$lib/room';
	import { resourceHref } from '$lib/api/games';
	import { session } from '$lib/session.svelte';
	import { creatable, loadWorkspaces, workspaces, wsName } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import ResourcePicker from '$lib/components/ResourcePicker.svelte';
	import CreatingIn from '$lib/components/CreatingIn.svelte';

	// Run one image. Everything but the image is optional; the defaults suit
	// most images that do not insist on running as root.
	type Policy = { registries: string[]; max_image_bytes: number; enabled: boolean; public_ports: boolean };
	let policy = $state<Policy | null>(null);
	let name = $state('');
	let image = $state('');
	let command = $state('');
	let ports = $state<{ container_port: number | null; host_port: number | null }[]>([]);
	let env = $state('');
	let volumes = $state<Volume[]>([]);
	let writable = $state('');
	let user = $state('');
	// App size; null uses the defaults (512 MiB and one core).
	let memory = $state<number | null>(null);
	let cpu = $state<number | null>(null);
	let limits = $state<Limits | null>(null);
	let cap = $state<Capacity | null>(null);
	let workspaceId = $state('');
	let start = $state(true);
	let busy = $state(false);
	let error = $state('');
	let advanced = $state(false);

	onMount(() => {
		api<Policy>('GET', '/containers/policy').then((p) => (policy = p)).catch(() => {});
		api<{ limits: Limits }>('GET', '/runtimes').then((r) => (limits = r.limits)).catch(() => {});
		// The workspace selected in the sidebar, once the list is known.
		loadWorkspaces().then(() => {
			const sel = workspaces.selected;
			if (sel && sel !== 'all') workspaceId = creatable().some((w) => w.id === sel && !w.personal) ? sel : '';
		});
	});
	// Container apps run on this host: its node maximum and budget apply,
	// with the room left under the workspace allowance.
	$effect(() => {
		const ws = workspaceId;
		let live = true;
		api<Capacity>('GET', `/me/capacity?workspace=${encodeURIComponent(ws)}`)
			.then((c) => {
				if (live) cap = c;
			})
			.catch(() => {
				if (live) cap = null;
			});
		return () => {
			live = false;
		};
	});
	const DEFAULT_MEM = 512 * 2 ** 20;
	const DEFAULT_CPU = 1e9;
	const rm = $derived(room(cap));
	const nodeMaxMem = $derived(limits?.max_memory_bytes ?? 0);
	const nodeMaxCpu = $derived(limits?.max_nano_cpus ?? 0);
	const maxMem = $derived(nodeMaxMem ? clampMax(nodeMaxMem, rm.memory) : rm.memory);
	const maxCpu = $derived(nodeMaxCpu ? clampMax(nodeMaxCpu, rm.cpu) : rm.cpu);
	const effMem = $derived(memory ?? DEFAULT_MEM);
	const effCpu = $derived(cpu ?? DEFAULT_CPU);
	const blocked = $derived(countBlock(cap, ['apps', 'bots'], { apps: 'apps in total', bots: 'bots and containers' }));
	const sizeProblem = $derived(
		maxMem != null && effMem > maxMem
			? rm.memory != null && rm.memory < nodeMaxMem
				? `The workspace has only ${fmtBytes(rm.memory)} of its memory allowance left.`
				: `Choose up to ${fmtBytes(maxMem)}, the node maximum on this host.`
			: maxCpu != null && effCpu > maxCpu
				? rm.cpu != null && rm.cpu < nodeMaxCpu
					? `The workspace has only ${fmtCpu(rm.cpu)} of its CPU allowance left.`
					: `Choose up to ${fmtCpu(maxCpu)}, the node maximum on this host.`
				: ''
	);
	// The image's last path part, without its tag, as a starting name.
	const suggested = $derived(image.trim().split('/').pop()?.split(/[:@]/)[0] ?? '');

	function parseEnv(text: string): Record<string, string> {
		const out: Record<string, string> = {};
		for (const line of text.split('\n')) {
			const l = line.trim();
			if (!l || l.startsWith('#')) continue;
			const i = l.indexOf('=');
			if (i > 0) out[l.slice(0, i).trim()] = l.slice(i + 1);
		}
		return out;
	}
	async function create(e: SubmitEvent) {
		e.preventDefault();
		if (sizeProblem) {
			error = sizeProblem;
			return;
		}
		busy = true;
		error = '';
		try {
			const b = await api<Bot>('POST', '/containers', {
				name: name.trim() || suggested,
				image,
				workspace_id: workspaceId,
				command: command.trim() ? splitArgs(command) : [],
				ports: ports.filter((p) => p.container_port).map((p) => ({ container_port: p.container_port, host_port: p.host_port ?? 0, protocol: 'tcp' })),
				env: parseEnv(env),
				volumes: volumes.filter((v) => v.name.trim() && v.path.trim()),
				writable: writable.split('\n').map((s) => s.trim()).filter(Boolean),
				user,
				memory_bytes: memory ?? 0,
				nano_cpus: cpu ?? 0,
				start: start && session.features.runner
			});
			goto(resourceHref(b));
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The container could not be created.';
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Run an image · RivetPanel</title></svelte:head>

<a href="/new" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />Everything you can create</a>
<header class="page-head">
	<h1 class="font-semibold">Run an image</h1>
	<p class="text-small text-muted">Any image from an allowed registry, as a container app with its own console, files, backups and size. <a class="link" href="/stacks/new">Several services? Import a Compose file.</a></p>
	<CreatingIn workspace={cap?.workspace} class="mt-1.5" />
</header>

{#if !session.features.containers}
	<Notice tone="warn" class="mt-5">Container apps are turned off on this panel. An administrator can turn on the Containers module and allow at least one registry.</Notice>
{:else}
	{#if blocked}<Notice tone="warn" class="mt-5 max-w-3xl" title="No room for another container">{blocked}</Notice>{/if}
	<form class="mt-6 grid max-w-3xl gap-6" onsubmit={create}>
		<section class="grid gap-4" aria-labelledby="img-h">
			<h2 id="img-h" class="text-title font-semibold">Image</h2>
			<label class="block">
				<span class="label">Image</span>
				<input class="field font-mono text-[13px]" bind:value={image} required spellcheck="false" autocomplete="off" placeholder="nginxinc/nginx-unprivileged:1.27" />
				<span class="help">
					{#if policy}{policy.registries.length ? `From ${policy.registries.join(', ')}` : 'No registry is allowed'}{policy.max_image_bytes ? `, up to ${Math.round(policy.max_image_bytes / 2 ** 20)} MiB` : ''}.{' '}{/if}Prefer a version tag over <code class="font-mono">latest</code>.
				</span>
			</label>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block">
					<span class="label">Name</span>
					<input class="field" bind:value={name} maxlength="64" placeholder={suggested || 'my-service'} />
				</label>
				{#if creatable().length > 1}
					<label class="block">
						<span class="label">Workspace</span>
						<select class="field" bind:value={workspaceId}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{wsName(w)}</option>{/each}</select>
					</label>
				{/if}
			</div>
		</section>

		<section class="grid gap-3 border-t border-rule-soft pt-6" aria-labelledby="ports-h">
			<h2 id="ports-h" class="text-title font-semibold">Ports</h2>
			<p class="text-small text-muted">Publish a port the container listens on. {policy?.public_ports ? 'Published ports are reachable from outside this host.' : 'Ports are published on 127.0.0.1 only; put a reverse proxy in front of them.'} Leave the host port empty to pick a free one.</p>
			{#each ports as p, i (i)}
				<div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-end gap-2">
					<label class="block"><span class="label">Container port</span><input class="field font-mono" type="number" min="1" max="65535" bind:value={p.container_port} required /></label>
					<label class="block"><span class="label">Host port</span><input class="field font-mono" type="number" min="1" max="65535" bind:value={p.host_port} placeholder="automatic" /></label>
					<button type="button" class="btn btn-quiet btn-icon" onclick={() => ports.splice(i, 1)} aria-label="Remove port {i + 1}"><Icon name="trash" size={14} /></button>
				</div>
			{/each}
			<div><button type="button" class="btn btn-sm" onclick={() => ports.push({ container_port: null, host_port: null })}><Icon name="plus" size={13} />Publish a port</button></div>
		</section>

		<section class="grid gap-3 border-t border-rule-soft pt-6" aria-labelledby="env-h">
			<h2 id="env-h" class="text-title font-semibold">Variables</h2>
			<label class="block">
				<span class="sr-only">Variables, one KEY=value per line</span>
				<textarea class="field min-h-24 font-mono text-[13px]" bind:value={env} spellcheck="false" placeholder="POSTGRES_PASSWORD=…&#10;TZ=Europe/Berlin"></textarea>
				<span class="help">One <code class="font-mono">KEY=value</code> per line. Values are encrypted and never shown again after saving.</span>
			</label>
		</section>

		<section class="grid gap-3 border-t border-rule-soft pt-6" aria-labelledby="files-h">
			<h2 id="files-h" class="text-title font-semibold">Files</h2>
			<p class="text-small text-muted">The image's own files are read-only. Give each folder it keeps data in a volume, so the data survives restarts and is backed up.</p>
			{#each volumes as v, i (i)}
				<div class="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)_auto] items-end gap-2">
					<label class="block"><span class="label">Volume</span><input class="field font-mono text-[13px]" bind:value={v.name} placeholder="data" spellcheck="false" /></label>
					<label class="block"><span class="label">Folder in the container</span><input class="field font-mono text-[13px]" bind:value={v.path} placeholder="/var/lib/app" spellcheck="false" /></label>
					<button type="button" class="btn btn-quiet btn-icon" onclick={() => volumes.splice(i, 1)} aria-label="Remove volume {i + 1}"><Icon name="trash" size={14} /></button>
				</div>
			{/each}
			<div><button type="button" class="btn btn-sm" onclick={() => volumes.push({ name: '', path: '' })}><Icon name="plus" size={13} />Add a volume</button></div>
		</section>

		<section class="grid gap-4 border-t border-rule-soft pt-6" aria-labelledby="size-h">
			<h2 id="size-h" class="text-title font-semibold">Size</h2>
			<div>
				<span class="label" id="ctr-memory-l">Memory</span>
				<ResourcePicker kind="memory" id="ctr-memory" labelledby="ctr-memory-l" value={memory} onchange={(v) => (memory = v)} min={limits?.min_memory_bytes ?? 0} max={maxMem} allowEmpty emptyLabel="Default (512 MiB)" invalid={!!sizeProblem && maxMem != null && effMem > maxMem} />
				<span class="help">The container is stopped if it uses more.<span class="block">{boundText('memory', cap, nodeMaxMem, 'this host')}</span></span>
			</div>
			<div>
				<span class="label" id="ctr-cpu-l">CPU</span>
				<ResourcePicker kind="cpu" id="ctr-cpu" labelledby="ctr-cpu-l" value={cpu} onchange={(v) => (cpu = v)} min={limits?.min_nano_cpus ?? 0} max={maxCpu} allowEmpty emptyLabel="Default (1 core)" invalid={!!sizeProblem && maxCpu != null && effCpu > maxCpu} />
				<span class="help">A limit, not a reservation.<span class="block">{boundText('cpu', cap, nodeMaxCpu, 'this host')}</span></span>
			</div>
			{#if sizeProblem}<p class="help text-fail!" role="status">{sizeProblem} Choose a smaller size.</p>{/if}
			{#if budgetWarning(cap, effMem)}<Notice tone="warn" title="Over the node budget">{budgetWarning(cap, effMem)}</Notice>{/if}
		</section>

		<section class="border-t border-rule-soft pt-6">
			<button type="button" class="btn btn-quiet -ml-2 font-medium" aria-expanded={advanced} onclick={() => (advanced = !advanced)}>
				<Icon name={advanced ? 'chevronDown' : 'chevronRight'} size={14} />Command and user
			</button>
			{#if advanced}
				<div class="mt-3 grid gap-4">
					<label class="block">
						<span class="label">Command <span class="font-normal text-muted">(optional)</span></span>
						<input class="field font-mono text-[13px]" bind:value={command} spellcheck="false" placeholder="the image's command" />
						<span class="help">Replaces the image's command; its entrypoint stays. No shell runs it.</span>
					</label>
					<label class="block">
						<span class="label">Writable paths <span class="font-normal text-muted">(optional)</span></span>
						<textarea class="field min-h-16 font-mono text-[13px]" bind:value={writable} spellcheck="false" placeholder="/var/cache/nginx"></textarea>
						<span class="help">In-memory folders for caches, sockets and PID files, one per line.</span>
					</label>
					<label class="block max-w-xs">
						<span class="label">Run as <span class="font-normal text-muted">(optional)</span></span>
						<input class="field font-mono text-[13px]" bind:value={user} spellcheck="false" placeholder="1000:1000" />
						<span class="help">Numeric uid:gid, never root.</span>
					</label>
				</div>
			{/if}
		</section>

		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
		<div class="flex flex-wrap items-center gap-4 border-t border-rule-soft pt-5">
			<button class="btn btn-primary" disabled={busy || !image.trim() || !!blocked}>{busy ? 'Creating…' : 'Create container'}</button>
			{#if session.features.runner}<label class="flex items-center gap-2"><input type="checkbox" bind:checked={start} />Start it now</label>{/if}
		</div>
	</form>
{/if}
