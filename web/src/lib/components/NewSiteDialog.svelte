<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { Site, SitesInfo, SiteTemplate } from '$lib/api/types';
	import { creatable, workspaces } from '$lib/workspaces.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	// The new-site form, shared by the Sites page and the Templates page. With
	// templates it offers "Start from"; a template seeds the first release so
	// the site is live as soon as it is created.
	let {
		open = $bindable(false),
		info,
		templates = [],
		template = ''
	}: { open?: boolean; info: SitesInfo; templates?: SiteTemplate[]; template?: string } = $props();

	let form = $state({ name: '', slug: '', domain_id: '', workspace_id: '', spa: false, template_id: '' });
	let slugTouched = $state(false);
	let error = $state('');
	let busy = $state(false);

	const bases = $derived(info.domains ?? []);
	const suggest = (n: string) => n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30);
	const exampleURL = $derived(bases.find((b) => b.id === form.domain_id)?.example_url ?? info.example_url ?? '');
	const preview = $derived(exampleURL.replace('://example.', `://${(slugTouched ? form.slug : suggest(form.name)) || 'your-site'}.`));
	const chosen = $derived(templates.find((t) => t.id === form.template_id));

	// Reset the form each time the dialog opens.
	let wasOpen = false;
	$effect(() => {
		if (open && !wasOpen) {
			const ws = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
			const t = templates.find((x) => x.id === template);
			form = { name: '', slug: '', domain_id: bases.find((b) => b.primary)?.id ?? '', workspace_id: ws, spa: t?.spa ?? false, template_id: t?.id ?? '' };
			slugTouched = false;
			error = '';
		}
		wasOpen = open;
	});

	function pickTemplate(id: string) {
		form.template_id = id;
		form.spa = templates.find((t) => t.id === id)?.spa ?? form.spa;
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const s = await api<Site>('POST', '/sites', { ...form, slug: slugTouched ? form.slug : '' });
			open = false;
			goto(`/sites/${s.id}`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The site could not be created. Check your connection and try again.';
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open title={chosen && template ? `New site from ${chosen.name}` : 'New site'} size="md">
	<form id="new-site-form" class="grid gap-4" onsubmit={create}>
		<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={form.name} placeholder={chosen ? chosen.name : 'Bot dashboard'} /></label>
		<label class="block">
			<span class="label">Address</span>
			<div class="flex items-stretch">
				<input class="field min-w-0 font-mono {bases.length > 1 ? 'rounded-r-none' : ''}" maxlength="40" value={slugTouched ? form.slug : suggest(form.name)} oninput={(e) => { slugTouched = true; form.slug = e.currentTarget.value.toLowerCase(); }} placeholder="bot-dashboard" aria-describedby="new-site-address" />
				{#if bases.length > 1}
					<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={form.domain_id} aria-label="Sites domain">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}{b.label ? ` (${b.label})` : ''}</option>{/each}</select>
				{/if}
			</div>
			<span class="help" id="new-site-address">Served at <code class="text-ink">{preview}</code>. Lower-case letters, digits and hyphens. You can change it, or add your own domain, afterwards.</span>
		</label>
		{#if templates.length}
			<label class="block">
				<span class="label">Start from</span>
				<select class="field" value={form.template_id} onchange={(e) => pickTemplate(e.currentTarget.value)}>
					<option value="">Empty site, upload files later</option>
					{#each templates as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
				</select>
				<span class="help">{chosen ? `${chosen.description} Its files become the first release; edit or replace them any time.` : 'Upload a ZIP of your built files, or connect a GitHub repository, after creating it.'}</span>
			</label>
		{/if}
		{#if creatable().length > 1}
			<label class="block"><span class="label">Workspace</span>
				<select class="field" bind:value={form.workspace_id}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{w.personal ? 'Personal' : w.name}</option>{/each}</select>
			</label>
		{/if}
		<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={form.spa} /><span>Single-page application<span class="help mt-0">Unknown paths serve <code>index.html</code>, for React, Vue or Svelte apps with client-side routing.</span></span></label>
		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="new-site-form" disabled={busy || !form.name.trim()}>{busy ? 'Creating…' : 'Create site'}</button>
	{/snippet}
</Dialog>
