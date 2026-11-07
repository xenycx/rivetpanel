<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { AppTemplate, SitesInfo, Stack } from '$lib/api/types';
	import { creatable, loadWorkspaces, workspaces, wsName } from '$lib/workspaces.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import AppIcon from './AppIcon.svelte';

	// Installs a one-click app: a name, where it is reached, and only the
	// values the app cannot choose itself. Passwords and keys are generated on
	// the server unless the person opens "More options" and sets them.
	let { open = $bindable(false), app, sites = false, publicPorts = false }: { open?: boolean; app: AppTemplate | null; sites?: boolean; publicPorts?: boolean } = $props();

	let name = $state('');
	let values = $state<Record<string, string>>({});
	let address = $state<'site' | 'port'>('port');
	let slug = $state('');
	let slugTouched = $state(false);
	let domainId = $state('');
	let workspaceId = $state('');
	let info = $state<SitesInfo | null>(null);
	let busy = $state(false);
	let error = $state('');

	const asked = $derived((app?.vars ?? []).filter((v) => !v.advanced && !v.generate));
	const more = $derived((app?.vars ?? []).filter((v) => v.advanced || v.generate));
	const canSite = $derived(sites && !!app?.web && (info?.domains?.length ?? 0) > 0);
	const bases = $derived(info?.domains ?? []);
	const suggest = (s: string) =>
		s
			.toLowerCase()
			.replace(/[^a-z0-9-]+/g, '-')
			.replace(/^-+|-+$/g, '')
			.slice(0, 40);
	const slugValue = $derived(slugTouched ? slug : suggest(name));
	const preview = $derived((bases.find((b) => b.id === domainId)?.example_url ?? info?.example_url ?? '').replace('://example.', `://${slugValue || 'your-app'}.`));

	// A fresh form each time the dialog opens for an app.
	let openedFor = '';
	$effect(() => {
		if (!open || !app || openedFor === app.id) return;
		openedFor = app.id;
		name = app.id;
		values = Object.fromEntries(app.vars.map((v) => [v.name, v.default ?? '']));
		slugTouched = false;
		slug = '';
		error = '';
		workspaceId = workspaces.selected !== 'all' && !workspaces.list.find((w) => w.id === workspaces.selected)?.personal ? workspaces.selected : '';
		loadWorkspaces();
		if (sites && app.web) {
			api<SitesInfo>('GET', '/sites-info')
				.then((i) => {
					info = i;
					domainId = i.domains?.find((b) => b.primary)?.id ?? '';
					address = (i.domains?.length ?? 0) > 0 ? 'site' : 'port';
				})
				.catch(() => (address = 'port'));
		} else address = 'port';
	});
	$effect(() => {
		if (!open) openedFor = '';
	});

	async function install(e: Event) {
		e.preventDefault();
		if (!app) return;
		busy = true;
		error = '';
		const set: Record<string, string> = {};
		for (const v of app.vars) {
			const val = (values[v.name] ?? '').trim();
			if (val) set[v.name] = val;
		}
		try {
			const r = await api<{ stack: Stack }>('POST', `/app-templates/${app.id}/install`, {
				name: name.trim(),
				workspace_id: workspaceId,
				values: set,
				address: canSite ? address : 'port',
				slug: address === 'site' ? slugValue : '',
				domain_id: address === 'site' ? domainId : ''
			});
			open = false;
			goto(`/stacks/${r.stack.id}?installed=1`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The app could not be installed.';
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open title={app ? `Install ${app.name}` : 'Install'} size="md">
	{#if app}
		<form id="install-app" class="grid gap-4 pb-2" onsubmit={install}>
			<div class="flex items-start gap-3">
				<AppIcon id={app.id} name={app.name} size={44} />
				<div class="min-w-0">
					<p class="text-muted">{app.description}</p>
					<a class="link mt-1 inline-flex items-center gap-1 text-small" href={app.website} target="_blank" rel="noopener noreferrer">{app.website.replace(/^https?:\/\//, '').replace(/\/$/, '')}<Icon name="external" size={12} /></a>
				</div>
			</div>

			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block"><span class="label">Name</span><input class="field" required maxlength="40" bind:value={name} autocomplete="off" data-autofocus /></label>
				{#if creatable().length > 1}
					<label class="block"><span class="label">Workspace</span>
						<select class="field" bind:value={workspaceId}>{#each creatable() as w (w.id)}<option value={w.personal ? '' : w.id}>{wsName(w)}</option>{/each}</select>
					</label>
				{/if}
			</div>

			{#if app.web}
				<fieldset class="grid gap-2">
					<legend class="label">Where it opens</legend>
					{#if canSite}
						<div class="seg w-fit" role="radiogroup" aria-label="Where it opens">
							<button type="button" role="radio" class="seg-item" aria-checked={address === 'site'} onclick={() => (address = 'site')}><Icon name="globe" size={13} />Web address</button>
							<button type="button" role="radio" class="seg-item" aria-checked={address === 'port'} onclick={() => (address = 'port')}><Icon name="network" size={13} />Port only</button>
						</div>
					{/if}
					{#if canSite && address === 'site'}
						<div class="flex items-stretch">
							<input class="field min-w-0 font-mono {bases.length > 1 ? 'rounded-r-none' : ''}" maxlength="40" aria-label="Address" value={slugValue} oninput={(e) => { slugTouched = true; slug = e.currentTarget.value.toLowerCase(); }} />
							{#if bases.length > 1}<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={domainId} aria-label="Domain">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}</select>{/if}
						</div>
						<span class="help">Opens at <code class="text-ink">{preview}</code>. You can add your own domain to the site later.</span>
					{:else}
						<span class="help">{publicPorts ? 'Opens on a free port of this server.' : 'Opens on a free port of this server, reachable from the server itself (127.0.0.1). Use a web address to open it from anywhere.'}</span>
					{/if}
				</fieldset>
			{:else if app.console}
				<Notice tone="info">{app.name} has no web page: you use it from the app's Terminal tab.</Notice>
			{/if}

			{#each asked as v (v.name)}
				<label class="block">
					<span class="label">{v.label}{#if !v.required}<span class="opt"> optional</span>{/if}</span>
					<input class="field {v.secret ? 'font-mono' : ''}" type={v.secret ? 'password' : 'text'} required={v.required} bind:value={values[v.name]} autocomplete="off" spellcheck="false" />
					{#if v.description}<span class="help">{v.description}</span>{/if}
				</label>
			{/each}

			{#if more.length}
				<details class="group">
					<summary class="flex cursor-pointer list-none items-center gap-1 text-small font-medium text-muted hover:text-ink"><Icon name="chevronRight" size={13} class="transition-transform group-open:rotate-90" />More options</summary>
					<div class="mt-3 grid gap-4">
						{#each more as v (v.name)}
							<label class="block">
								<span class="label">{v.label}</span>
								<input class="field {v.secret ? 'font-mono' : ''}" type={v.secret ? 'password' : 'text'} bind:value={values[v.name]} placeholder={v.generate ? 'Generated automatically' : (v.default ?? '')} autocomplete="new-password" spellcheck="false" />
								{#if v.description}<span class="help">{v.description}</span>{/if}
							</label>
						{/each}
					</div>
				</details>
			{/if}

			{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
		</form>
	{/if}
	{#snippet footer()}
		{#if app}<span class="mr-auto text-small text-muted">{app.services} {app.services === 1 ? 'container' : 'containers'}, up to {fmtBytes(app.memory_bytes)}</span>{/if}
		<button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
		<button type="submit" form="install-app" class="btn btn-primary" disabled={busy || !app?.available}>{busy ? 'Installing…' : 'Install'}</button>
	{/snippet}
</Dialog>
