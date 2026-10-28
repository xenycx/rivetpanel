<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { LimitDefaults } from '$lib/api/types';
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
		form.memory_mib = val('memory_bytes') == null ? '' : String(Math.round((val('memory_bytes') as number) / 2 ** 20));
		form.cpus = val('nano_cpus') == null ? '' : String((val('nano_cpus') as number) / 1e9);
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
		const n = envName[k];
		if (v.source[k] === 'env' && v.limits[k] != null) return `${show(v.limits[k] as number)} (${n})`;
		return 'No limit';
	}
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(counts.map((f) => [f.key, num(form[f.key])]));
		const mib = num(form.memory_mib);
		const cpus = num(form.cpus);
		body.team_workspaces = num(form.team_workspaces);
		if ([...Object.values(body), mib].some((n) => n != null && (!Number.isInteger(n) || n < 0)) || (cpus != null && !(cpus >= 0))) {
			error = 'Limits must be whole numbers of 0 or more (CPU may have decimals); leave a field empty to fall back.';
			return;
		}
		body.memory_bytes = mib == null ? null : Math.round(mib * 2 ** 20);
		body.nano_cpus = cpus == null ? null : Math.round(cpus * 1e9);
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
				<input class="field font-mono" type="number" min="0" step="1" placeholder={fallback(f.key)} bind:value={form[f.key]} disabled={!editable || busy} />
			</label>
		{/each}
		<label class="block">
			<span class="label">Memory (MiB)</span>
			<input class="field font-mono" type="number" min="0" step="1" placeholder={fallback('memory_bytes', fmtBytes)} bind:value={form.memory_mib} disabled={!editable || busy} />
		</label>
		<label class="block">
			<span class="label">CPU cores</span>
			<input class="field font-mono" type="number" min="0" step="0.25" placeholder={fallback('nano_cpus')} bind:value={form.cpus} disabled={!editable || busy} />
		</label>
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
