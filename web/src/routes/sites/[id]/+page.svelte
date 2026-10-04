<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes, upload } from '$lib/api/client';
	import { roleRank, type Connection, type GitHubRepo, type Site, type SiteBaseChoice, type SiteDomain, type SiteRelease, type SitesInfo, type WorkspaceRole } from '$lib/api/types';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { creatable, loadWorkspaces } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import LogoEditor from '$lib/components/LogoEditor.svelte';
	import { publishTarget } from '$lib/ai/context.svelte';

	type Detail = { site: Site; role: WorkspaceRole; domains: SiteDomain[]; releases: SiteRelease[]; deploy: { running: boolean; last_error: string; finished_at_ms: number } };
	const id = $derived(page.params.id ?? '');
	let d = $state<Detail | null>(null);
	let logoBusy = $state(false);
	async function siteLogo(method: 'PUT' | 'DELETE', body?: unknown) {
		if (!d) return;
		logoBusy = true;
		try {
			const s = await api<Site>(method, `/sites/${d.site.id}/logo`, body);
			d.site = { ...d.site, icon_url: s.icon_url, custom_logo: s.custom_logo };
			toast(method === 'PUT' ? 'Logo saved' : 'Custom logo removed', 'success');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The logo could not be changed.', 'fail');
		} finally {
			logoBusy = false;
		}
	}
	// Tell the assistant which site is in view.
	const siteName = $derived(d?.site.name ?? '');
	$effect(() => {
		if (!siteName) return;
		return publishTarget({ kind: 'site', id, label: siteName });
	});
	let error = $state('');
	let uploading = $state(false);
	let dragging = $state(false);
	let domain = $state('');
	let checking = $state<Record<string, boolean>>({});
	let conn = $state<Connection | null>(null);
	let repos = $state<GitHubRepo[]>([]);
	let repoForm = $state({ full_name: '', branch: '', root_dir: '' });
	let repoOpen = $state(false);
	let settings = $state({ name: '', slug: '', domain_id: '', spa: false, clean_urls: true, workspace_id: '' });
	let bases = $state<SiteBaseChoice[]>([]);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			d = await api<Detail>('GET', `/sites/${id}`);
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	function syncForms() {
		if (!d) return;
		settings = { name: d.site.name, slug: d.site.slug, domain_id: d.site.domain_id ?? '', spa: d.site.spa, clean_urls: d.site.clean_urls, workspace_id: d.site.workspace_id };
		repoForm = { full_name: d.site.repo_full_name ?? '', branch: d.site.repo_branch ?? '', root_dir: d.site.repo_root };
	}
	onMount(() => {
		load().then(syncForms);
		loadWorkspaces();
		api<SitesInfo>('GET', '/sites-info')
			.then((r) => (bases = r.domains ?? []))
			.catch(() => {});
		if (session.features.deploy) {
			api<{ connections: Connection[] }>('GET', '/me/connections')
				.then(async (r) => {
					conn = r.connections.find((c) => c.provider === 'github') ?? null;
					if (conn?.linked) repos = (await api<{ repos: GitHubRepo[] }>('GET', '/me/github/repos')).repos;
				})
				.catch(() => {});
		}
		const t = setInterval(() => {
			if (document.visibilityState === 'visible' && d?.deploy.running) load();
		}, 2000);
		return () => clearInterval(t);
	});

	const canEdit = $derived(roleRank[d?.role ?? ''] >= roleRank.developer);
	const canAdmin = $derived(roleRank[d?.role ?? ''] >= roleRank.admin);
	const live = $derived(!!d?.site.current_release && !d.site.disabled);
	// Domains the site can move to, plus its current one even when that no
	// longer takes new sites.
	const addressChoices = $derived.by(() => {
		const list = bases.map((b) => ({ id: b.id, domain: b.domain }));
		if (d?.site.domain_id && !list.some((b) => b.id === d!.site.domain_id)) list.unshift({ id: d.site.domain_id, domain: d.site.base_domain });
		return list;
	});
	const addressChanged = $derived(!!d && (settings.slug.trim() !== d.site.slug || (!!settings.domain_id && settings.domain_id !== (d.site.domain_id ?? ''))));
	const newAddress = $derived.by(() => {
		if (!d) return '';
		const base = addressChoices.find((b) => b.id === settings.domain_id)?.domain ?? d.site.base_domain;
		return d.site.url.replace(/^(https?:\/\/)[^:/]+/, `$1${settings.slug.trim() || d.site.slug}.${base}`);
	});

	async function send(file: File) {
		if (!file.name.toLowerCase().endsWith('.zip')) return toast('Choose a .zip archive of the site’s files.', 'fail');
		uploading = true;
		try {
			const r = await upload(`/sites/${id}/upload`, file, 'application/zip');
			const out = (await r.json()) as { files: number; bytes: number };
			toast(`Published ${out.files} files (${fmtBytes(out.bytes)})`, 'success');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			uploading = false;
		}
	}
	function pick(e: Event) {
		const f = (e.currentTarget as HTMLInputElement).files?.[0];
		if (f) send(f);
		(e.currentTarget as HTMLInputElement).value = '';
	}
	function drop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		const f = e.dataTransfer?.files?.[0];
		if (f && canEdit) send(f);
	}

	async function linkRepo(e: SubmitEvent) {
		e.preventDefault();
		try {
			d = await api<Detail>('PATCH', `/sites/${id}`, { repo: repoForm });
			repoOpen = false;
			toast(`Linked ${d.site.repo_full_name}`, 'success');
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function unlinkRepo() {
		try {
			d = await api<Detail>('PATCH', `/sites/${id}`, { repo: { clear: true } });
			syncForms();
			toast('Repository unlinked');
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function deploy() {
		try {
			await api('POST', `/sites/${id}/deploy`);
			toast('Deploying the latest commit');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function activate(r: SiteRelease) {
		const ok = await confirmDialog({ title: 'Serve this release again?', body: `Visitors get the files published ${fmtWhen(r.created_at_ms)} immediately. Newer releases stay available.`, confirmLabel: 'Serve this release' });
		if (!ok) return;
		try {
			await api('POST', `/sites/${id}/releases/${r.id}/activate`);
			toast('Release restored', 'success');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}

	async function addDomain(e: SubmitEvent) {
		e.preventDefault();
		try {
			const x = await api<SiteDomain>('POST', `/sites/${id}/domains`, { domain });
			domain = '';
			toast(`Added ${x.domain}; add the DNS records to verify it`);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function verify(x: SiteDomain) {
		checking[x.domain] = true;
		try {
			const r = await api<SiteDomain>('POST', `/sites/${id}/domains/${encodeURIComponent(x.domain)}/verify`);
			toast(r.verified ? `${r.domain} is verified and live` : (r.last_error ?? 'Not verified yet'), r.verified ? 'success' : 'fail');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			checking[x.domain] = false;
		}
	}
	async function removeDomain(x: SiteDomain) {
		const ok = await confirmDialog({ title: `Remove ${x.domain}?`, body: 'The site stops answering on this domain immediately.', confirmLabel: 'Remove domain', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/sites/${id}/domains/${encodeURIComponent(x.domain)}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}

	async function saveSettings(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, unknown> = { name: settings.name, spa: settings.spa, clean_urls: settings.clean_urls };
		if (canAdmin && settings.workspace_id !== d?.site.workspace_id) body.workspace_id = settings.workspace_id;
		if (addressChanged && d) {
			const ok = await confirmDialog({
				title: 'Change the site address?',
				body: `The site moves to ${newAddress} immediately. ${d.site.url} stops working and another site can take it. Verified custom domains keep working.`,
				confirmLabel: 'Change address'
			});
			if (!ok) return;
			body.slug = settings.slug.trim().toLowerCase();
			if (settings.domain_id) body.domain_id = settings.domain_id;
		}
		try {
			d = await api<Detail>('PATCH', `/sites/${id}`, body);
			syncForms();
			toast('Settings saved', 'success');
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function destroy() {
		const ok = await confirmDialog({
			title: `Delete ${d?.site.name}?`,
			body: 'Every release and custom domain is removed and the address stops working immediately. This cannot be undone.',
			confirmLabel: 'Delete site',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/sites/${id}`);
			toast('Site deleted');
			goto('/sites');
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const copy = (v: string) => navigator.clipboard?.writeText(v).then(() => toast('Copied'));
</script>

<svelte:head><title>{d?.site.name ?? 'Site'} · RivetPanel</title></svelte:head>

<a href="/sites" class="mb-4 inline-flex items-center gap-1 text-small text-muted hover:text-ink"><Icon name="chevronLeft" size={14} />All sites</a>
{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
{#if !d && !error}
	<Skeleton rows={5} />
{:else if d}
	<header class="card card-glow flex flex-wrap items-center gap-4 p-6">
		<span class="grid size-12 shrink-0 place-items-center rounded-tile bg-paper-2 text-action" aria-hidden="true"><Icon name="globe" size={20} /></span>
		<div class="min-w-0 flex-1 basis-72">
			<div class="flex flex-wrap items-center gap-2">
				<h1 class="truncate text-section">{d.site.name}</h1>
				<span class="pill" data-tone={d.site.disabled ? 'fail' : live ? 'run' : undefined}>{d.site.disabled ? 'Suspended' : live ? 'Live' : 'Nothing published'}</span>
			</div>
			<a href={d.site.url} target="_blank" rel="noopener" class="mt-0.5 inline-flex max-w-full items-center gap-1.5 font-mono text-small text-action hover:underline"><span class="truncate">{d.site.url}</span><Icon name="external" size={12} /></a>
			<p class="text-small text-muted">{d.site.workspace_name === 'Personal' ? 'Personal workspace' : d.site.workspace_name} · created by {d.site.owner_email}</p>
		</div>
		<a href={d.site.url} target="_blank" rel="noopener" class="btn"><Icon name="external" size={14} />Visit</a>
	</header>
	{#if d.site.disabled}<Notice tone="fail" class="mt-4">An administrator suspended this site. Visitors see an “unavailable” page until it is restored.</Notice>{/if}

	<div class="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
		<div class="grid min-w-0 content-start gap-6">
			<!-- Publishing -->
			<section class="card p-5" aria-labelledby="pub-h">
				<h2 id="pub-h" class="text-title font-semibold">Publish</h2>
				<p class="mt-1 text-small text-muted">Files are served as they are: build your site first and publish the output (index.html at the top). Each publish is a new release; the previous ones stay for rollback.</p>
				{#if canEdit}
					<label
						class="mt-4 grid cursor-pointer place-items-center rounded-tile border-2 border-dashed px-6 py-8 text-center transition-colors {dragging ? 'border-action bg-action/8' : 'border-rule hover:border-action/60'}"
						ondragover={(e) => { e.preventDefault(); dragging = true; }}
						ondragleave={() => (dragging = false)}
						ondrop={drop}
					>
						<input class="sr-only" type="file" accept=".zip,application/zip" onchange={pick} disabled={uploading} />
						<span class="grid size-11 place-items-center rounded-pill bg-paper-2 text-action"><Icon name="upload" size={18} /></span>
						<span class="mt-3 font-semibold">{uploading ? 'Publishing…' : 'Drop a ZIP here or choose one'}</span>
						<span class="mt-1 text-small text-muted">A single top folder such as <code>dist/</code> is unwrapped. Dotfiles like <code>.env</code> are never served.</span>
					</label>
				{/if}

				<div class="mt-5 border-t border-rule-soft pt-4">
					<div class="flex flex-wrap items-center gap-3">
						<Icon name="github" />
						{#if d.site.repo_full_name}
							<p class="min-w-0 flex-1 basis-60"><span class="font-medium">{d.site.repo_full_name}</span> <span class="text-muted">on <code>{d.site.repo_branch}</code>{d.site.repo_root ? `, folder /${d.site.repo_root}` : ''}</span></p>
							{#if canEdit}
								<button class="btn btn-sm btn-quiet" onclick={() => (repoOpen = !repoOpen)}>Change</button>
								<button class="btn btn-sm btn-primary" onclick={deploy} disabled={d.deploy.running}><Icon name="rocket" size={14} />{d.deploy.running ? 'Deploying…' : 'Deploy latest commit'}</button>
							{/if}
						{:else}
							<p class="min-w-0 flex-1 basis-60 text-small text-muted">{session.features.deploy ? 'Or deploy a GitHub branch that contains built files (for example gh-pages).' : 'Deploying from GitHub needs GitHub sign-in to be configured on this panel.'}</p>
							{#if canEdit && session.features.deploy}<button class="btn btn-sm" onclick={() => (repoOpen = !repoOpen)}>Link a repository</button>{/if}
						{/if}
					</div>
					{#if d.deploy.last_error && !d.deploy.running}<Notice tone="fail" class="mt-3">Last GitHub deployment failed: {d.deploy.last_error}</Notice>{/if}
					{#if repoOpen && canEdit}
						<form class="mt-4 grid gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)] sm:items-end" onsubmit={linkRepo}>
							<label class="block"><span class="label">Repository</span>
								<input class="field font-mono" list="site-repos" required bind:value={repoForm.full_name} placeholder="owner/name" />
								<datalist id="site-repos">{#each repos as r (r.full_name)}<option value={r.full_name}></option>{/each}</datalist>
							</label>
							<label class="block"><span class="label">Branch</span><input class="field font-mono" required bind:value={repoForm.branch} placeholder="main" /></label>
							<label class="block"><span class="label">Folder</span><input class="field font-mono" bind:value={repoForm.root_dir} placeholder="/ or dist" /></label>
							<div class="flex flex-wrap gap-2 sm:col-span-3">
								<button class="btn btn-primary">Save repository</button>
								{#if d.site.repo_full_name}<button type="button" class="btn btn-quiet" onclick={unlinkRepo}>Unlink</button>{/if}
								{#if !conn?.linked}<span class="self-center text-small text-muted">Only public repositories work until you <a class="link" href="/settings/connected-accounts">connect GitHub</a>.</span>{/if}
							</div>
						</form>
					{/if}
				</div>
			</section>

			<!-- Releases -->
			<section class="card overflow-hidden" aria-labelledby="rel-h">
				<div class="flex items-center justify-between gap-2 px-5 pt-5 pb-3">
					<h2 id="rel-h" class="text-title font-semibold">Releases</h2>
					<span class="text-small text-muted">The newest five are kept</span>
				</div>
				{#if d.releases.length}
					<ul class="divide-y divide-rule-soft border-t border-rule-soft">
						{#each d.releases as r (r.id)}
							<li class="flex flex-wrap items-center gap-3 px-5 py-3">
								<span class="grid size-8 shrink-0 place-items-center rounded-control {r.current ? 'bg-run/12 text-run' : 'bg-paper-2 text-muted'}"><Icon name={r.source === 'github' ? 'github' : 'upload'} size={14} /></span>
								<div class="min-w-0 flex-1 basis-56">
									<p class="truncate font-medium">{r.source_label ?? 'ZIP upload'}{#if r.current}<span class="pill ml-2 align-middle" data-tone="run">Serving</span>{/if}</p>
									<p class="text-small text-muted">{r.files} files · {fmtBytes(r.bytes)} · {fmtAgo(r.created_at_ms)}{r.actor ? ` by ${r.actor}` : ''}</p>
								</div>
								{#if !r.current && canEdit}<button class="btn btn-sm" onclick={() => activate(r)}>Serve this</button>{/if}
							</li>
						{/each}
					</ul>
				{:else}
					<p class="border-t border-rule-soft px-5 py-4 text-small text-muted">Nothing published yet. Visitors see a “nothing published” page until the first release.</p>
				{/if}
			</section>
		</div>

		<div class="grid min-w-0 content-start gap-6">
			<!-- Domains -->
			<section class="card p-5" aria-labelledby="dom-h">
				<h2 id="dom-h" class="text-title font-semibold">Custom domains</h2>
				<p class="mt-1 text-small text-muted">Serve the site on your own domain. A TXT record proves you control it; nothing is served on a domain before that.</p>
				<ul class="mt-4 grid gap-3">
					<li class="rounded-tile border border-rule-soft bg-paper/40 px-3 py-2.5">
						<p class="flex items-center gap-2 text-small"><span class="side-dot" data-tone="run"></span><span class="truncate font-mono">{d.site.url.replace(/^https?:\/\//, '')}</span><span class="ml-auto text-muted">default</span></p>
					</li>
					{#each d.domains as x (x.domain)}
						<li class="rounded-tile border px-3 py-3 {x.verified ? 'border-rule-soft' : 'border-warn/40'}">
							<div class="flex items-center gap-2">
								<span class="side-dot" data-tone={x.verified ? (x.last_error ? 'warn' : 'run') : 'warn'}></span>
								<a class="min-w-0 flex-1 truncate font-mono text-small hover:underline" href={x.url} target="_blank" rel="noopener">{x.domain}</a>
								{#if canEdit}
									<button class="btn btn-sm btn-quiet btn-icon" onclick={() => removeDomain(x)} aria-label="Remove {x.domain}" title="Remove"><Icon name="x" size={14} /></button>
								{/if}
							</div>
							{#if !x.verified}
								<p class="mt-2 text-small text-muted">Add these two records at your DNS provider, then check. Some providers want only the part before your domain as the name.</p>
								<dl class="mt-2 grid gap-2 text-small">
									{#each [{ type: 'TXT', why: 'Proves you control the domain', name: x.txt_name, value: x.txt_value }, { type: x.record_type, why: 'Sends visitors to this server', name: x.domain, value: x.record_target }] as rec (rec.type)}
										<div class="rounded-control bg-paper px-2.5 py-2">
											<dt class="flex items-center gap-2"><span class="pill !px-1.5 !py-0">{rec.type}</span><span class="text-muted">{rec.why}</span></dt>
											<dd class="mt-1.5 grid grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1">
												<span class="text-muted">Name</span><code class="break-all">{rec.name}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(rec.name)} aria-label="Copy {rec.type} name"><Icon name="copy" size={13} /></button>
												<span class="text-muted">Value</span>
												{#if rec.value}<code class="break-all">{rec.value}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(rec.value)} aria-label="Copy {rec.type} value"><Icon name="copy" size={13} /></button>{:else}<span class="col-span-2 text-muted">this server’s public IP address</span>{/if}
											</dd>
										</div>
									{/each}
								</dl>
								{#if x.record_type === 'CNAME'}<p class="mt-1.5 text-small text-muted">For a root domain (no <code>www.</code>), use your provider’s ALIAS/ANAME record, or an A record with this server’s address.</p>{/if}
								{#if x.last_error}<p class="mt-2 text-small text-warn">{x.last_error}</p>{/if}
								{#if canEdit}<button class="btn btn-sm mt-3 w-full" onclick={() => verify(x)} disabled={checking[x.domain]}>{checking[x.domain] ? 'Checking DNS…' : 'Check DNS now'}</button>{/if}
							{:else}
								<p class="mt-1 text-small text-muted">Verified {fmtAgo(x.verified_at_ms ?? 0)}.{#if x.last_error} <span class="text-warn">Last re-check: {x.last_error}</span>{/if}</p>
							{/if}
						</li>
					{/each}
				</ul>
				{#if canEdit}
					<form class="mt-4 flex gap-2" onsubmit={addDomain}>
						<label class="min-w-0 flex-1"><span class="sr-only">Domain</span><input class="field font-mono" required bind:value={domain} placeholder="www.example.com" /></label>
						<button class="btn"><Icon name="plus" size={14} />Add</button>
					</form>
				{/if}
			</section>

			<!-- Settings -->
			{#if canEdit}
				<section class="card p-5" aria-labelledby="set-h">
					<h2 id="set-h" class="text-title font-semibold">Settings</h2>
					<div class="mt-4">
						<span class="label">Logo</span>
						<LogoEditor src={d.site.icon_url} custom={d.site.custom_logo} fallbackLabel={d.site.name.slice(0, 2).toUpperCase()} busy={logoBusy}
							onUpload={(image) => siteLogo('PUT', { image })} onRemove={() => siteLogo('DELETE')} />
						<span class="help">Without a custom logo the site's own favicon is used{d.site.bot_id ? ", or else the bot's logo" : ''}.</span>
					</div>
					<form class="mt-4 grid gap-4" onsubmit={saveSettings}>
						<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={settings.name} /></label>
						<label class="block">
							<span class="label">Address</span>
							<div class="flex items-stretch">
								<input class="field min-w-0 rounded-r-none font-mono" required minlength="3" maxlength="40" bind:value={settings.slug} oninput={(e) => (settings.slug = e.currentTarget.value.toLowerCase())} aria-describedby="address-help" />
								{#if addressChoices.length > 1}
									<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={settings.domain_id} aria-label="Sites domain">{#each addressChoices as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}</select>
								{:else}
									<span class="flex items-center truncate rounded-r-control border border-l-0 border-rule px-3 font-mono text-small text-muted">.{d.site.base_domain}</span>
								{/if}
							</div>
							<span id="address-help" class="help">{#if addressChanged}Moves to <code class="text-ink">{newAddress}</code>; the current address stops working.{:else}Lower-case letters, digits and hyphens. Unique on each sites domain.{/if}</span>
						</label>
						<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={settings.spa} /><span>Single-page application<span class="help mt-0">Unknown paths serve <code>index.html</code>.</span></span></label>
						<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={settings.clean_urls} /><span>Clean URLs<span class="help mt-0"><code>/about</code> serves <code>about.html</code>.</span></span></label>
						{#if canAdmin && creatable().length > 1}
							<label class="block"><span class="label">Workspace</span>
								<select class="field" bind:value={settings.workspace_id}>{#each creatable() as w (w.id)}<option value={w.id}>{w.personal ? 'Personal' : w.name}</option>{/each}</select>
							</label>
						{/if}
						<div><button class="btn btn-primary">Save settings</button></div>
					</form>
					{#if canAdmin}
						<div class="mt-5 flex flex-wrap items-center gap-3 border-t border-rule-soft pt-4">
							<p class="min-w-0 flex-1 text-small text-muted">Deleting removes every release and domain.</p>
							<button class="btn btn-sm btn-danger" onclick={destroy}>Delete site</button>
						</div>
					{/if}
				</section>
			{/if}
		</div>
	</div>
{/if}
