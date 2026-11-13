<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { labelColors, type Label, type LabelColor } from '$lib/api/support';
	import { support, loadLabels } from '$lib/supportData.svelte';
	import LabelChip from './LabelChip.svelte';

	// Staff-only labels: create, rename, recolor and delete. Requesters never
	// see labels.
	let { open = $bindable(false), onchange }: { open?: boolean; onchange?: () => void } = $props();

	let name = $state('');
	let color = $state<LabelColor>('data');
	let editing = $state<string | null>(null);
	let error = $state('');
	let busy = $state(false);
	$effect(() => {
		if (open) loadLabels(true);
	});
	function edit(l: Label) {
		editing = l.id;
		name = l.name;
		color = l.color;
	}
	function reset() {
		editing = null;
		name = '';
		color = 'data';
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			if (editing) await api('PATCH', `/tickets/labels/${editing}`, { name, color });
			else await api('POST', '/tickets/labels', { name, color });
			reset();
			await loadLabels(true);
			onchange?.();
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The request failed.';
		} finally {
			busy = false;
		}
	}
	async function remove(l: Label) {
		const ok = await confirmDialog({
			title: `Delete the label “${l.name}”?`,
			body: l.tickets ? `It is removed from ${l.tickets} ticket${l.tickets === 1 ? '' : 's'}. This cannot be undone.` : 'No ticket carries it.',
			confirmLabel: 'Delete label',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/tickets/labels/${l.id}`);
			if (editing === l.id) reset();
			await loadLabels(true);
			onchange?.();
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The request failed.';
		}
	}
</script>

<Dialog bind:open title="Labels" description="Tag tickets for the support team. Requesters never see labels.">
	<form class="grid gap-3" onsubmit={save}>
		<label class="block"><span class="label">{editing ? 'Rename label' : 'New label'}</span>
			<input class="field" maxlength="32" required bind:value={name} placeholder="bug, refund, vip…" />
		</label>
		<fieldset>
			<legend class="label">Color</legend>
			<div class="flex flex-wrap gap-1.5">
				{#each labelColors as c (c.id)}
					<label class="color-opt" title={c.name}>
						<input type="radio" class="sr-only" name="label-color" value={c.id} bind:group={color} />
						<LabelChip label={{ name: c.name, color: c.id }} />
					</label>
				{/each}
			</div>
		</fieldset>
		<div class="flex gap-2">
			<button class="btn btn-primary" disabled={busy || !name.trim()}>{editing ? 'Save label' : 'Add label'}</button>
			{#if editing}<button type="button" class="btn btn-quiet" onclick={reset}>Cancel</button>{/if}
		</div>
	</form>
	{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
	<ul class="list-card mt-4 max-h-72 overflow-y-auto">
		{#each support.labels as l (l.id)}
			<li class="flex items-center gap-2 px-3 py-2">
				<LabelChip label={l} />
				<span class="flex-1 text-small text-muted tabular-nums">{l.tickets} ticket{l.tickets === 1 ? '' : 's'}</span>
				<button type="button" class="btn btn-quiet h-8 px-2" onclick={() => edit(l)} aria-label="Edit {l.name}"><Icon name="pencil" size={13} /></button>
				<button type="button" class="btn btn-quiet h-8 px-2" onclick={() => remove(l)} aria-label="Delete {l.name}"><Icon name="trash" size={13} /></button>
			</li>
		{:else}
			<li class="px-3 py-6 text-center text-small text-muted">No labels yet. Add the first one above.</li>
		{/each}
	</ul>
</Dialog>

<style>
	.color-opt {
		border-radius: var(--radius-pill);
		padding: 2px;
		cursor: pointer;
	}
	.color-opt:has(input:checked) {
		box-shadow: 0 0 0 2px var(--color-action);
	}
	.color-opt:has(input:focus-visible) {
		outline: 2px solid var(--color-action);
		outline-offset: 2px;
	}
</style>
