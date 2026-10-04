<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { KBCategory, KBIndex } from '$lib/api/kb';
	import { visibilityLabel } from '$lib/api/kb';
	import { fmtWhen } from '$lib/args';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';

	let data = $state<KBIndex | null>(null);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load() {
		try {
			data = await api<KBIndex>('GET', '/kb/manage');
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	let publicOn = $state(false);
	$effect(() => {
		if (data) publicOn = data.public;
	});
	async function togglePublic() {
		try {
			await api('PUT', '/kb/manage/settings', { public: publicOn });
			toast(publicOn ? 'The help center is public' : 'The help center is for signed-in accounts only');
		} catch (e) {
			publicOn = !publicOn;
			toast(msg(e), 'fail');
		}
	}

	async function newCategory() {
		const name = await promptDialog({ title: 'New category', label: 'Name', confirmLabel: 'Create', validate: (v) => (v.trim() ? null : 'Enter a name') });
		if (!name) return;
		try {
			await api('POST', '/kb/manage/categories', { name, position: data?.categories.length ?? 0 });
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function renameCategory(c: KBCategory) {
		const name = await promptDialog({ title: 'Rename category', label: 'Name', value: c.name, confirmLabel: 'Save' });
		if (!name) return;
		const description = await promptDialog({ title: 'Category description', label: 'Description (optional)', value: c.description, confirmLabel: 'Save' });
		try {
			await api('PATCH', `/kb/manage/categories/${c.id}`, { name, description: description ?? c.description });
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function moveCategory(c: KBCategory, by: number) {
		const list = [...(data?.categories ?? [])];
		const i = list.findIndex((x) => x.id === c.id);
		const j = i + by;
		if (j < 0 || j >= list.length) return;
		[list[i], list[j]] = [list[j], list[i]];
		try {
			await Promise.all(list.map((x, k) => (x.position === k ? null : api('PATCH', `/kb/manage/categories/${x.id}`, { position: k }))));
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function deleteCategory(c: KBCategory) {
		const n = data?.articles.filter((a) => a.category_id === c.id).length ?? 0;
		if (!(await confirmDialog({ title: `Delete “${c.name}”?`, body: n ? `Its ${n} article${n === 1 ? '' : 's'} stay and become uncategorized.` : 'The category is empty.', confirmLabel: 'Delete category', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/kb/manage/categories/${c.id}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const catName = (id: string | null) => data?.categories.find((c) => c.id === id)?.name ?? 'Uncategorized';
</script>

<svelte:head><title>Knowledgebase · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Knowledgebase</h2>
		<p class="mt-1 max-w-3xl text-muted">Help articles in Markdown. Drafts are visible to knowledgebase managers only; published articles follow their visibility. <a class="link" href="/help">Open the help center</a></p>
	</div>
	<a class="btn btn-primary" href="/admin/kb/new"><Icon name="plus" />New article</a>
</div>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if !data && !error}
	<div class="mt-4"><Skeleton rows={4} label="Loading articles" /></div>
{:else if data}
	<div class="card mt-5 p-5">
		<Switch label="Public help center" bind:checked={publicOn} onchange={togglePublic}>
			When on, visitors without an account can read articles whose visibility is Public at <a class="link" href="/help">/help</a>. Signed-in-only and staff-only articles and drafts are never shown to them.
		</Switch>
	</div>

	<section class="mt-6">
		<div class="flex items-center gap-2"><h3 class="text-title font-semibold">Categories</h3><button class="btn btn-quiet ml-auto" onclick={newCategory}><Icon name="plus" />Category</button></div>
		{#if !data.categories.length}
			<p class="mt-2 text-small text-muted">No categories yet. Articles without a category are listed under “Articles”.</p>
		{:else}
			<ul class="card mt-2 divide-y divide-rule-soft">
				{#each data.categories as c, i (c.id)}
					<li class="flex flex-wrap items-center gap-2 px-4 py-2.5">
						<span class="min-w-0 flex-1"><span class="font-medium">{c.name}</span> <span class="text-small text-muted">/{c.slug} · {data.articles.filter((a) => a.category_id === c.id).length} articles</span>{#if c.description}<span class="block text-small text-muted">{c.description}</span>{/if}</span>
						<button class="btn btn-quiet btn-icon" aria-label="Move {c.name} up" disabled={i === 0} onclick={() => moveCategory(c, -1)}>↑</button>
						<button class="btn btn-quiet btn-icon" aria-label="Move {c.name} down" disabled={i === data.categories.length - 1} onclick={() => moveCategory(c, 1)}>↓</button>
						<button class="btn btn-quiet" onclick={() => renameCategory(c)}>Edit</button>
						<button class="btn btn-quiet text-fail" onclick={() => deleteCategory(c)}>Delete</button>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	<section class="mt-6">
		<h3 class="text-title font-semibold">Articles</h3>
		{#if !data.articles.length}
			<div class="mt-2"><EmptyState title="No articles yet" compact>Write the first article with <strong>New article</strong>.</EmptyState></div>
		{:else}
			<div class="card mt-2 overflow-x-auto">
				<table class="w-full text-left text-small">
					<thead><tr class="border-b border-rule text-muted"><th class="px-4 py-2 font-medium">Title</th><th class="px-4 py-2 font-medium">Category</th><th class="px-4 py-2 font-medium">State</th><th class="px-4 py-2 font-medium">Visible to</th><th class="px-4 py-2 font-medium">Updated</th></tr></thead>
					<tbody>
						{#each data.articles as a (a.id)}
							<tr class="border-b border-rule-soft last:border-0">
								<td class="px-4 py-2.5"><a class="link font-medium" href="/admin/kb/{a.id}">{a.title}</a><span class="block text-muted">/help/{a.slug}</span></td>
								<td class="px-4 py-2.5">{catName(a.category_id)}</td>
								<td class="px-4 py-2.5">{#if a.status === 'draft'}<span class="rounded-pill border border-warn/40 px-2 py-0.5 text-[12px] text-warn">Draft</span>{:else}<span class="rounded-pill border border-run/40 px-2 py-0.5 text-[12px] text-run">Published</span>{/if}</td>
								<td class="px-4 py-2.5">{visibilityLabel[a.visibility]}</td>
								<td class="px-4 py-2.5 text-muted">{fmtWhen(a.updated_at_ms)}{#if a.updated_by}<span class="block">{a.updated_by}</span>{/if}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
{/if}
