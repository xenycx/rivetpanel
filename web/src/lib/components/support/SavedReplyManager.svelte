<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import type { SavedReply } from '$lib/api/support';
	import { support, loadReplies } from '$lib/supportData.svelte';

	// Saved replies, shared by everyone who manages tickets.
	let { open = $bindable(false) }: { open?: boolean } = $props();

	let editing = $state<string | null>(null);
	let title = $state('');
	let body = $state('');
	let error = $state('');
	let busy = $state(false);
	$effect(() => {
		if (open) loadReplies(true);
	});
	function edit(r: SavedReply) {
		editing = r.id;
		title = r.title;
		body = r.body;
	}
	function reset() {
		editing = null;
		title = '';
		body = '';
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			if (editing) await api('PATCH', `/tickets/replies/${editing}`, { title, body });
			else await api('POST', '/tickets/replies', { title, body });
			reset();
			await loadReplies(true);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The request failed.';
		} finally {
			busy = false;
		}
	}
	async function remove(r: SavedReply) {
		if (!(await confirmDialog({ title: `Delete “${r.title}”?`, body: 'The saved reply is removed for the whole support team. Messages already sent keep their text.', confirmLabel: 'Delete', tone: 'danger' })))
			return;
		try {
			await api('DELETE', `/tickets/replies/${r.id}`);
			if (editing === r.id) reset();
			await loadReplies(true);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The request failed.';
		}
	}
</script>

<Dialog bind:open title="Saved replies" description="Answers the support team reuses. Insert one from the reply box with / or the Saved replies button." size="lg">
	<form class="grid gap-3" onsubmit={save}>
		<label class="block"><span class="label">Title</span><input class="field" maxlength="80" required bind:value={title} placeholder="Restart the bot first" /></label>
		<label class="block"><span class="label">Reply</span><textarea class="field min-h-28" maxlength="10000" required bind:value={body} placeholder="Markdown works: **bold**, `code`, lists and links."></textarea></label>
		<div class="flex gap-2">
			<button class="btn btn-primary" disabled={busy || !title.trim() || !body.trim()}>{editing ? 'Save changes' : 'Add saved reply'}</button>
			{#if editing}<button type="button" class="btn btn-quiet" onclick={reset}>Cancel</button>{/if}
		</div>
	</form>
	{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
	<ul class="list-card mt-4 max-h-80 overflow-y-auto">
		{#each support.replies as r (r.id)}
			<li class="flex items-start gap-2 px-3 py-2.5">
				<div class="min-w-0 flex-1">
					<p class="truncate font-medium">{r.title}</p>
					<p class="line-clamp-2 text-small text-muted">{r.body}</p>
				</div>
				<button type="button" class="btn btn-quiet h-8 px-2" onclick={() => edit(r)} aria-label="Edit {r.title}"><Icon name="pencil" size={13} /></button>
				<button type="button" class="btn btn-quiet h-8 px-2" onclick={() => remove(r)} aria-label="Delete {r.title}"><Icon name="trash" size={13} /></button>
			</li>
		{:else}
			<li class="px-3 py-6 text-center text-small text-muted">No saved replies yet.</li>
		{/each}
	</ul>
</Dialog>
