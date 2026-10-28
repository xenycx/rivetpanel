<script lang="ts">
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { WorkspaceQuota, WorkspaceQuotaView } from '$lib/api/types';
	import WorkspaceUsage from '$lib/components/WorkspaceUsage.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { session } from '$lib/session.svelte';

	let { workspaceId }: { workspaceId: string } = $props();

	type CountKey = 'max_apps' | 'max_bots' | 'max_games' | 'max_web_apps' | 'max_sites';
	const fields: { key: CountKey; label: string }[] = [
		{ key: 'max_bots', label: 'Bots' },
		{ key: 'max_games', label: 'Game servers' },
		{ key: 'max_web_apps', label: 'Web apps' },
		{ key: 'max_apps', label: 'Apps in total' },
		{ key: 'max_sites', label: 'Sites' }
	];

	let v = $state<WorkspaceQuotaView | null>(null);
	// Number inputs bind numbers, or null when emptied.
	let form = $state<Record<string, number | string | null>>({});
	let error = $state('');
	let saved = $state('');
	let busy = $state(false);
	// Only built-in administrators change limits; delegated roles read them.
	const editable = $derived(session.user?.role === 'admin');

	function fill(r: WorkspaceQuotaView) {
		v = r;
		const q = r.quota;
		form = Object.fromEntries(fields.map((f) => [f.key, q[f.key] == null ? '' : String(q[f.key])]));
		form.memory_mib = q.memory_bytes == null ? '' : String(Math.round(q.memory_bytes / 2 ** 20));
		form.cpus = q.nano_cpus == null ? '' : String(q.nano_cpus / 1e9);
	}
	const path = $derived(`/admin/workspaces/${workspaceId}/quota`);
	$effect(() => {
		if (!workspaceId) return;
		api<WorkspaceQuotaView>('GET', path)
			.then(fill)
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The limits could not be loaded.'));
	});

	function dflt(key: keyof WorkspaceQuota, show: (n: number) => string = String): string {
		const d = v?.defaults?.[key];
		return d == null ? 'Default: no limit' : d === 0 ? 'Default: none' : `Default: ${show(d)}`;
	}
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	async function save(body: Record<string, number | null>) {
		busy = true;
		error = saved = '';
		try {
			fill(await api<WorkspaceQuotaView>('PUT', path, body));
			saved = 'Limits saved. Apps and sites the workspace already holds are not touched.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The limits could not be saved.';
		} finally {
			busy = false;
		}
	}
	function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(fields.map((f) => [f.key, num(form[f.key])]));
		const mib = num(form.memory_mib);
		const cpus = num(form.cpus);
		if ([...Object.values(body), mib].some((n) => n != null && (!Number.isInteger(n) || n < 0)) || (cpus != null && !(cpus >= 0))) {
			error = 'Limits must be whole numbers of 0 or more (CPU may have decimals); leave a field empty for the default.';
			return;
		}
		body.memory_bytes = mib == null ? null : Math.round(mib * 2 ** 20);
		body.nano_cpus = cpus == null ? null : Math.round(cpus * 1e9);
		save(body);
	}
	async function reset() {
		busy = true;
		error = saved = '';
		try {
			await api('DELETE', path);
			fill(await api<WorkspaceQuotaView>('GET', path));
			saved = 'The default limits apply to this workspace again.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The limits could not be cleared.';
		} finally {
			busy = false;
		}
	}
</script>

<section class="mt-8" aria-labelledby="quota-h">
	<h3 id="quota-h" class="mb-1 text-title font-semibold">Limits</h3>
	<p class="mb-3 max-w-3xl text-small text-muted">
		Every member's apps and sites in this workspace count together. Leave a field empty to use the <a class="link" href="/admin/settings#limits">default limits</a>; 0 allows none. Stopped apps count until they are deleted. Lowering a limit never stops or removes anything; it only blocks new apps, sites, memory and CPU.
	</p>
	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	{#if saved}<Notice tone="success" live>{saved}</Notice>{/if}
	{#if v}
		<div class="card mb-3 p-5">
			<WorkspaceUsage usage={v.usage} limits={v.limits} exempt={v.exempt} />
		</div>
		<form class="card grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3" onsubmit={submit}>
			{#each fields as f (f.key)}
				<label class="block">
					<span class="label">{f.label}</span>
					<input class="field font-mono" type="number" min="0" step="1" placeholder={dflt(f.key)} bind:value={form[f.key]} disabled={!editable || busy} />
				</label>
			{/each}
			<label class="block">
				<span class="label">Memory (MiB)</span>
				<input class="field font-mono" type="number" min="0" step="1" placeholder={dflt('memory_bytes', fmtBytes)} bind:value={form.memory_mib} disabled={!editable || busy} />
			</label>
			<label class="block">
				<span class="label">CPU cores</span>
				<input class="field font-mono" type="number" min="0" step="0.25" placeholder={dflt('nano_cpus', (n) => String(n / 1e9))} bind:value={form.cpus} disabled={!editable || busy} />
			</label>
			{#if editable}
				<div class="flex flex-wrap items-center gap-2 sm:col-span-2 lg:col-span-3">
					<button class="btn btn-primary" type="submit" disabled={busy}>Save limits</button>
					<button class="btn" type="button" disabled={busy} onclick={() => save({ max_bots: 1, max_games: 0, max_web_apps: 0, max_sites: 0, max_apps: null, memory_bytes: null, nano_cpus: null })}>One bot only</button>
					{#if v.updated_at_ms}<button class="btn" type="button" disabled={busy} onclick={reset}>Use default limits</button>{/if}
				</div>
			{:else}
				<p class="text-small text-muted sm:col-span-2 lg:col-span-3">Only administrators can change limits.</p>
			{/if}
		</form>
	{/if}
</section>
