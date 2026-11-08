<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { LimitDefaults } from '$lib/api/types';
	import ResourcePicker from '$lib/components/ResourcePicker.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { countFields, countHelp, fmtAllowance, sourceText, type LimitSource } from '$lib/limits';
	import { session } from '$lib/session.svelte';

	// The default allowance every workspace inherits unless it has its own.
	// An empty field falls back to the environment, else the built-in value.
	type View = { limits: LimitDefaults; source: Partial<Record<keyof LimitDefaults, Exclude<LimitSource, 'workspace'>>>; builtin_team_workspaces: number };
	type Key = keyof LimitDefaults;
	// The environment variable each default falls back to, when there is one.
	const envName: Partial<Record<Key, string>> = { max_apps: 'RIVET_MAX_BOTS_PER_USER', max_sites: 'RIVET_MAX_SITES_PER_USER', memory_bytes: 'RIVET_USER_MEMORY_BYTES' };
	// What applies with no panel value and no environment value.
	const builtin: Partial<Record<Key, string>> = { max_sites: '10' };

	let v = $state<View | null>(null);
	let form = $state<Record<string, number | string | null>>({});
	// Bytes and nano-CPUs; null falls back to the environment.
	let memory = $state<number | null>(null);
	let cpu = $state<number | null>(null);
	let error = $state('');
	let saved = $state('');
	let busy = $state(false);
	// Only built-in administrators change the allowance; settings managers read it.
	const editable = $derived(session.user?.role === 'admin');

	function fill(r: View) {
		v = r;
		const own = (k: Key) => r.source[k] === 'panel';
		const val = (k: Key) => (own(k) && r.limits[k] != null ? r.limits[k] : null);
		form = Object.fromEntries(countFields.map((f) => [f.quota, val(f.quota) == null ? '' : String(val(f.quota))]));
		memory = val('memory_bytes');
		cpu = val('nano_cpus');
		form.team_workspaces = own('team_workspaces') ? String(r.limits.team_workspaces) : '';
	}
	onMount(() => {
		api<View>('GET', '/admin/limits')
			.then(fill)
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The default allowance could not be loaded.'));
	});

	// What an empty field falls back to, with its source. Once a panel value
	// is set, the fallback is the environment value when one is set, else the
	// built-in one.
	function fallback(k: Key, kind: 'count' | 'memory' | 'cpu' = 'count'): string {
		if (!v) return '';
		const env = envName[k];
		const base = k === 'team_workspaces' ? String(v.builtin_team_workspaces) : (builtin[k] ?? 'no limit');
		if (v.source[k] === 'env') return `Empty: ${fmtAllowance(kind, v.limits[k] as number | null).replace('No limit', 'no limit')} · ${sourceText.env} (${env})`;
		if (v.source[k] === 'panel') return env ? `Empty: ${env} if set, else ${base} · ${sourceText.builtin}` : `Empty: ${base} · ${sourceText.builtin}`;
		return `Empty: ${base} · ${sourceText.builtin}`;
	}
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	// One account owns its personal workspace and up to this many team
	// workspaces, and each inherits the default allowance.
	const teamWorkspaces = $derived(num(form.team_workspaces) ?? (v?.source.team_workspaces === 'panel' ? v.limits.team_workspaces : (v?.builtin_team_workspaces ?? 20)));
	const ceiling = $derived.by(() => {
		if (!v || teamWorkspaces == null || !Number.isInteger(teamWorkspaces)) return '';
		const n = teamWorkspaces + 1;
		const parts: string[] = [];
		const apps = num(form.max_apps) ?? v.limits.max_apps;
		const sites = num(form.max_sites) ?? v.limits.max_sites;
		const mem = memory ?? v.limits.memory_bytes;
		if (apps != null) parts.push(`${apps * n} apps`);
		if (sites != null) parts.push(`${sites * n} sites`);
		if (mem != null) parts.push(`${fmtBytes(mem * n).replace('.0 ', ' ')} of memory`);
		return `Ceiling per account: ${n} workspaces (personal plus ${teamWorkspaces} team), each with its own default allowance${parts.length ? `, so up to ${parts.join(', ')}` : ', so no limit where the allowance has none'}.`;
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(countFields.map((f) => [f.quota, num(form[f.quota])]));
		body.team_workspaces = num(form.team_workspaces);
		if (Object.values(body).some((n) => n != null && (!Number.isInteger(n) || n < 0))) {
			error = 'Use whole numbers of 0 or more, or leave a field empty to fall back.';
			return;
		}
		body.memory_bytes = memory;
		body.nano_cpus = cpu;
		busy = true;
		error = saved = '';
		try {
			fill(await api<View>('PUT', '/admin/limits', body));
			saved = 'Default allowance saved. It applies to the next app or site created.';
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The default allowance could not be saved.';
		} finally {
			busy = false;
		}
	}
</script>

{#if error}<Notice tone="fail" class="mb-3" live>{error}</Notice>{/if}
{#if saved}<Notice tone="success" class="mb-3" live>{saved}</Notice>{/if}
{#if v}
	<form class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" onsubmit={submit}>
		{#each countFields as f (f.key)}
			<label class="block">
				<span class="label">{f.label}</span>
				<input class="field font-mono" type="number" min="0" step="1" placeholder="Not set" bind:value={form[f.quota]} disabled={!editable || busy} />
				<span class="help">{fallback(f.quota)}. 0 allows none. {countHelp[f.key]}</span>
			</label>
		{/each}
		<div class="sm:col-span-2 lg:col-span-3">
			<span class="label" id="default-memory-l">Memory</span>
			<ResourcePicker kind="memory" id="default-memory" labelledby="default-memory-l" allowEmpty emptyLabel="Not set" value={memory} onchange={(x) => (memory = x)} disabled={!editable || busy} />
			<span class="help">{fallback('memory_bytes', 'memory')}. The memory sizes of all apps in a workspace, add-ons included.</span>
		</div>
		<div class="sm:col-span-2 lg:col-span-3">
			<span class="label" id="default-cpu-l">CPU</span>
			<ResourcePicker kind="cpu" id="default-cpu" labelledby="default-cpu-l" allowEmpty emptyLabel="Not set" value={cpu} onchange={(x) => (cpu = x)} disabled={!editable || busy} />
			<span class="help">{fallback('nano_cpus', 'cpu')}. The CPU sizes of all apps in a workspace, in cores.</span>
		</div>
		<label class="block sm:col-span-2 lg:col-span-3">
			<span class="label">Team workspaces per account</span>
			<input class="field max-w-48 font-mono" type="number" min="0" max="1000" step="1" placeholder="Not set" bind:value={form.team_workspaces} disabled={!editable || busy} />
			<span class="help">{fallback('team_workspaces')}. 0 stops users from creating team workspaces. {ceiling}</span>
		</label>
		{#if editable}
			<div class="sm:col-span-2 lg:col-span-3"><button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save default allowance'}</button></div>
		{:else}
			<p class="text-small text-muted sm:col-span-2 lg:col-span-3">Only administrators can change the default allowance.</p>
		{/if}
	</form>
{/if}
