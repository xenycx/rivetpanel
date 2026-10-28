<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import type { LimitDefaults } from '$lib/api/types';
	import ResourcePicker from '$lib/components/ResourcePicker.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { session } from '$lib/session.svelte';

	type View = { limits: LimitDefaults; source: Partial<Record<keyof LimitDefaults, 'panel' | 'env'>>; builtin_team_workspaces: number };
	type Key = keyof LimitDefaults;
	// The environment variable each default falls back to, when there is one.
	const envName: Partial<Record<Key, string>> = { max_apps: 'RIVET_MAX_BOTS_PER_USER', max_sites: 'RIVET_MAX_SITES_PER_USER', memory_bytes: 'RIVET_USER_MEMORY_BYTES' };
	const counts: { key: Key; label: string }[] = [
		{ key: 'max_apps', label: 'Apps in total' },
		{ key: 'max_bots', label: 'Bots' },
		{ key: 'max_games', label: 'Game servers' },
		{ key: 'max_web_apps', label: 'Web apps' },
		{ key: 'max_sites', label: 'Sites' }
	];

	let v = $state<View | null>(null);
	let form = $state<Record<string, number | string | null>>({});
	// Bytes and nano-CPUs; null falls back to the environment.
	let memory = $state<number | null>(null);
	let cpu = $state<number | null>(null);
	let error = $state('');
	let saved = $state('');
	let busy = $state(false);
	// Only built-in administrators change limits; settings managers read them.
	const editable = $derived(session.user?.role === 'admin');

	function fill(r: View) {
		v = r;
		const own = (k: Key) => r.source[k] === 'panel';
		const val = (k: Key) => (own(k) && r.limits[k] != null ? r.limits[k] : null);
		form = Object.fromEntries(counts.map((f) => [f.key, val(f.key) == null ? '' : String(val(f.key))]));
		memory = val('memory_bytes');
		cpu = val('nano_cpus');
		form.team_workspaces = own('team_workspaces') ? String(r.limits.team_workspaces) : '';
	}
	onMount(() => {
		api<View>('GET', '/admin/limits')
			.then(fill)
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The default limits could not be loaded.'));
	});

	// What applies when the field is empty: the environment, else nothing.
	function fallback(k: Key, show: (n: number) => string = String): string {
		if (!v) return '';
		if (k === 'team_workspaces') return String(v.builtin_team_workspaces);
		if (v.source[k] === 'env' && v.limits[k] != null) return `${show(v.limits[k] as number)} from environment`;
		return 'No limit';
	}
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(counts.map((f) => [f.key, num(form[f.key])]));
		body.team_workspaces = num(form.team_workspaces);
		if (Object.values(body).some((n) => n != null && (!Number.isInteger(n) || n < 0))) {
			error = 'Limits must be whole numbers of 0 or more; leave a field empty to fall back.';
			return;
		}
		body.memory_bytes = memory;
		body.nano_cpus = cpu;
		busy = true;
		error = saved = '';
		try {
			fill(await api<View>('PUT', '/admin/limits', body));
			saved = 'Default limits saved. They apply to the next app or site created.';
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The default limits could not be saved.';
		} finally {
			busy = false;
		}
	}
</script>

{#if error}<Notice tone="fail" class="mb-3" live>{error}</Notice>{/if}
{#if saved}<Notice tone="success" class="mb-3" live>{saved}</Notice>{/if}
{#if v}
	<form class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" onsubmit={submit}>
		{#each counts as f (f.key)}
			<label class="block">
				<span class="label">{f.label}</span>
				<input class="field font-mono" type="number" min="0" step="1" placeholder={fallback(f.key)} title={envName[f.key] ? `Falls back to ${envName[f.key]}` : undefined} bind:value={form[f.key]} disabled={!editable || busy} />
			</label>
		{/each}
		<div class="sm:col-span-2 lg:col-span-3">
			<span class="label" id="default-memory-l">Memory</span>
			<ResourcePicker kind="memory" id="default-memory" labelledby="default-memory-l" allowEmpty emptyLabel={fallback('memory_bytes', fmtBytes)} value={memory} onchange={(x) => (memory = x)} disabled={!editable || busy} />
		</div>
		<div class="sm:col-span-2 lg:col-span-3">
			<span class="label" id="default-cpu-l">CPU</span>
			<ResourcePicker kind="cpu" id="default-cpu" labelledby="default-cpu-l" allowEmpty emptyLabel={fallback('nano_cpus', fmtCpu)} value={cpu} onchange={(x) => (cpu = x)} disabled={!editable || busy} />
		</div>
		<label class="block">
			<span class="label">Team workspaces per account</span>
			<input class="field font-mono" type="number" min="0" max="1000" step="1" placeholder={fallback('team_workspaces')} bind:value={form.team_workspaces} disabled={!editable || busy} />
			<span class="help">Each workspace gets these defaults, so this bounds how much one account can hold. 0 stops users from creating team workspaces.</span>
		</label>
		{#if editable}
			<div class="sm:col-span-2 lg:col-span-3"><button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save default limits'}</button></div>
		{:else}
			<p class="text-small text-muted sm:col-span-2 lg:col-span-3">Only administrators can change limits.</p>
		{/if}
	</form>
{/if}
