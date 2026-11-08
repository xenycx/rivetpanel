<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { WorkspaceQuota, WorkspaceQuotaView } from '$lib/api/types';
	import WorkspaceUsage from '$lib/components/WorkspaceUsage.svelte';
	import ResourcePicker from '$lib/components/ResourcePicker.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { countFields, countHelp, inheritsText, LIMITS_HELP } from '$lib/limits';
	import { session } from '$lib/session.svelte';

	// A workspace's own allowance. An empty field inherits the default
	// allowance; 0 allows none.
	let { workspaceId }: { workspaceId: string } = $props();

	let v = $state<WorkspaceQuotaView | null>(null);
	// Number inputs bind numbers, or null when emptied.
	let form = $state<Record<string, number | string | null>>({});
	// Bytes and nano-CPUs; null inherits the default allowance.
	let memory = $state<number | null>(null);
	let cpu = $state<number | null>(null);
	let error = $state('');
	let saved = $state('');
	let busy = $state(false);
	// Only built-in administrators change an allowance; delegated roles read it.
	const editable = $derived(session.user?.role === 'admin');

	function fill(r: WorkspaceQuotaView) {
		v = r;
		const q = r.quota;
		form = Object.fromEntries(countFields.map((f) => [f.quota, q[f.quota] == null ? '' : String(q[f.quota])]));
		memory = q.memory_bytes ?? null;
		cpu = q.nano_cpus ?? null;
	}
	const path = $derived(`/admin/workspaces/${workspaceId}/quota`);
	$effect(() => {
		if (!workspaceId) return;
		api<WorkspaceQuotaView>('GET', path)
			.then(fill)
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The allowance could not be loaded.'));
	});

	// What an empty field inherits: the default allowance and its source.
	const inherits = (key: keyof WorkspaceQuota, kind: 'count' | 'memory' | 'cpu' = 'count') => inheritsText(kind, v?.defaults?.[key], v?.default_sources?.[key]);
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	async function save(body: Record<string, number | null>) {
		busy = true;
		error = saved = '';
		try {
			fill(await api<WorkspaceQuotaView>('PUT', path, body));
			saved = 'Allowance saved. Apps and sites the workspace already holds are not touched.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The allowance could not be saved.';
		} finally {
			busy = false;
		}
	}
	function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(countFields.map((f) => [f.quota, num(form[f.quota])]));
		if (Object.values(body).some((n) => n != null && (!Number.isInteger(n) || n < 0))) {
			error = 'Use whole numbers of 0 or more, or leave a field empty to inherit the default allowance.';
			return;
		}
		body.memory_bytes = memory;
		body.nano_cpus = cpu;
		save(body);
	}
	async function reset() {
		busy = true;
		error = saved = '';
		try {
			await api('DELETE', path);
			fill(await api<WorkspaceQuotaView>('GET', path));
			saved = 'This workspace inherits the default allowance again.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The allowance could not be cleared.';
		} finally {
			busy = false;
		}
	}
</script>

<section class="mt-8" aria-labelledby="quota-h">
	<h3 id="quota-h" class="mb-1 text-title font-semibold">Workspace allowance</h3>
	<p class="mb-3 max-w-3xl text-small text-muted">
		What this workspace may hold in total, counting every member's apps and sites. An empty field inherits the <a class="link" href="/admin/settings#limits">default allowance</a>. Lowering a value never stops or removes anything; it only refuses growth. <a class="link" href={LIMITS_HELP}>How limits work</a>
	</p>
	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	{#if saved}<Notice tone="success" live>{saved}</Notice>{/if}
	{#if v}
		<div class="card mb-3 p-5">
			<WorkspaceUsage usage={v.usage} limits={v.limits} sources={v.sources} />
		</div>
		<form class="card grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3" onsubmit={submit}>
			{#each countFields as f (f.key)}
				<label class="block">
					<span class="label">{f.label}</span>
					<input class="field font-mono" type="number" min="0" step="1" placeholder="Inherit" bind:value={form[f.quota]} disabled={!editable || busy} aria-describedby="quota-{f.key}-h" />
					<span class="help" id="quota-{f.key}-h">{form[f.quota] === '' || form[f.quota] == null ? inherits(f.quota) : 'Set for this workspace'}. 0 allows none. {countHelp[f.key]}</span>
				</label>
			{/each}
			<div class="sm:col-span-2 lg:col-span-3">
				<span class="label" id="quota-memory-l">Memory</span>
				<ResourcePicker kind="memory" id="quota-memory" labelledby="quota-memory-l" allowEmpty emptyLabel="Inherit" value={memory} onchange={(x) => (memory = x)} disabled={!editable || busy} />
				<span class="help">{memory == null ? inherits('memory_bytes', 'memory') : 'Set for this workspace'}. The memory sizes of all apps, add-ons included.</span>
			</div>
			<div class="sm:col-span-2 lg:col-span-3">
				<span class="label" id="quota-cpu-l">CPU</span>
				<ResourcePicker kind="cpu" id="quota-cpu" labelledby="quota-cpu-l" allowEmpty emptyLabel="Inherit" value={cpu} onchange={(x) => (cpu = x)} disabled={!editable || busy} />
				<span class="help">{cpu == null ? inherits('nano_cpus', 'cpu') : 'Set for this workspace'}. The CPU sizes of all apps, in cores.</span>
			</div>
			{#if editable}
				<div class="flex flex-wrap items-center gap-2 sm:col-span-2 lg:col-span-3">
					<button class="btn btn-primary" type="submit" disabled={busy}>Save allowance</button>
					{#if v.updated_at_ms}<button class="btn" type="button" disabled={busy} onclick={reset}>Inherit the default allowance</button>{/if}
				</div>
			{:else}
				<p class="text-small text-muted sm:col-span-2 lg:col-span-3">Only administrators can change an allowance.</p>
			{/if}
		</form>
	{/if}
</section>
