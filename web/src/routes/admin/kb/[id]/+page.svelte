<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import type { KBArticle, KBIndex } from '$lib/api/kb';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	const id = $derived(page.params.id ?? 'new');
	const isNew = $derived(id === 'new');
	let index = $state<KBIndex | null>(null);
	let form = $state({ title: '', slug: '', summary: '', body: '', category_id: '', status: 'draft', visibility: 'users', position: 0 });
	let saved = $state('');
	let loaded = $state(false);
	let error = $state('');
	let busy = $state(false);
	let preview = $state(false);
	let current = $state<KBArticle | null>(null);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load(articleId: string) {
		loaded = false;
		try {
			index = await api<KBIndex>('GET', '/kb/manage');
			if (articleId !== 'new') {
				const a = await api<KBArticle>('GET', `/kb/manage/articles/${articleId}`);
				current = a;
				form = { title: a.title, slug: a.slug, summary: a.summary, body: a.body ?? '', category_id: a.category_id ?? '', status: a.status, visibility: a.visibility, position: a.position };
			}
			saved = JSON.stringify(form);
		} catch (e) {
			error = msg(e);
		} finally {
			loaded = true;
		}
	}
	$effect(() => {
		load(id);
	});
	const dirty = $derived(loaded && JSON.stringify(form) !== saved);
	$effect(() => registerDirty({ label: 'the article', isDirty: () => dirty }));

	async function save(e?: SubmitEvent) {
		e?.preventDefault();
		busy = true;
		error = '';
		try {
			const a = await api<KBArticle>(isNew ? 'POST' : 'PATCH', isNew ? '/kb/manage/articles' : `/kb/manage/articles/${id}`, form);
			form.slug = a.slug;
			saved = JSON.stringify(form);
			current = a;
			toast(a.status === 'published' ? 'Article saved and published' : 'Draft saved');
			if (isNew) goto(`/admin/kb/${a.id}`, { replaceState: true });
		} catch (err) {
			error = msg(err);
		} finally {
			busy = false;
		}
	}
	async function remove() {
		if (!(await confirmDialog({ title: `Delete “${form.title}”?`, body: 'The article is removed for everyone. This cannot be undone.', confirmLabel: 'Delete article', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/kb/manage/articles/${id}`);
			saved = JSON.stringify(form);
			goto('/admin/kb');
		} catch (e) {
			error = msg(e);
		}
	}
</script>

<svelte:head><title>{isNew ? 'New article' : form.title || 'Article'} · RivetPanel</title></svelte:head>
<nav class="text-small text-muted"><a class="link" href="/admin/kb">Knowledgebase</a> / {isNew ? 'New article' : 'Edit article'}</nav>
<h2 class="mt-1 text-section">{isNew ? 'New article' : form.title}</h2>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if !loaded}
	<div class="mt-4"><Skeleton rows={5} label="Loading the article" /></div>
{:else if index}
	<form class="mt-5 grid gap-4" onsubmit={save}>
		<label class="block"><span class="label">Title</span><input class="field" required maxlength="200" bind:value={form.title} /></label>
		<div class="grid gap-4 sm:grid-cols-2">
			<label class="block"><span class="label">Address</span>
				<span class="flex items-center gap-1"><span class="text-small text-muted">/help/</span><input class="field font-mono" maxlength="80" pattern="[a-z0-9]+(-[a-z0-9]+)*" placeholder="from the title" bind:value={form.slug} /></span>
			</label>
			<label class="block"><span class="label">Category</span>
				<select class="field" bind:value={form.category_id}><option value="">Uncategorized</option>{#each index.categories as c (c.id)}<option value={c.id}>{c.name}</option>{/each}</select>
			</label>
		</div>
		<label class="block"><span class="label">Summary (optional)</span><input class="field" maxlength="500" bind:value={form.summary} placeholder="One sentence shown in lists and search results" /></label>
		<div class="grid gap-4 sm:grid-cols-3">
			<label class="block"><span class="label">State</span>
				<select class="field" bind:value={form.status}><option value="draft">Draft (managers only)</option><option value="published">Published</option></select>
			</label>
			<label class="block"><span class="label">Visible to</span>
				<select class="field" bind:value={form.visibility}>
					<option value="public">Everyone (public when the help center is public)</option>
					<option value="users">Signed-in accounts</option>
					<option value="staff">Support staff only</option>
				</select>
			</label>
			<label class="block"><span class="label">Order</span><input class="field" type="number" min="-100000" max="100000" bind:value={form.position} /></label>
		</div>
		<div>
			<div class="flex items-center gap-2">
				<span class="label mb-0">Body (Markdown)</span>
				<div class="ml-auto flex gap-1 rounded-control border border-rule-soft p-0.5 text-small" role="tablist" aria-label="Editor view">
					<button type="button" role="tab" aria-selected={!preview} class="rounded-control px-3 py-0.5 {preview ? 'text-muted' : 'bg-raised font-medium'}" onclick={() => (preview = false)}>Write</button>
					<button type="button" role="tab" aria-selected={preview} class="rounded-control px-3 py-0.5 {preview ? 'bg-raised font-medium' : 'text-muted'}" onclick={() => (preview = true)}>Preview</button>
				</div>
			</div>
			{#if preview}
				<div class="card mt-2 min-h-64 p-5"><SafeMarkdown source={form.body} relativeLinks /></div>
			{:else}
				<textarea class="field mt-2 min-h-96 font-mono text-small" required maxlength="100000" bind:value={form.body} placeholder="# Heading&#10;&#10;Write the answer. **Bold**, *italic*, `code`, lists, tables, > quotes, ``` code blocks and [links](https://example.com) or [panel pages](/support)."></textarea>
			{/if}
			<p class="mt-1 text-small text-muted">Supported: headings, emphasis, code, lists, quotes, tables and links to https:// pages or panel paths. HTML is shown as text, never run; other link types are refused when saving.</p>
		</div>
		<div class="flex flex-wrap gap-2">
			<button class="btn btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
			{#if current}<a class="btn btn-quiet" href="/help/{current.slug}"><Icon name="eye" />View</a>{/if}
			{#if !isNew}<button type="button" class="btn btn-quiet ml-auto text-fail" onclick={remove}><Icon name="trash" />Delete</button>{/if}
		</div>
	</form>
{/if}
