<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, MiB } from '$lib/api/client';
	import { power, type PowerAction } from '$lib/api/bots';
	import { Perm, type Bot, type Site } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { resolveTab, tabNav, tabsFor } from '$lib/tabs';
	import { joinArgs, splitArgs } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import TabBar from '$lib/components/ui/TabBar.svelte';
	import Addons from '$lib/components/Addons.svelte';
	import Backups from '$lib/components/Backups.svelte';
	import Console from '$lib/components/Console.svelte';
	import Deploy from '$lib/components/Deploy.svelte';
	import Env from '$lib/components/Env.svelte';
	import Files from '$lib/components/Files.svelte';
	import RecentOperations from '$lib/components/RecentOperations.svelte';

	// A site deployed from a repository: its web unit's logs, files,
	// deployments, configuration, variables and services, managed from the site.
	let { site, app, canEdit, canAdmin, onChanged }: { site: Site; app: Bot; canEdit: boolean; canAdmin: boolean; onChanged: () => void } = $props();

	const isApp = $derived(site.mode === 'app');
	const stopped = $derived(app.desired_state === 'stopped' && (app.observed_state === 'stopped' || app.observed_state === 'failed'));
	// The same sections, labels and ?tab= values as every other app page, from
	// the tab registry; the site's former ids (deployments, environment, build,
	// logs) still open their tab.
	const tabs = $derived(
		tabsFor({ kind: 'web', bot: app, site, features: session.features, game: null, allow: (p) => (p === -1 ? canAdmin : p === Perm.admin ? canEdit : true) })
	);
	const resolved = $derived(resolveTab(page.url.searchParams.get('tab'), 'web', tabs));
	const tab = $derived(resolved.id);
	$effect(() => {
		if (resolved.canonical) {
			const u = new URL(page.url);
			u.searchParams.set('tab', resolved.canonical);
			goto(u.pathname + u.search, { replaceState: true, noScroll: true, keepFocus: true });
		}
	});
	const nav = $derived(tabNav(tabs, tab, 'web', (id) => `?tab=${id}`));
	const stateLabel: Record<string, string> = { running: 'Running', building: 'Building', starting: 'Starting', stopping: 'Stopping', stopped: 'Stopped', failed: 'Failed', unknown: 'Unknown' };
	const stateTone: Record<string, string | undefined> = { running: 'run', building: 'warn', starting: 'warn', stopping: 'warn', failed: 'fail' };

	async function act(a: PowerAction) {
		if (await power(app, a)) onChanged();
	}
	async function redeploy() {
		try {
			await api('POST', `/sites/${site.id}/deploy`);
			toast(isApp ? 'Deploying the latest commit' : 'Building the latest commit');
			onChanged();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The deployment could not start.', 'fail');
		}
	}

	let form = $state({ build: '', output: '', command: '', port: 3000, health: '/', release: '', memory: 0, body: 0 });
	let saving = $state(false);
	function reset() {
		form = {
			build: app.build_command,
			output: site.build_output_dir || '.',
			command: joinArgs(app.argv),
			port: site.app_port,
			health: site.health_path,
			release: app.release_command ?? '',
			memory: Math.round(app.memory_bytes / MiB),
			body: site.max_body_bytes ? Math.round(site.max_body_bytes / MiB) : 0
		};
	}
	$effect(() => {
		void app.generation;
		void site.updated_at_ms;
		reset();
	});
	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			const botPatch: Record<string, unknown> = {};
			if (form.build !== app.build_command) botPatch.build_command = form.build;
			if (isApp) {
				const argv = splitArgs(form.command);
				if (joinArgs(argv) !== joinArgs(app.argv)) botPatch.argv = argv;
				if (form.release !== (app.release_command ?? '')) botPatch.release_command = form.release;
				if (form.memory && Math.round(form.memory * MiB) !== app.memory_bytes) botPatch.memory_bytes = Math.round(form.memory * MiB);
			}
			if (Object.keys(botPatch).length) await api('PATCH', `/bots/${app.id}`, botPatch);
			const sitePatch: Record<string, unknown> = {};
			if (!isApp && form.output !== (site.build_output_dir || '.')) sitePatch.build_output_dir = form.output;
			if (isApp) {
				if (form.port !== site.app_port) sitePatch.app_port = form.port;
				if (form.health !== site.health_path) sitePatch.health_path = form.health;
				const body = form.body ? Math.round(form.body * MiB) : 0;
				if (body !== site.max_body_bytes) sitePatch.max_body_bytes = body;
			}
			if (Object.keys(sitePatch).length) await api('PATCH', `/sites/${site.id}`, sitePatch);
			toast(isApp ? (stopped ? 'Saved; start the app to use the new settings' : 'Saved; restart the app to use the new settings') : 'Saved; the next deployment uses them', 'success');
			onChanged();
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The settings could not be saved.', 'fail');
		} finally {
			saving = false;
		}
	}
</script>

<section class="card overflow-hidden" aria-label="Deployment">
	<div class="flex flex-wrap items-center gap-3 px-5 pt-5">
		<span class="grid size-9 shrink-0 place-items-center rounded-control bg-paper-2 text-action"><Icon name={isApp ? 'box' : 'layers'} size={16} /></span>
		<div class="min-w-0 flex-1 basis-60">
			<p class="font-semibold">{isApp ? 'App' : 'Static build'} <span class="font-normal text-muted">· {app.runtime}{site.framework ? ` · ${site.framework}` : ''}</span></p>
			<p class="text-small text-muted">{isApp ? `Listens on port ${site.app_port}; /data keeps its files across deploys.` : `Each deployment builds the repository and publishes ${site.build_output_dir || 'the repository'}.`}</p>
		</div>
		{#if isApp}<span class="pill" data-tone={stateTone[app.observed_state]}>{stateLabel[app.observed_state] ?? app.observed_state}</span>{/if}
		{#if canEdit}
			<div class="flex flex-wrap gap-2">
				{#if isApp}
					{#if app.desired_state === 'running'}
						<button class="btn btn-sm" onclick={() => act('restart')}><Icon name="restart" size={14} />Restart</button>
						<button class="btn btn-sm" onclick={() => act('stop')}><Icon name="stop" size={14} />Stop</button>
					{:else}
						<button class="btn btn-sm" onclick={() => act('start')}><Icon name="play" size={14} />Start</button>
					{/if}
				{/if}
				<button class="btn btn-sm btn-primary" onclick={redeploy}><Icon name="rocket" size={14} />Deploy latest</button>
			</div>
		{/if}
	</div>
	{#if isApp && app.last_error && app.observed_state === 'failed'}<Notice tone="fail" class="mx-5 mt-3">{app.last_error}</Notice>{/if}
	<div class="mt-4 px-3"><TabBar tabs={nav.primary} current={nav.active} label="Site sections" /></div>
	{#if nav.secondary.length}<div class="border-t border-rule-soft px-3 py-2"><TabBar tabs={nav.secondary} current={tab} variant="pill" label="{nav.groupLabel} sections" /></div>{/if}
	<div class="border-t border-rule-soft p-5">
		{#if tab === 'deploy'}
			<Deploy bot={app} admin={canAdmin} />
		{:else if tab === 'env'}
			{#if !isApp}<p class="mb-3 text-small text-muted">A static build only receives public variables (such as <code>VITE_*</code> and <code>PUBLIC_*</code>); they end up in the published files.</p>{/if}
			<Env bot={app} running={!stopped} />
		{:else if tab === 'addons'}
			<Addons bot={app} {stopped} onChanged={onChanged} />
		{:else if tab === 'backups'}
			<p class="mb-3 text-small text-muted">Backups hold the {isApp ? "app's files, including its /data folder," : 'checkout of the repository'} and, if you choose, its variables. {isApp ? '' : 'Published releases are kept separately under Releases.'}</p>
			<Backups bot={app} {stopped} admin={canAdmin} onSaved={() => onChanged()} />
		{:else if tab === 'files'}
			{#if isApp}<p class="mb-3 text-small text-muted">The checkout of the repository. <code>.rivet-data</code> is the app's <code>/data</code> folder: deployments never touch it and it is backed up with the rest.</p>{/if}
			<Files botId={app.id} running={!stopped} rootLabel={isApp ? 'app' : 'checkout'} />
		{:else if tab === 'startup'}
			<form class="grid gap-4 sm:grid-cols-2" onsubmit={save}>
				<label class="block sm:col-span-2"><span class="label">Build command</span>
					<textarea class="field font-mono text-small" rows="3" bind:value={form.build} placeholder={isApp ? 'Empty: the runtime default' : 'npm ci && npm run build'}></textarea>
					<span class="help">Runs in a build container without secrets (only public variables).</span>
				</label>
				{#if !isApp}
					<label class="block"><span class="label">Output folder</span><input class="field font-mono" bind:value={form.output} /></label>
				{:else}
					<label class="block sm:col-span-2"><span class="label">Start command</span><input class="field font-mono" required bind:value={form.command} /></label>
					<label class="block"><span class="label">Port</span><input class="field font-mono" type="number" min="1" max="65535" bind:value={form.port} /><span class="help">Passed to the app as <code>$PORT</code>.</span></label>
					<label class="block"><span class="label">Health check path</span><input class="field font-mono" bind:value={form.health} /></label>
					<label class="block sm:col-span-2"><span class="label">Release command</span><input class="field font-mono" bind:value={form.release} placeholder="none" /><span class="help">Runs before each start with the app's variables (database migrations).</span></label>
					<label class="block"><span class="label">Memory (MiB)</span><input class="field font-mono" type="number" min="64" step="64" bind:value={form.memory} /><span class="help">The app's size; it counts toward the workspace memory allowance.</span></label>
					<label class="block"><span class="label">Request body limit (MiB)</span><input class="field font-mono" type="number" min="0" bind:value={form.body} /><span class="help">0: the panel default (RIVET_SITES_MAX_BODY_BYTES, 100 MiB unless set).</span></label>
				{/if}
				{#if isApp && !stopped}<Notice tone="info" class="sm:col-span-2">The app is running: a new start command, memory or build applies after a restart, and the port needs a stopped app. The address and health check change at once.</Notice>{/if}
				<div class="sm:col-span-2 flex gap-2"><button class="btn btn-primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button><button type="button" class="btn btn-quiet" onclick={reset}>Reset</button></div>
			</form>
		{:else}
			{#if isApp}<Console bot={app} />{/if}
			<div class={isApp ? 'mt-6' : ''}><RecentOperations botId={app.id} /></div>
		{/if}
	</div>
</section>
