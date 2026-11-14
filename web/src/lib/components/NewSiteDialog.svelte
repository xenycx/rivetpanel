<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import type { Capacity, Site, SitesInfo, SiteTemplate } from '$lib/api/types';
	import { capLimits, countBlock, room } from '$lib/room';
	import { creatable, workspaces, wsName } from '$lib/workspaces.svelte';
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
	const mine = $derived(info.my_domains ?? []);
	// The address select holds a sites domain id, or "mine:<id>" for one of
	// the user's own domains (the site then also gets its default address).
	let address = $state('');
	const own = $derived(address.startsWith('mine:') ? mine.find((m) => `mine:${m.id}` === address) : undefined);
	const suggest = (n: string) => n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30);
	const label = $derived((slugTouched ? form.slug : suggest(form.name)) || 'your-site');
	const exampleURL = $derived(own?.example_url ?? bases.find((b) => b.id === address)?.example_url ?? info.example_url ?? '');
	const preview = $derived(exampleURL.replace('://example.', `://${label}.`));
	const chosen = $derived(templates.find((t) => t.id === form.template_id));

	// Room under the chosen workspace's sites limit.
	let cap = $state<Capacity | null>(null);
	const rm = $derived(room(cap));
	const noRoom = $derived(rm.sites === 0);
	const sitesLeft = $derived(rm.sites == null ? '' : `Workspace allowance: ${rm.sites} of ${capLimits(cap).sites} ${capLimits(cap).sites === 1 ? 'site' : 'sites'} left`);
	$effect(() => {
		if (!open) return;
		const ws = form.workspace_id;
		let live = true;
		api<Capacity>('GET', `/me/capacity?workspace=${encodeURIComponent(ws)}`)
			.then((c) => {
				if (live) cap = c;
			})
			.catch(() => {
				if (live) cap = null;
			});
		return () => {
			live = false;
		};
	});

	// Reset the form each time the dialog opens.
	let wasOpen = false;
	$effect(() => {
		if (open && !wasOpen) {
			const ws = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
			const t = templates.find((x) => x.id === template);
			form = { name: '', slug: '', domain_id: bases.find((b) => b.primary)?.id ?? '', workspace_id: ws, spa: t?.spa ?? false, template_id: t?.id ?? '' };
			const def = mine.find((m) => m.default);
			address = def ? `mine:${def.id}` : form.domain_id;
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
			const s = await api<Site>('POST', '/sites', {
				...form,
				slug: slugTouched ? form.slug : '',
				domain_id: own ? (bases.find((b) => b.primary)?.id ?? '') : address,
				user_domain_id: own?.id ?? ''
			});
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
				<input class="field min-w-0 font-mono {bases.length > 1 || mine.length ? 'rounded-r-none' : ''}" maxlength="40" value={slugTouched ? form.slug : suggest(form.name)} oninput={(e) => { slugTouched = true; form.slug = e.currentTarget.value.toLowerCase(); }} placeholder="bot-dashboard" aria-describedby="new-site-address" />
				{#if bases.length > 1 || mine.length}
					<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={address} aria-label="Domain">
						{#if mine.length}
							<optgroup label="Sites domains">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}{b.label ? ` (${b.label})` : ''}</option>{/each}</optgroup>
							<optgroup label="Your domains">{#each mine as m (m.id)}<option value="mine:{m.id}">.{m.domain}</option>{/each}</optgroup>
						{:else}
							{#each bases as b (b.id)}<option value={b.id}>.{b.domain}{b.label ? ` (${b.label})` : ''}</option>{/each}
						{/if}
					</select>
				{/if}
			</div>
			<span class="help" id="new-site-address">Served at <code class="text-ink">{preview}</code>{#if own}, verified by your record on {own.domain}; point <code class="text-ink">{label}.{own.domain}</code> at this server (a wildcard record covers it){/if}. Lower-case letters, digits and hyphens. {#if !mine.length}Prove a domain once under <a class="link" href="/settings/domains" onclick={() => (open = false)}>Settings → Domains</a> to offer it here.{/if}</span>
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
				<select class="field" bind:value={form.workspace_id}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{wsName(w)}</option>{/each}</select>
				{#if sitesLeft}<span class="help">{sitesLeft}.</span>{/if}
			</label>
		{:else if sitesLeft}
			<p class="text-small text-muted">{sitesLeft}.</p>
		{/if}
		{#if noRoom}
			<Notice tone="warn" title="No room for another site">{countBlock(cap, ['sites'], { sites: 'sites' })}</Notice>
		{/if}
		{#if info.builds_enabled && !form.template_id}<p class="text-small text-muted">Building with npm, or running Node.js, PHP, Python, Go or Rust? <a class="link" href="/sites/new" onclick={() => (open = false)}>Deploy from a GitHub repository</a> instead.</p>{/if}
		<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={form.spa} /><span>Single-page application<span class="help mt-0">Unknown paths serve <code>index.html</code>, for React, Vue or Svelte apps with client-side routing.</span></span></label>
		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="new-site-form" disabled={busy || noRoom || !form.name.trim()}>{busy ? 'Creating…' : 'Create site'}</button>
	{/snippet}
</Dialog>
