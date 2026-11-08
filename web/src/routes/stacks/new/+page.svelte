<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import type { Capacity, Stack } from '$lib/api/types';
	import { allowanceRoom, budgetWarning, capLimits } from '$lib/room';
	import CreatingIn from '$lib/components/CreatingIn.svelte';
	import { session } from '$lib/session.svelte';
	import { creatable, loadWorkspaces, workspaces, wsName } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	// Paste a Compose file, see exactly what it becomes, then create it. The
	// preview is the same check the import runs, so what it shows is what
	// gets created.
	type Note = { service?: string; message: string };
	type PlanService = {
		service: string;
		name: string;
		image: string;
		command: string[];
		env: string[];
		ports: { container_port: number; host_port: number; protocol: string; host_ip: string }[];
		volumes: { name: string; path: string }[];
		writable: string[];
		depends_on: string[];
		memory_bytes: number;
		nano_cpus: number;
		network_disabled: boolean;
	};
	type Plan = { name: string; services: PlanService[]; warnings: Note[]; errors: Note[]; memory_bytes: number; nano_cpus: number };

	let name = $state('');
	let compose = $state('');
	let envText = $state('');
	let workspaceId = $state('');
	let start = $state(true);
	let plan = $state<Plan | null>(null);
	let previewed = $state(''); // the input the plan belongs to
	let busy = $state<'' | 'preview' | 'create'>('');
	let error = $state('');
	let fileInput: HTMLInputElement | undefined = $state();

	onMount(() => {
		// The workspace selected in the sidebar, once the list is known.
		loadWorkspaces().then(() => {
			const sel = workspaces.selected;
			if (sel && sel !== 'all') workspaceId = creatable().some((w) => w.id === sel && !w.personal) ? sel : '';
		});
	});
	// The workspace's allowance and this host's node budget (stacks run here).
	let capacity = $state<Capacity | null>(null);
	$effect(() => {
		const ws = workspaceId;
		let live = true;
		api<Capacity>('GET', `/me/capacity?workspace=${encodeURIComponent(ws)}`)
			.then((c) => {
				if (live) capacity = c;
			})
			.catch(() => {
				if (live) capacity = null;
			});
		return () => {
			live = false;
		};
	});
	// "2 of 5 bots and containers left" for the counts the stack uses.
	const allowanceLeft = $derived.by(() => {
		if (!capacity) return '';
		const r = allowanceRoom(capacity);
		const l = capLimits(capacity);
		const parts: string[] = [];
		if (r.bots != null) parts.push(`${r.bots} of ${l.bots} bots and containers`);
		if (r.apps != null) parts.push(`${r.apps} of ${l.apps} apps in total`);
		if (r.memory != null) parts.push(`${fmtBytes(r.memory)} of ${fmtBytes(l.memory_bytes ?? 0)} memory`);
		if (r.cpu != null) parts.push(`${fmtCpu(r.cpu)} of ${fmtCpu(l.nano_cpus ?? 0)}`);
		if (!parts.length) return '';
		return `Workspace allowance left: ${parts.join(', ')}.${capacity.exempt ? ' You are exempt as an administrator.' : ''}`;
	});
	const input = $derived(JSON.stringify([name, compose, envText, workspaceId]));
	const stale = $derived(!!plan && previewed !== input);
	const fileNotes = $derived((plan?.warnings ?? []).filter((n) => !n.service));
	const notesFor = (svc: string, list: Note[]) => list.filter((n) => n.service === svc);
	const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

	async function run(dryRun: boolean) {
		busy = dryRun ? 'preview' : 'create';
		error = '';
		const at = input;
		try {
			const r = await api<{ plan: Plan; stack?: Stack }>('POST', '/stacks/import', {
				name, compose, env: envText, workspace_id: workspaceId, dry_run: dryRun, start: start && session.features.runner
			});
			plan = r.plan;
			previewed = at;
			if (r.stack) goto(`/stacks/${r.stack.id}`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The Compose file could not be checked.';
		} finally {
			busy = '';
		}
	}
	async function load(e: Event) {
		const f = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!f) return;
		if (f.size > 256 * 1024) {
			error = 'The Compose file is larger than 256 KiB.';
			return;
		}
		compose = await f.text();
		if (!name) name = f.name.replace(/\.(ya?ml)$/i, '').replace(/^(docker-)?compose$/i, '');
	}
</script>

<svelte:head><title>Import a Compose file · RivetPanel</title></svelte:head>

<a href="/new" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />Everything you can create</a>
<header class="page-head">
	<h1 class="font-semibold">Import a Compose file</h1>
	<p class="text-small text-muted">Each service becomes a container app; together they form a stack whose services reach each other by name. Nothing is created until you confirm the preview.</p>
	<CreatingIn workspace={capacity?.workspace} class="mt-1.5" />
</header>

{#if !session.features.containers}
	<Notice tone="warn" class="mt-5">Container apps are turned off on this panel. An administrator can turn on the Containers module and allow at least one registry.</Notice>
{:else}
	<div class="mt-6 grid gap-8 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
		<form class="grid content-start gap-4" onsubmit={(e) => { e.preventDefault(); run(true); }}>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block"><span class="label">Stack name</span><input class="field" bind:value={name} maxlength="64" placeholder="my-stack" /></label>
				{#if creatable().length > 1}
					<label class="block">
						<span class="label">Workspace</span>
						<select class="field" bind:value={workspaceId}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{wsName(w)}</option>{/each}</select>
					</label>
				{/if}
			</div>
			<label class="block">
				<span class="flex items-baseline justify-between gap-2">
					<span class="label">compose.yaml</span>
					<button type="button" class="link text-small" onclick={() => fileInput?.click()}>Open a file…</button>
				</span>
				<input type="file" accept=".yml,.yaml,text/yaml" class="hidden" bind:this={fileInput} onchange={load} />
				<textarea class="field min-h-80 font-mono text-[13px] leading-5" bind:value={compose} spellcheck="false" required placeholder={'services:\n  web:\n    image: nginxinc/nginx-unprivileged:1.27\n    ports: ["8080:8080"]'}></textarea>
			</label>
			<label class="block">
				<span class="label">.env <span class="font-normal text-muted">(optional)</span></span>
				<textarea class="field min-h-20 font-mono text-[13px]" bind:value={envText} spellcheck="false" placeholder="DB_PASSWORD=…"></textarea>
				<span class="help">Values for <code class="font-mono">${'{VAR}'}</code> in the file. They become each service's variables where the file uses them, encrypted at rest.</span>
			</label>
			{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
			<div class="flex flex-wrap items-center gap-3">
				<button class="btn" disabled={!!busy || !compose.trim()}>{busy === 'preview' ? 'Checking…' : 'Preview'}</button>
				<button type="button" class="btn btn-primary" disabled={!!busy || !plan || stale || plan.errors.length > 0 || !plan.services.length} onclick={() => run(false)}>
					{busy === 'create' ? 'Creating…' : plan && !stale && plan.services.length ? `Create ${plan.services.length} ${plan.services.length === 1 ? 'service' : 'services'}` : 'Create stack'}
				</button>
				{#if session.features.runner}<label class="flex items-center gap-2"><input type="checkbox" bind:checked={start} />Start them</label>{/if}
			</div>
			{#if stale}<p class="text-small text-warn">The file changed since the preview. Preview again before creating.</p>{/if}
		</form>

		<section aria-labelledby="plan-h" aria-live="polite">
			<h2 id="plan-h" class="text-title font-semibold">Preview</h2>
			{#if !plan}
				<p class="mt-2 max-w-measure text-small text-muted">
					The preview lists each service with its image, ports, volumes and variables, and every option the panel changes or refuses. Containers run as a non-root user with a read-only root filesystem and no added capabilities, so privileged mode, host networking and the Docker socket are refused; Dockerfile builds are not supported.
				</p>
			{:else}
				{#if plan.errors.length}
					<Notice tone="fail" class="mt-3" title="This file cannot be imported as it is">
						<ul class="mt-1 list-disc pl-5">{#each plan.errors as n, i (i)}<li>{n.service ? `${n.service}: ${n.message}` : cap(n.message)}</li>{/each}</ul>
					</Notice>
				{/if}
				<p class="mt-2 text-small text-muted">
					Stack <strong class="font-medium text-ink">{plan.name}</strong>: {plan.services.length} {plan.services.length === 1 ? 'service' : 'services'}, {fmtBytes(plan.memory_bytes)} memory, {fmtCpu(plan.nano_cpus)} in total. Each service counts as one of the workspace's bots and containers.
					{#if allowanceLeft}<span class="block">{allowanceLeft}</span>{/if}
				</p>
				{#if start && budgetWarning(capacity, plan.memory_bytes)}<Notice tone="warn" class="mt-3" title="Over the node budget">{budgetWarning(capacity, plan.memory_bytes).replace('This app needs', 'The stack needs').replace('refuses to start it', 'refuses to start what does not fit')}</Notice>{/if}
				{#if fileNotes.length}
					<ul class="mt-3 grid gap-1 text-small">{#each fileNotes as n, i (i)}<li class="flex gap-2"><Icon name="info" size={14} class="mt-0.5 shrink-0 text-muted" /><span>{cap(n.message)}</span></li>{/each}</ul>
				{/if}
				<ol class="mt-4 divide-y divide-rule-soft border-y border-rule-soft">
					{#each plan.services as s (s.service)}
						{@const warn = notesFor(s.service, plan.warnings)}
						<li class="py-3">
							<p class="flex flex-wrap items-baseline gap-x-2">
								<span class="font-medium">{s.service}</span>
								<span class="truncate font-mono text-small text-muted">{s.image.replace(/^docker\.io\/library\//, '')}</span>
							</p>
							<dl class="mt-1.5 grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-small">
								{#if s.command.length}<dt class="text-muted">Command</dt><dd class="truncate font-mono">{s.command.join(' ')}</dd>{/if}
								{#if s.ports.length}<dt class="text-muted">Published</dt><dd class="font-mono">{s.ports.map((p) => `${p.host_ip}:${p.host_port} → ${p.container_port}${p.protocol === 'udp' ? '/udp' : ''}`).join(', ')}</dd>{/if}
								{#if s.volumes.length}<dt class="text-muted">Volumes</dt><dd class="font-mono">{s.volumes.map((v) => `${v.name} → ${v.path}`).join(', ')}</dd>{/if}
								{#if s.writable.length}<dt class="text-muted">Writable</dt><dd class="font-mono">{s.writable.join(', ')}</dd>{/if}
								{#if s.env.length}<dt class="text-muted">Variables</dt><dd class="font-mono">{s.env.join(', ')}</dd>{/if}
								{#if s.depends_on.length}<dt class="text-muted">Waits for</dt><dd>{s.depends_on.join(', ')}</dd>{/if}
								<dt class="text-muted">Limits</dt><dd class="font-mono">{fmtBytes(s.memory_bytes)}, {fmtCpu(s.nano_cpus)}{s.network_disabled ? ', no outbound access' : ''}</dd>
							</dl>
							{#if warn.length}
								<ul class="mt-2 grid gap-0.5 text-small text-warn">{#each warn as n, i (i)}<li>{cap(n.message)}</li>{/each}</ul>
							{/if}
						</li>
					{/each}
				</ol>
			{/if}
		</section>
	</div>
{/if}
