<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Blueprint, EggDraft } from '$lib/api/games';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let list = $state<Blueprint[] | null>(null);
	let error = $state('');
	let importing = $state(false);
	let yaml = $state('');
	let importError = $state('');
	let busy = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	// Pterodactyl egg import: upload → review the draft and warnings → save.
	let eggOpen = $state(false);
	let eggText = $state('');
	let eggFile = $state('');
	let draft = $state<EggDraft | null>(null);
	let draftYaml = $state('');
	let eggError = $state('');
	let reviewed = $state(false);
	const EGG_MAX = 512 * 1024;

	function openEgg() {
		eggOpen = true;
		eggText = eggFile = draftYaml = eggError = '';
		draft = null;
		reviewed = false;
	}
	async function readEgg(ev: Event) {
		const f = (ev.target as HTMLInputElement).files?.[0];
		eggError = '';
		if (!f) return;
		if (f.size > EGG_MAX) {
			eggError = 'The egg is larger than 512 KiB.';
			return;
		}
		eggFile = f.name;
		eggText = await f.text();
	}
	async function previewEgg(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		eggError = '';
		try {
			draft = await api<EggDraft>('POST', '/admin/blueprints/egg-preview', { egg: eggText });
			draftYaml = draft.yaml;
			reviewed = false;
		} catch (err) {
			eggError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function saveDraft() {
		busy = true;
		eggError = '';
		try {
			const r = await api<{ slug: string; revision: number; changed: boolean }>('POST', '/admin/blueprints', { yaml: draftYaml });
			eggOpen = false;
			toast(r.changed ? `Saved ${r.slug} as revision ${r.revision}. It is offered for new servers.` : `${r.slug} is unchanged.`);
			await load();
		} catch (err) {
			eggError = msg(err);
		} finally {
			busy = false;
		}
	}

	async function load() {
		try {
			list = (await api<{ blueprints: Blueprint[] }>('GET', '/blueprints')).blueprints;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	async function setEnabled(b: Blueprint, enabled: boolean) {
		try {
			await api('PATCH', `/admin/blueprints/${b.id}`, { enabled });
			toast(enabled ? `${b.name} is offered for new servers.` : `${b.name} is hidden for new servers. Existing servers keep running.`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(b: Blueprint) {
		if (!(await confirmDialog({ title: `Delete ${b.name}?`, body: 'Every revision of this custom server type is deleted. This cannot be undone.', confirmLabel: 'Delete', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/admin/blueprints/${b.id}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function doImport(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		importError = '';
		try {
			const r = await api<{ slug: string; revision: number; changed: boolean }>('POST', '/admin/blueprints', { yaml });
			importing = false;
			yaml = '';
			toast(r.changed ? `Saved ${r.slug} as revision ${r.revision}.` : `${r.slug} is unchanged.`);
			await load();
		} catch (err) {
			importError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function readFile(ev: Event) {
		const f = (ev.target as HTMLInputElement).files?.[0];
		if (f) yaml = await f.text();
	}
</script>

<svelte:head><title>Server types · RivetPanel</title></svelte:head>

<div class="flex flex-wrap items-start justify-between gap-3">
	<div>
		<h2 class="text-section">Server types</h2>
		<p class="mt-1 max-w-3xl text-muted">Blueprints define how each kind of game server is installed and started. Built-in types update with RivetPanel; servers keep the revision they were created with until someone updates them from their Startup page.</p>
	</div>
	{#if session.features.games}
		<div class="flex flex-wrap gap-2">
			<button class="btn" onclick={openEgg}><Icon name="upload" size={14} />Import Pterodactyl egg</button>
			<button class="btn" onclick={() => ((importing = true), (importError = ''))}><Icon name="upload" size={14} />Import blueprint</button>
		</div>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if !session.features.games}
	<Notice tone="info" class="mt-5">Game servers are off. Enable the <code>game_servers</code> module under Modules to manage server types.</Notice>
{:else if list === null && !error}
	<div class="mt-5"><Skeleton rows={6} label="Loading server types" /></div>
{:else if list}
	<ul class="list-card mt-5">
		{#each list as b (b.id)}
			<li class="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between">
				<div class="min-w-0">
					<div class="flex flex-wrap items-center gap-2">
						<h3 class="font-medium">{b.name}</h3>
						<code class="text-small text-muted">{b.slug}</code>
						<span class="pill">{b.category}</span>
						{#if b.source === 'custom'}<span class="pill" data-tone="warn">Custom</span>{/if}
						{#if !b.enabled}<span class="pill" data-tone="fail">Hidden</span>{/if}
					</div>
					<p class="mt-1 text-small text-muted">Revision {b.current_revision} · {b.servers} {b.servers === 1 ? 'server' : 'servers'} · {b.spec.images.length} images · {b.spec.variables.length} settings</p>
				</div>
				<div class="flex shrink-0 flex-wrap gap-2">
					<a class="btn btn-sm" href="/api/v1/admin/blueprints/{b.slug}/export" download><Icon name="download" size={14} />Export</a>
					<button class="btn btn-sm" onclick={() => setEnabled(b, !b.enabled)}>{b.enabled ? 'Hide' : 'Offer'}</button>
					{#if b.source === 'custom' && b.servers === 0}<button class="btn btn-sm btn-quiet text-fail" onclick={() => remove(b)} aria-label="Delete {b.name}"><Icon name="trash" size={14} /></button>{/if}
				</div>
			</li>
		{/each}
	</ul>
{/if}

<Dialog bind:open={importing} title="Import a blueprint">
	<form class="grid gap-3" onsubmit={doImport}>
		<p class="text-small text-muted">Paste or choose a blueprint YAML file. A new slug creates a server type; an existing custom slug adds a revision. Install scripts run as the unprivileged server user, with internet access, inside a container.</p>
		<input type="file" accept=".yaml,.yml,text/yaml" onchange={readFile} />
		<textarea class="field min-h-72 font-mono text-[12px]" bind:value={yaml} spellcheck="false" placeholder="slug: my-game&#10;name: My game&#10;..." required></textarea>
		{#if importError}<Notice tone="fail" live>{importError}</Notice>{/if}
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => (importing = false)}>Cancel</button>
			<button class="btn btn-primary" disabled={busy || !yaml.trim()}>Import</button>
		</div>
	</form>
</Dialog>

<Dialog bind:open={eggOpen} title="Import a Pterodactyl egg">
	{#if !draft}
		<form class="grid gap-3" onsubmit={previewEgg}>
			<p class="text-small text-muted">Choose an egg export (PTDL_v1 or PTDL_v2 JSON, at most 512 KiB). It is converted into a blueprint draft for you to review; nothing is saved yet and nothing the egg links to is downloaded.</p>
			<input type="file" accept=".json,application/json" onchange={readEgg} aria-label="Egg file" />
			{#if eggFile}<p class="text-small text-muted">Loaded <code>{eggFile}</code> ({Math.ceil(eggText.length / 1024)} KiB).</p>{/if}
			{#if eggError}<Notice tone="fail" live>{eggError}</Notice>{/if}
			<div class="flex justify-end gap-2">
				<button type="button" class="btn" onclick={() => (eggOpen = false)}>Cancel</button>
				<button class="btn btn-primary" disabled={busy || !eggText.trim()}>Convert</button>
			</div>
		</form>
	{:else}
		<div class="grid gap-3">
			<p class="text-small text-muted">Converted <strong>{draft.name}</strong> ({draft.format}). Review every warning and the YAML below — especially the install script, images, ports and resources — then save it as a custom server type. The install script runs as the unprivileged server user, with internet access, inside the builder container.</p>
			{#if draft.warnings.length}
				<div>
					<h3 class="text-small font-medium">{draft.warnings.length} {draft.warnings.length === 1 ? 'warning' : 'warnings'}</h3>
					<ul class="mt-1 max-h-48 list-disc space-y-1 overflow-y-auto pl-5 text-small" aria-label="Import warnings">
						{#each draft.warnings as w, i (i)}<li>{w}</li>{/each}
					</ul>
				</div>
			{/if}
			{#if draft.error}<Notice tone="warn">The draft does not validate yet: {draft.error}. Fix it in the YAML before saving.</Notice>{/if}
			<label class="grid gap-1 text-small font-medium">Blueprint YAML
				<textarea class="field min-h-72 font-mono text-[12px] font-normal" bind:value={draftYaml} spellcheck="false"></textarea>
			</label>
			<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={reviewed} />I reviewed the warnings and the install script.</label>
			{#if eggError}<Notice tone="fail" live>{eggError}</Notice>{/if}
			<div class="flex justify-end gap-2">
				<button type="button" class="btn" onclick={() => ((draft = null), (eggError = ''))}>Back</button>
				<button class="btn btn-primary" disabled={busy || !reviewed || !draftYaml.trim()} onclick={saveDraft}>Save server type</button>
			</div>
		</div>
	{/if}
</Dialog>
