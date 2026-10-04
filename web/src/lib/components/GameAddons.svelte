<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Bot, type FileEntry } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot }: { bot: Bot } = $props();

	type Hit = { project_id: string; slug: string; title: string; description: string; author: string; downloads: number; icon_url: string };
	type Source = { kind: 'plugin' | 'mod'; loaders: string[]; dir: string };
	let q = $state('');
	let hits = $state<Hit[] | null>(null);
	let total = $state(0);
	let source = $state<Source | null>(null);
	let gameVersion = $state('');
	let installed = $state<FileEntry[]>([]);
	let error = $state('');
	let installing = $state<Record<string, boolean>>({});
	let needs = $state<{ from: string; ids: string[] } | null>(null);
	const canEdit = $derived(can(bot, Perm.files));
	const noun = $derived(source?.kind === 'mod' ? 'mod' : 'plugin');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function search(offset = 0) {
		try {
			const r = await api<{ source: Source; game_version: string; hits: Hit[]; total: number }>('GET', `/bots/${bot.id}/game/addons?q=${encodeURIComponent(q)}&offset=${offset}`);
			source = r.source;
			gameVersion = r.game_version;
			hits = offset ? [...(hits ?? []), ...r.hits] : r.hits;
			total = r.total;
			error = '';
			loadInstalled();
		} catch (e) {
			error = msg(e);
			hits = hits ?? [];
		}
	}
	async function loadInstalled() {
		if (!source || !canEdit) return;
		try {
			installed = (await api<{ entries: FileEntry[] }>('GET', `/bots/${bot.id}/files?path=${encodeURIComponent(source.dir)}`)).entries.filter((e) => e.type === 'file' && e.name.endsWith('.jar'));
		} catch {
			installed = [];
		}
	}
	onMount(() => search());
	let timer: ReturnType<typeof setTimeout>;
	function onInput() {
		clearTimeout(timer);
		timer = setTimeout(() => search(), 350);
	}

	async function install(project: string, title: string) {
		installing[project] = true;
		try {
			const r = await api<{ filename: string; required: string[] }>('POST', `/bots/${bot.id}/game/addons`, { project, version: '' });
			toast(`Installed ${r.filename}. ${bot.observed_state === 'running' ? 'Restart the server to load it.' : 'It loads at the next start.'}`, 'success');
			needs = r.required.length ? { from: title, ids: r.required } : null;
			await loadInstalled();
		} catch (e) {
			toast(`${title}: ${msg(e)}`, 'fail');
		} finally {
			installing[project] = false;
		}
	}
	async function remove(f: FileEntry) {
		if (!source) return;
		if (!(await confirmDialog({ title: `Remove ${f.name}?`, body: `The file is deleted from ${source.dir}. Its configuration folder, if any, is kept.`, confirmLabel: 'Remove', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/bots/${bot.id}/files?path=${encodeURIComponent(source.dir + '/' + f.name)}`);
			await loadInstalled();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const fmtCount = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? Math.round(n / 1e3) + 'k' : String(n));
</script>

<div class="grid gap-5">
	<div class="flex flex-wrap items-end gap-3">
		<label class="relative min-w-0 flex-1 basis-72">
			<span class="label">Search Modrinth</span>
			<Icon name="search" class="pointer-events-none absolute bottom-2.5 left-2.5 text-muted" />
			<input class="field pl-8" type="search" bind:value={q} oninput={onInput} placeholder="For example: LuckPerms, EssentialsX, Sodium" />
		</label>
		{#if source}<p class="text-small text-muted">Showing {source.loaders.join(', ')} {noun}s{gameVersion ? ` for Minecraft ${gameVersion}` : ''}, installed into <code>{source.dir}/</code>.</p>{/if}
	</div>

	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	{#if needs}
		<Notice tone="warn" title="{needs.from} needs other {noun}s">
			It lists {needs.ids.length} required {needs.ids.length === 1 ? 'dependency' : 'dependencies'}. Install them too, or the server may refuse to load it.
			{#snippet action()}
				{#each needs?.ids ?? [] as id (id)}<button class="btn btn-sm" disabled={installing[id]} onclick={() => install(id, 'Dependency')}>Install {id}</button>{/each}
			{/snippet}
		</Notice>
	{/if}

	{#if canEdit && source}
		<section class="card p-4">
			<h2 class="font-semibold">Installed {noun}s <span class="text-small font-normal text-muted">· {installed.length}</span></h2>
			<ul class="mt-2 grid gap-1 sm:grid-cols-2">
				{#each installed as f (f.name)}
					<li class="flex items-center gap-2 rounded-control px-2 py-1.5 hover:bg-paper-2/60">
						<Icon name="package" size={14} class="text-muted" />
						<span class="min-w-0 flex-1 truncate font-mono text-small">{f.name}</span>
						<button class="btn btn-quiet btn-icon btn-sm text-fail" aria-label="Remove {f.name}" onclick={() => remove(f)}><Icon name="trash" size={13} /></button>
					</li>
				{:else}
					<li class="text-small text-muted">None yet.</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if hits === null}
		<Skeleton rows={4} label="Searching Modrinth" />
	{:else}
		<ul class="grid gap-3 md:grid-cols-2">
			{#each hits as h (h.project_id)}
				<li class="card flex gap-3 p-4">
					{#if h.icon_url}<img src={h.icon_url} alt="" class="size-12 shrink-0 rounded-tile object-cover" referrerpolicy="no-referrer" loading="lazy" />{:else}<span class="grid size-12 shrink-0 place-items-center rounded-tile bg-paper-2 text-action"><Icon name="package" /></span>{/if}
					<div class="min-w-0 flex-1">
						<a class="font-semibold hover:underline" href="https://modrinth.com/project/{h.slug}" target="_blank" rel="noopener">{h.title}</a>
						<p class="text-small text-muted">by {h.author} · {fmtCount(h.downloads)} downloads</p>
						<p class="mt-1 line-clamp-2 text-small">{h.description}</p>
					</div>
					{#if canEdit}<button class="btn btn-sm self-start" disabled={installing[h.project_id]} onclick={() => install(h.project_id, h.title)}>{installing[h.project_id] ? 'Installing…' : 'Install'}</button>{/if}
				</li>
			{:else}
				<li class="text-muted">Nothing found{gameVersion ? ` for Minecraft ${gameVersion}` : ''}.</li>
			{/each}
		</ul>
		{#if hits.length < total}<button class="btn w-fit" onclick={() => search(hits?.length ?? 0)}>Show more</button>{/if}
		<p class="text-small text-muted">Files come from Modrinth's CDN and are checked against their published SHA-512 before they are saved. RivetPanel installs the newest version that supports this server's loader and Minecraft version.</p>
	{/if}
</div>
