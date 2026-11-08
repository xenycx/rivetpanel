<script lang="ts">
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { join, resolveFolder, shown, within } from '$lib/files/paths';

	// Picks a destination folder for one file or folder. The field accepts a
	// typed path (missing folders are created by the move); the list below
	// browses whatever folder the field names, so clicking and typing agree.
	let {
		open = $bindable(false),
		from,
		isDir,
		cwd,
		rootLabel = 'workspace',
		listFolders,
		onmove
	}: {
		open?: boolean;
		/** Workspace path of the entry being moved. */
		from: string;
		isDir: boolean;
		cwd: string;
		rootLabel?: string;
		/** Folder names directly inside a folder; rejects when it does not exist. */
		listFolders: (dir: string) => Promise<string[]>;
		onmove: (destFolder: string) => void;
	} = $props();

	const name = $derived(from.slice(from.lastIndexOf('/') + 1));
	let text = $state('');
	let folders = $state<string[] | null>(null);
	let missing = $state(false);
	let browsed = $state('');

	$effect(() => {
		if (open) text = shown(cwd);
	});

	const target = $derived(resolveFolder(cwd, text || '/', rootLabel));
	const dest = $derived('path' in target ? target.path : null);
	const problem = $derived.by(() => {
		if ('error' in target) return target.error;
		if (isDir && within(target.path, from)) return 'A folder cannot move into itself.';
		return null;
	});
	const here = $derived(dest !== null && dest === cwd);

	// Browse the named folder, a moment after typing settles.
	let seq = 0;
	$effect(() => {
		const d = dest;
		if (!open || d === null) return;
		const mine = ++seq;
		const t = setTimeout(async () => {
			try {
				const names = await listFolders(d);
				if (mine !== seq) return;
				folders = names;
				missing = false;
			} catch {
				if (mine !== seq) return;
				folders = [];
				missing = true;
			}
			browsed = d;
		}, 120);
		return () => clearTimeout(t);
	});

	const crumbs = $derived(browsed ? browsed.split('/').map((n, i, a) => ({ name: n, path: a.slice(0, i + 1).join('/') })) : []);
	const go = (p: string) => (text = shown(p));
	function submit(e: SubmitEvent) {
		e.preventDefault();
		if (dest === null || problem || here) return;
		open = false;
		onmove(dest);
	}
</script>

<Dialog bind:open title="Move {name}" size="sm">
	<form id="move-form" onsubmit={submit} novalidate>
		<label class="block">
			<span class="label">Destination folder</span>
			<input
				class="field font-mono"
				bind:value={text}
				aria-invalid={problem ? 'true' : undefined}
				aria-describedby="move-note"
				autocomplete="off"
				spellcheck="false"
			/>
		</label>
		<p id="move-note" class="mt-1 min-h-[1lh] text-small {problem ? 'text-fail' : 'text-muted'}" aria-live="polite">
			{#if problem}{problem}{:else if here}This is where it is now. Pick a folder below or type a path.{:else if dest !== null && missing && browsed === dest}{shown(dest)} does not exist yet. It is created when you move.{:else if dest !== null}Moves to {shown(join(dest, name))}{/if}
		</p>
	</form>

	<div class="mt-3 overflow-hidden rounded-control border border-rule-soft">
		<nav class="flex min-w-0 flex-wrap items-center gap-0.5 border-b border-rule-soft bg-paper px-2 py-1.5 text-small" aria-label="Browsing">
			<button type="button" class="rounded-control px-1 py-0.5 font-mono hover:bg-paper-2 {browsed ? 'text-action' : 'font-semibold'}" onclick={() => go('')}>{rootLabel}</button>
			{#each crumbs as c, i (c.path)}
				<Icon name="chevronRight" size={12} class="text-muted" />
				<button type="button" class="max-w-32 truncate rounded-control px-1 py-0.5 font-mono hover:bg-paper-2 {i === crumbs.length - 1 ? 'font-semibold' : 'text-action'}" onclick={() => go(c.path)}>{c.name}</button>
			{/each}
		</nav>
		<ul class="h-52 overflow-auto p-1" aria-label="Folders in {shown(browsed)}">
			{#if folders === null}
				<li class="px-2 py-2 text-muted">Loading…</li>
			{:else}
				{#each folders as f (f)}
					{@const p = join(browsed, f)}
					{@const self = isDir && within(p, from)}
					<li>
						<button
							type="button"
							class="flex w-full items-center gap-2 rounded-control px-2 py-1 text-left hover:bg-paper-2 disabled:cursor-not-allowed disabled:text-muted disabled:hover:bg-transparent"
							disabled={self}
							title={self ? 'A folder cannot move into itself' : undefined}
							onclick={() => go(p)}
						>
							<Icon name="folder" size={15} class="text-muted" /><span class="min-w-0 flex-1 truncate">{f}</span>
							{#if p === cwd}<span class="shrink-0 text-small text-muted">current</span>{:else if !self}<Icon name="chevronRight" size={13} class="text-muted" />{/if}
						</button>
					</li>
				{:else}
					<li class="px-2 py-3 text-muted">{missing ? 'New folder. Nothing inside yet.' : here ? 'No folders inside. Type a new folder name above.' : 'No folders inside. Move here, or type a new folder name above.'}</li>
				{/each}
			{/if}
		</ul>
	</div>

	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="move-form" disabled={dest === null || !!problem || here}>Move here</button>
	{/snippet}
</Dialog>
