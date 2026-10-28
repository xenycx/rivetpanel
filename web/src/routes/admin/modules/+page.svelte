<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { EnvView } from '$lib/api/admin';
	import type { Module, ModuleState } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { restartConfirm, restartPanel } from '$lib/restart';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	const NAME = 'RIVET_MODULES';
	let modules = $state<Module[] | null>(null);
	let env = $state<EnvView | null>(null);
	let error = $state('');
	let saving = $state(false);
	let restarting = $state(false);
	// The states the saved RIVET_MODULES asks for (applied after a restart).
	let wanted = $state<Record<string, ModuleState>>({});

	const variable = $derived(env?.vars.find((v) => v.name === NAME));
	// Set in the environment file and not overridden here: still editable, the
	// value saved here takes priority.
	function parse(value: string): Record<string, ModuleState> {
		const out: Record<string, ModuleState> = {};
		for (const raw of value.split(',')) {
			const [k, st] = raw.split('=').map((x) => x.trim());
			if (k && (st === 'off' || st === 'preview' || st === 'stable')) out[k] = st;
		}
		return out;
	}
	async function load() {
		try {
			const [m, e] = await Promise.all([api<{ modules: Module[] }>('GET', '/modules'), api<EnvView>('GET', '/admin/environment')]);
			modules = m.modules;
			env = e;
			const saved = parse(e.vars.find((v) => v.name === NAME)?.value ?? '');
			wanted = Object.fromEntries(m.modules.map((x) => [x.key, saved[x.key] ?? x.default]));
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The module catalog could not be loaded.';
		}
	}
	onMount(load);

	// Only modules that differ from the built-in state are written.
	function serialize(w: Record<string, ModuleState>): string {
		return (modules ?? [])
			.filter((m) => w[m.key] !== m.default)
			.map((m) => `${m.key}=${w[m.key]}`)
			.join(',');
	}
	async function choose(m: Module, st: ModuleState) {
		if (wanted[m.key] === st) return;
		const next = { ...wanted, [m.key]: st };
		const value = serialize(next);
		saving = true;
		try {
			env = await api<EnvView>('PUT', '/admin/environment', value ? { set: { [NAME]: value }, unset: [] } : { set: {}, unset: [NAME] });
			wanted = next;
			toast(`${m.label}: ${label[st].toLowerCase()} after a restart`, 'success');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The change could not be saved.', 'fail');
		} finally {
			saving = false;
		}
	}
	const pending = $derived((modules ?? []).some((m) => wanted[m.key] && wanted[m.key] !== m.state));
	async function restart() {
		if (!(await confirmDialog(restartConfirm))) return;
		restarting = true;
		try {
			const back = await restartPanel();
			await load();
			if (back) toast('RivetPanel restarted with the new modules', 'success');
			else error = 'The panel did not come back within 90 seconds. Check the service on the host.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The restart could not be requested.';
		} finally {
			restarting = false;
		}
	}

	const label: Record<ModuleState, string> = { off: 'Off', preview: 'Preview', stable: 'On' };
	const tone = (state: ModuleState) => (state === 'stable' ? 'run' : state === 'preview' ? 'warn' : undefined);
</script>

<svelte:head><title>Modules · RivetPanel</title></svelte:head>

<h2 class="text-section">Modules</h2>
<p class="mt-1 max-w-3xl text-muted">On modules are ready for normal use. Preview modules are opt-in and may still change; turning one on does not mean every planned part is built. Off modules show no pages and answer no API calls. Changes are saved to <code>{NAME}</code> and apply after a restart.</p>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if restarting}
	<Notice tone="info" class="mt-4" live title="Restarting…">Waiting for RivetPanel to come back with the new modules.</Notice>
{:else if pending}
	<Notice tone="warn" class="mt-4" title="Saved changes not in effect yet">
		The panel is still running with the modules it started with.
		{#if !env?.can_restart}Restart the service on the host: <code>sudo systemctl restart rivetpanel</code> or <code>docker compose restart</code>.{/if}
		{#snippet action()}{#if env?.can_restart}<button class="btn btn-sm btn-primary" onclick={restart}><Icon name="restart" size={13} />Restart panel</button>{/if}{/snippet}
	</Notice>
{/if}
{#if variable?.env_set && !variable.override}
	<p class="mt-3 text-small text-muted">The environment file sets <code>{NAME}={variable.env_value}</code>. A change here is saved in the panel and replaces it.</p>
{/if}

{#if !modules && !error}
	<div class="mt-5"><Skeleton rows={6} label="Loading modules" /></div>
{:else if modules}
	<ul class="list-card mt-5">
		{#each modules as m (m.key)}
			{@const want = wanted[m.key] ?? m.state}
			<li class="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
				<div class="min-w-0">
					<div class="flex flex-wrap items-center gap-2">
						<h3 class="font-medium">{m.label}</h3>
						<code class="text-small text-muted">{m.key}</code>
						{#if want !== m.state}<span class="pill" data-tone="warn" title="Running now: {label[m.state]}">Restart to apply</span>{/if}
					</div>
					<p class="mt-1 text-small text-muted">{m.description}</p>
				</div>
				{#if m.key === 'core'}
					<span class="pill shrink-0" data-tone="run" title="Core hosting cannot be turned off">Always on</span>
				{:else}
					<div class="inline-flex shrink-0 rounded-control border border-rule bg-raised p-0.5" role="radiogroup" aria-label="{m.label} state">
						{#each ['off', 'preview', 'stable'] as const as st (st)}
							<button
								role="radio"
								aria-checked={want === st}
								disabled={saving || restarting}
								class="rounded-inner px-2.5 py-1 text-small font-medium {want === st ? 'bg-paper-2 text-ink shadow-[inset_0_0_0_1px_var(--color-rule)]' : 'text-muted hover:text-ink'}"
								onclick={() => choose(m, st)}>{#if want === st && tone(st)}<span class="mr-1.5 inline-block size-1.5 rounded-pill align-middle {st === 'stable' ? 'bg-run' : 'bg-warn'}"></span>{/if}{label[st]}</button
							>
						{/each}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
{/if}
