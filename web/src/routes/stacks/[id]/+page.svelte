<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Stack } from '$lib/api/types';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import ResourceTable from '$lib/components/ResourceTable.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import AppIcon from '$lib/components/apps/AppIcon.svelte';

	// A stack: its services as container apps, power for all of them at once,
	// and the Compose file it came from.
	const id = $derived(page.params.id!);
	let stack = $state<Stack | null>(null);
	let error = $state('');
	let busy = $state('');
	let now = $state(Date.now());
	let showFile = $state(false);

	async function load() {
		try {
			stack = await api<Stack>('GET', `/stacks/${id}`);
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The stack could not be loaded.';
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') load();
		}, 3000);
		return () => clearInterval(t);
	});
	const running = $derived(stack?.services.filter((b) => b.phase === 'running').length ?? 0);
	const tpl = $derived(stack?.template);
	const allUp = $derived(!!stack && stack.services.length > 0 && running === stack.services.length);
	const webUp = $derived(!!tpl?.web_bot_id && stack?.services.find((b) => b.id === tpl.web_bot_id)?.phase === 'running');
	const openURL = $derived(stack?.url ? stack.url + (tpl?.web?.path && tpl.web.path !== '/' ? tpl.web.path : '') : '');
	const localOnly = $derived(!!stack?.url && !stack.site_id && /\/\/127\.0\.0\.1[:/]/.test(stack.url));
	const failing = $derived(stack?.services.some((b) => b.phase === 'failed' || b.phase === 'retrying') ?? false);

	// Sign-in values stay hidden until asked for (each reveal is audited).
	let revealed = $state<Record<string, string>>({});
	async function reveal(c: { bot_id: string; name: string }) {
		const k = c.bot_id + '/' + c.name;
		if (revealed[k]) return void delete revealed[k];
		try {
			const r = await api<{ value: string }>('POST', `/bots/${c.bot_id}/env/${encodeURIComponent(c.name)}/reveal`);
			revealed[k] = r.value;
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The value could not be shown.', 'fail');
		}
	}
	async function copy(text: string, what: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast(`${what} copied`);
		} catch {
			toast('Copying is blocked in this browser.', 'fail');
		}
	}
	const admin = $derived(!!stack && (stack.role === 'owner' || stack.role === 'admin'));
	const dev = $derived(admin || stack?.role === 'developer');

	async function power(action: 'start' | 'stop' | 'restart') {
		busy = action;
		try {
			stack = await api<Stack>('POST', `/stacks/${id}/${action}`);
			toast(action === 'start' ? 'Starting the stack; each service waits for the ones it depends on' : action === 'stop' ? 'Stopping the stack' : 'Restarting the stack');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The request failed.', 'fail');
			load();
		} finally {
			busy = '';
		}
	}
	async function rename() {
		if (!stack) return;
		const name = await promptDialog({ title: 'Rename the stack', label: 'Name', value: stack.name, confirmLabel: 'Rename', validate: (v) => (v.trim() ? null : 'Give it a name.') });
		if (!name) return;
		try {
			stack = await api<Stack>('PATCH', `/stacks/${id}`, { name });
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The stack could not be renamed.', 'fail');
		}
	}
	async function remove() {
		if (!stack) return;
		const ok = await confirmDialog({
			title: `Delete ${stack.name}?`,
			body: 'Every service of the stack is stopped and deleted with its files, volumes, variables and backups. This cannot be undone.',
			details: stack.services.map((b) => [b.container?.service ?? b.name, b.image_ref.replace(/^docker\.io\/library\//, '')] as [string, string]),
			confirmLabel: 'Delete stack',
			tone: 'danger',
			typeToConfirm: stack.name
		});
		if (!ok) return;
		busy = 'delete';
		try {
			await api('DELETE', `/stacks/${id}`);
			toast('Stack deleted');
			goto('/apps?kind=container');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The stack could not be deleted.', 'fail');
		} finally {
			busy = '';
		}
	}
</script>

<svelte:head><title>{stack?.name ?? 'Stack'} · RivetPanel</title></svelte:head>

<a href="/apps?kind=container" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />Containers</a>

{#if error && !stack}
	<Notice tone="fail" class="mt-2" live>{error}</Notice>
{:else if !stack}
	<Skeleton rows={4} label="Loading the stack" />
{:else}
	<header class="page-head">
		{#if tpl}<AppIcon id={tpl.id} name={tpl.name} size={44} />{/if}
		<div class="min-w-0">
			<h1 class="truncate font-semibold">{stack.name}</h1>
			<p class="text-small text-muted">{tpl ? tpl.name : 'Compose stack'}, {running} of {stack.services.length} running, {fmtBytes(stack.services.reduce((n, b) => n + b.memory_bytes, 0))} memory, {tpl ? 'installed' : 'imported'} {fmtWhen(stack.created_at_ms)}</p>
		</div>
		<div class="ml-auto flex flex-wrap gap-2">
			{#if tpl?.web && openURL}
				<a class="btn {allUp ? 'btn-primary' : ''}" href={openURL} target="_blank" rel="noopener noreferrer" aria-disabled={!webUp}><Icon name="external" size={13} />Open {tpl.name}</a>
			{/if}
			{#if tpl?.console_bot_id && session.features.terminal && admin}
				<a class="btn {allUp ? 'btn-primary' : ''}" href="/containers/{tpl.console_bot_id}?tab=terminal"><Icon name="terminal" size={13} />Open terminal</a>
			{/if}
			{#if session.features.runner && dev}
				{#if running < stack.services.length}<button class="btn {tpl ? '' : 'btn-primary'}" disabled={!!busy} onclick={() => power('start')}><Icon name="play" size={12} />Start all</button>{/if}
				{#if running > 0}
					<button class="btn" disabled={!!busy} onclick={() => power('restart')}><Icon name="restart" size={14} />Restart all</button>
					<button class="btn" disabled={!!busy} onclick={() => power('stop')}><Icon name="stop" size={12} />Stop all</button>
				{/if}
			{/if}
		</div>
	</header>
	{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

	{#if tpl}
		{#if !allUp && !failing && stack.services.some((b) => b.desired_state === 'running')}
			<Notice tone="info" class="mt-4" live>Setting up {tpl.name}: pulling images and starting {stack.services.length === 1 ? 'its container' : `${stack.services.length} containers`}. The first start can take a few minutes.</Notice>
		{:else if failing}
			<Notice tone="fail" class="mt-4" live>A container of {tpl.name} is failing. Open it below to see its console output.</Notice>
		{/if}
		<section class="card mt-6 p-5" aria-labelledby="start-h">
			<h2 id="start-h" class="text-title font-semibold">Getting started</h2>
			<dl class="mt-3 grid gap-x-6 gap-y-3 text-small sm:grid-cols-[10rem_minmax(0,1fr)]">
				{#if tpl.web}
					<dt class="text-muted">Address</dt>
					<dd class="min-w-0">
						{#if stack.url}
							<span class="flex flex-wrap items-center gap-2"><a class="link truncate font-mono" href={openURL} target="_blank" rel="noopener noreferrer">{stack.url}</a><button class="btn btn-quiet btn-icon btn-sm" aria-label="Copy the address" onclick={() => copy(stack!.url!, 'Address')}><Icon name="copy" size={13} /></button></span>
							{#if localOnly}<span class="help">Reachable from the server itself (for example through an SSH tunnel). Install it with a web address to open it from anywhere.</span>{/if}
							{#if stack.site_id}<span class="help">A website of this panel; <a class="link" href="/sites/{stack.site_id}">add your own domain</a> to it.</span>{/if}
						{:else}<span class="text-muted">Not published yet</span>{/if}
					</dd>
				{:else if tpl.console}
					<dt class="text-muted">Use it</dt>
					<dd>From the terminal: <code class="text-ink">{(tpl.console.command ?? ['sh']).join(' ')}</code> starts in the container.</dd>
				{/if}
				{#each tpl.credentials as c (c.bot_id + c.name)}
					{@const k = c.bot_id + '/' + c.name}
					<dt class="text-muted">{c.label}</dt>
					<dd class="flex min-w-0 flex-wrap items-center gap-2">
						<code class="copyable truncate font-mono text-ink">{revealed[k] ?? '••••••••••••'}</code>
						<button class="btn btn-sm btn-quiet" onclick={() => reveal(c)}><Icon name={revealed[k] ? 'eyeOff' : 'eye'} size={13} />{revealed[k] ? 'Hide' : 'Show'}</button>
						{#if revealed[k]}<button class="btn btn-quiet btn-icon btn-sm" aria-label="Copy {c.label}" onclick={() => copy(revealed[k], c.label)}><Icon name="copy" size={13} /></button>{/if}
					</dd>
				{/each}
			</dl>
			{#if tpl.notes.length}
				<ol class="mt-4 grid gap-2 border-t border-rule-soft pt-4">
					{#each tpl.notes as n, i (i)}<li class="flex gap-2.5 text-small"><span class="step-no">{i + 1}</span><span class="pt-0.5">{n}</span></li>{/each}
				</ol>
			{/if}
			<p class="mt-4 text-small text-muted">Settings live under each container's Variables; data is kept in its volumes and backups. <a class="link" href={tpl.website} target="_blank" rel="noopener noreferrer">{tpl.name} documentation</a></p>
		</section>
	{/if}

	<section class="mt-6" aria-labelledby="svc-h">
		<h2 id="svc-h" class="mb-2 text-section font-semibold">{tpl ? 'Containers' : 'Services'}</h2>
		<p class="mb-3 max-w-measure text-small text-muted">Services reach each other by service name on the stack's private network. Each one waits for the services it depends on before it starts.</p>
		<ResourceTable kind="bot" label="Services" items={stack.services} {now} onChanged={load}>
			{#snippet empty()}<span>This stack has no services left.</span>{/snippet}
		</ResourceTable>
	</section>

	<section class="mt-8" aria-labelledby="file-h">
		<h2 id="file-h" class="text-section font-semibold">Compose file</h2>
		<p class="mt-1 text-small text-muted">{tpl ? `The file ${tpl.name} was installed from, with its settings as variables;` : 'Kept as imported, for reference;'} change each service under its own Configuration and Variables.</p>
		<button class="btn btn-sm btn-quiet -ml-2 mt-2" aria-expanded={showFile} onclick={() => (showFile = !showFile)}><Icon name={showFile ? 'chevronDown' : 'chevronRight'} size={13} />{showFile ? 'Hide the file' : 'Show the file'}</button>
		{#if showFile}<pre class="mt-2 max-h-[28rem] overflow-auto rounded-tile border border-rule-soft bg-paper-2/40 p-4 font-mono text-[12.5px] leading-5">{stack.compose}</pre>{/if}
	</section>

	{#if admin || dev}
		<section class="mt-10 border-t border-rule-soft pt-6" aria-labelledby="manage-h">
			<h2 id="manage-h" class="text-section font-semibold">Manage</h2>
			<div class="mt-3 flex flex-wrap gap-2">
				<button class="btn" onclick={rename}><Icon name="pencil" size={13} />Rename</button>
				{#if admin}<button class="btn btn-danger" disabled={!!busy} onclick={remove}><Icon name="trash" size={13} />Delete stack</button>{/if}
			</div>
		</section>
	{/if}
{/if}
