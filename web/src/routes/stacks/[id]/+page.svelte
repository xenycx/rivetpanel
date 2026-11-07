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
		<div class="min-w-0">
			<h1 class="truncate font-semibold">{stack.name}</h1>
			<p class="text-small text-muted">Compose stack, {running} of {stack.services.length} running, {fmtBytes(stack.services.reduce((n, b) => n + b.memory_bytes, 0))} memory, imported {fmtWhen(stack.created_at_ms)}</p>
		</div>
		<div class="ml-auto flex flex-wrap gap-2">
			{#if session.features.runner && dev}
				{#if running < stack.services.length}<button class="btn btn-primary" disabled={!!busy} onclick={() => power('start')}><Icon name="play" size={12} />Start all</button>{/if}
				{#if running > 0}
					<button class="btn" disabled={!!busy} onclick={() => power('restart')}><Icon name="restart" size={14} />Restart all</button>
					<button class="btn" disabled={!!busy} onclick={() => power('stop')}><Icon name="stop" size={12} />Stop all</button>
				{/if}
			{/if}
		</div>
	</header>
	{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

	<section class="mt-6" aria-labelledby="svc-h">
		<h2 id="svc-h" class="mb-2 text-section font-semibold">Services</h2>
		<p class="mb-3 max-w-measure text-small text-muted">Services reach each other by service name on the stack's private network. Each one waits for the services it depends on before it starts.</p>
		<ResourceTable kind="bot" label="Services" items={stack.services} {now} onChanged={load}>
			{#snippet empty()}<span>This stack has no services left.</span>{/snippet}
		</ResourceTable>
	</section>

	<section class="mt-8" aria-labelledby="file-h">
		<h2 id="file-h" class="text-section font-semibold">Compose file</h2>
		<p class="mt-1 text-small text-muted">Kept as imported, for reference; change each service under its own Configuration and Variables.</p>
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
