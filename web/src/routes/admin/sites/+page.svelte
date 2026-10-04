<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { Site, SiteBaseDomain, SitesInfo } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let info = $state<SitesInfo | null>(null);
	let sites = $state<Site[] | null>(null);
	let error = $state('');
	let q = $state('');
	let served = $state<'all' | 'live' | 'suspended'>('all');
	let bases = $state<SiteBaseDomain[]>([]);
	let addForm = $state({ domain: '', label: '', dns_target: '' });
	let adding = $state(false);
	let checking = $state<Record<string, boolean>>({});
	let moveTo = $state<Record<string, string>>({});
	let edits = $state<Record<string, { label: string; dns_target: string }>>({});
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			info = await api<SitesInfo>('GET', '/sites-info');
			if (info.enabled) {
				const [s, b] = await Promise.all([
					api<{ sites: Site[] }>('GET', '/admin/sites'),
					api<{ domains: SiteBaseDomain[] }>('GET', '/admin/site-base-domains')
				]);
				sites = s.sites;
				setBases(b.domains);
			}
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Sites could not be loaded.';
		}
	}
	function setBases(list: SiteBaseDomain[]) {
		bases = list;
		edits = Object.fromEntries(list.map((b) => [b.domain, { label: b.label, dns_target: b.dns_target }]));
	}
	async function reloadBases() {
		setBases((await api<{ domains: SiteBaseDomain[] }>('GET', '/admin/site-base-domains')).domains);
	}
	const serving = $derived(bases.filter((b) => b.serving));
	const path = (b: SiteBaseDomain) => `/admin/site-base-domains/${encodeURIComponent(b.domain)}`;

	async function addBase(e: SubmitEvent) {
		e.preventDefault();
		adding = true;
		try {
			const b = await api<SiteBaseDomain>('POST', '/admin/site-base-domains', addForm);
			addForm = { domain: '', label: '', dns_target: '' };
			toast(`Added ${b.domain}; add the DNS records, then check`);
			await reloadBases();
		} catch (err) {
			toast(msg(err), 'fail');
		} finally {
			adding = false;
		}
	}
	async function verifyBase(b: SiteBaseDomain) {
		checking[b.domain] = true;
		try {
			const r = await api<SiteBaseDomain>('POST', `${path(b)}/verify`);
			toast(r.verified ? `${r.domain} is verified` : (r.last_error ?? 'Not verified yet'), r.verified ? 'success' : 'fail');
			await reloadBases();
		} catch (err) {
			toast(msg(err), 'fail');
		} finally {
			checking[b.domain] = false;
		}
	}
	async function patchBase(b: SiteBaseDomain, body: Record<string, unknown>, done: string) {
		try {
			await api('PATCH', path(b), body);
			toast(done, 'success');
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function toggleBase(b: SiteBaseDomain) {
		if (b.enabled) {
			const ok = await confirmDialog({
				title: `Turn off ${b.domain}?`,
				body: `${b.sites} site(s) under it stop answering at their ${b.domain} address immediately (custom domains keep working), and no new site can use it. Nothing is deleted.`,
				confirmLabel: 'Turn off',
				tone: 'danger'
			});
			if (!ok) return;
		}
		await patchBase(b, { enabled: !b.enabled }, b.enabled ? `${b.domain} turned off` : `${b.domain} turned on`);
	}
	async function moveSites(b: SiteBaseDomain) {
		const to = bases.find((x) => x.id === moveTo[b.domain]);
		if (!to) return;
		const ok = await confirmDialog({
			title: `Move ${b.sites} site(s) to ${to.domain}?`,
			body: `Each site keeps its name and moves from <name>.${b.domain} to <name>.${to.domain}; the old addresses stop working. If any name is taken on ${to.domain}, nothing moves.`,
			confirmLabel: 'Move sites'
		});
		if (!ok) return;
		try {
			const r = await api<{ moved: number }>('POST', `${path(b)}/move-sites`, { to: to.id });
			toast(`Moved ${r.moved} site(s) to ${to.domain}`, 'success');
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function removeBase(b: SiteBaseDomain) {
		const ok = await confirmDialog({ title: `Remove ${b.domain}?`, body: 'It stops being offered for sites. You can add it again later.', confirmLabel: 'Remove domain', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', path(b));
			toast(`Removed ${b.domain}`);
			await reloadBases();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	const records = (b: SiteBaseDomain) => [
		...(b.from_config ? [] : [{ type: 'TXT', why: 'Proves you control the domain', name: b.txt_name, value: b.txt_value }]),
		{ type: b.record_type, why: 'Sends every site address to this server', name: `*.${b.domain}`, value: b.record_target },
		{ type: b.record_type, why: 'The domain itself (optional)', name: b.domain, value: b.record_target }
	];
	const copy = (v: string) => navigator.clipboard?.writeText(v).then(() => toast('Copied'));
	onMount(load);
	const shown = $derived(
		(sites ?? []).filter(
			(s) =>
				(served === 'all' || (served === 'suspended') === s.disabled) &&
				(!q.trim() || `${s.name} ${s.slug}.${s.base_domain} ${s.owner_email} ${s.workspace_name}`.toLowerCase().includes(q.trim().toLowerCase()))
		)
	);
	const total = $derived((sites ?? []).reduce((n, s) => n + s.release_bytes, 0));
	const domains = $derived((sites ?? []).reduce((n, s) => n + s.domains, 0));
	const suspended = $derived((sites ?? []).filter((s) => s.disabled).length);

	async function toggle(s: Site) {
		if (!s.disabled) {
			const ok = await confirmDialog({ title: `Suspend ${s.name}?`, body: 'Visitors get an “unavailable” page on every address of the site until you restore it. Its members can still manage it.', confirmLabel: 'Suspend site', tone: 'danger' });
			if (!ok) return;
		}
		try {
			await api('PATCH', `/admin/sites/${s.id}`, { disabled: !s.disabled });
			toast(s.disabled ? `Restored ${s.name}` : `Suspended ${s.name}`);
			await load();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The site could not be changed.', 'fail');
		}
	}
</script>

<svelte:head><title>Sites · Administration · RivetPanel</title></svelte:head>

<h2 class="text-section">Sites and domains</h2>
<p class="mt-1 max-w-3xl text-muted">Every hosted site on this panel. Suspending a site stops it from being served on all its addresses without deleting anything.</p>
{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}

{#if info && !info.enabled}
	<Notice class="mt-4" title="Site hosting is off">
		Set <code>RIVET_SITES_LISTEN</code> (for example <code>127.0.0.1:8081</code>) and <code>RIVET_SITES_BASE_URL</code> (for example <code>https://sites.example.com</code>), route <code>*.sites.example.com</code> and custom domains to that listener in your reverse proxy, then restart. See <a href="/docs#sites">the hosting guide</a>.
	</Notice>
{:else if sites === null && !error}
	<div class="mt-4"><Skeleton rows={3} /></div>
{:else if sites}
	<dl class="mt-5 grid grid-cols-2 gap-3 md:grid-cols-4">
		{#each [['Sites', `${sites.length}`, bases.length > 1 ? `under ${serving.length} domains` : `under ${info?.domain ?? ''}`], ['Served', fmtBytes(total), 'current releases'], ['Custom domains', `${domains}`, 'across all sites'], ['Suspended', `${suspended}`, suspended ? 'not being served' : 'none']] as [k, v, h] (k)}
			<div class="stat"><dt class="eyebrow">{k}</dt><dd class="mt-1 font-mono text-title font-semibold">{v}</dd><dd class="truncate text-small text-muted" title={h}>{h}</dd></div>
		{/each}
	</dl>

	<section class="card mt-5 p-5" aria-labelledby="bases-h">
		<h3 id="bases-h" class="text-title font-semibold">Sites domains</h3>
		<p class="mt-1 max-w-3xl text-small text-muted">A site's address is <code>&lt;name&gt;.&lt;sites domain&gt;</code>, and each name is unique per domain. New sites use the primary domain unless their creator picks another. A domain you add serves nothing until a TXT record proves you control it; the reverse proxy also has to route it and present a certificate for it (see <a class="link" href="/docs#sites">the hosting guide</a>).</p>
		<ul class="mt-4 grid gap-3">
			{#each bases as b (b.id)}
				<li class="rounded-tile border px-4 py-3 {b.serving || !b.enabled ? 'border-rule-soft' : 'border-warn/40'}">
					<div class="flex flex-wrap items-center gap-2">
						<span class="side-dot" data-tone={b.serving ? (b.last_error ? 'warn' : 'run') : b.enabled ? 'warn' : undefined}></span>
						<span class="min-w-0 truncate font-mono font-medium">{b.domain}</span>
						{#if b.label}<span class="text-small text-muted">{b.label}</span>{/if}
						{#if b.primary}<span class="pill" data-tone="run">Primary</span>{/if}
						{#if !b.enabled}<span class="pill">Off</span>{:else if !b.verified}<span class="pill" data-tone="warn">Not verified</span>{/if}
						{#if b.from_config}<span class="pill" title="Listed in the environment file">Environment</span>{/if}
						<span class="ml-auto text-small text-muted">{b.sites} site{b.sites === 1 ? '' : 's'}</span>
					</div>
					{#if b.verified && b.last_error}<p class="mt-1 text-small text-warn">Last re-check: {b.last_error}</p>{/if}
					<details class="mt-2" open={!b.verified}>
						<summary class="cursor-pointer text-small text-muted">DNS records and settings</summary>
						<dl class="mt-2 grid gap-2 text-small 2xl:grid-cols-3">
							{#each records(b) as rec (rec.name + rec.type)}
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
						{#if !b.verified && b.last_error}<p class="mt-2 text-small text-warn">{b.last_error}</p>{/if}
						<form class="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end" onsubmit={(e) => { e.preventDefault(); patchBase(b, edits[b.domain], 'Saved'); }}>
							<label class="block"><span class="label">Label</span><input class="field" maxlength="64" bind:value={edits[b.domain].label} placeholder="Shown next to the domain" /></label>
							<label class="block"><span class="label">DNS target</span><input class="field font-mono" bind:value={edits[b.domain].dns_target} placeholder="host name or IP (optional)" /></label>
							<button class="btn">Save</button>
						</form>
					</details>
					{#if !b.primary || !b.verified || (b.sites > 0 && serving.length > 1)}<div class="mt-3 flex flex-wrap items-center gap-2">
						{#if !b.verified}<button class="btn btn-sm btn-primary" onclick={() => verifyBase(b)} disabled={checking[b.domain]}>{checking[b.domain] ? 'Checking DNS…' : 'Check DNS now'}</button>{/if}
						{#if b.serving && !b.primary}<button class="btn btn-sm" onclick={() => patchBase(b, { primary: true }, `${b.domain} is now the primary domain`)}>Make primary</button>{/if}
						{#if !b.primary}<button class="btn btn-sm {b.enabled ? 'btn-quiet' : ''}" onclick={() => toggleBase(b)}>{b.enabled ? 'Turn off' : 'Turn on'}</button>{/if}
						{#if b.sites > 0 && serving.some((x) => x.id !== b.id)}
							<span class="flex items-center gap-1.5">
								<select class="field w-auto py-1 text-small" bind:value={moveTo[b.domain]} aria-label="Move sites to"><option value="">Move sites to…</option>{#each serving.filter((x) => x.id !== b.id) as x (x.id)}<option value={x.id}>{x.domain}</option>{/each}</select>
								<button class="btn btn-sm" disabled={!moveTo[b.domain]} onclick={() => moveSites(b)}>Move</button>
							</span>
						{/if}
						{#if !b.primary && !b.from_config && b.sites === 0}<button class="btn btn-sm btn-quiet ml-auto" onclick={() => removeBase(b)}><Icon name="x" size={14} />Remove</button>{/if}
					</div>{/if}
				</li>
			{/each}
		</ul>
		<form class="mt-4 grid gap-2 border-t border-rule-soft pt-4 sm:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_auto] sm:items-end" onsubmit={addBase}>
			<label class="block"><span class="label">Add a domain</span><input class="field font-mono" required bind:value={addForm.domain} placeholder="pages.example.net" /></label>
			<label class="block"><span class="label">Label</span><input class="field" maxlength="64" bind:value={addForm.label} placeholder="optional" /></label>
			<button class="btn" disabled={adding}><Icon name="plus" size={14} />Add</button>
		</form>
	</section>

	<div class="mt-5 flex flex-wrap items-center gap-2">
		<label class="relative min-w-0 flex-1 basis-60">
			<span class="sr-only">Search sites</span>
			<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
			<input class="field pl-8" type="search" placeholder="Search by name, address, owner or workspace" bind:value={q} />
		</label>
		<select class="field w-auto" bind:value={served} aria-label="State"><option value="all">All sites</option><option value="live">Being served</option><option value="suspended">Suspended</option></select>
	</div>
	<div class="card mt-3 overflow-x-auto">
		<table class="w-full min-w-[46rem] text-left">
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal">
					<th class="eyebrow">Site</th><th class="eyebrow">Workspace</th><th class="eyebrow">Created by</th><th class="eyebrow text-right">Size</th><th class="eyebrow text-right">Domains</th><th class="eyebrow text-right">Updated</th><th></th>
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each shown as s (s.id)}
					<tr class="[&>td]:px-4 [&>td]:py-2.5 hover:bg-paper/40">
						<td class="max-w-64">
							<a class="block truncate font-medium hover:underline" href="/sites/{s.id}">{s.name}</a>
							<a class="block truncate font-mono text-[12px] text-action hover:underline" href={s.url} target="_blank" rel="noopener">{s.slug}{bases.length > 1 ? `.${s.base_domain}` : ''}</a>
						</td>
						<td class="max-w-40 truncate text-small"><a class="hover:underline" href="/admin/workspaces/{s.workspace_id}">{s.workspace_name}</a></td>
						<td class="max-w-48 truncate text-small"><a class="hover:underline" href="/admin/users/{s.owner_id}">{s.owner_email}</a></td>
						<td class="text-right font-mono text-small">{s.current_release ? fmtBytes(s.release_bytes) : '—'}</td>
						<td class="text-right font-mono text-small">{s.domains}</td>
						<td class="text-right text-small text-muted">{fmtAgo(s.updated_at_ms)}</td>
						<td class="text-right"><button class="btn btn-sm {s.disabled ? '' : 'btn-danger'}" onclick={() => toggle(s)}>{s.disabled ? 'Restore' : 'Suspend'}</button></td>
					</tr>
				{:else}
					<tr><td colspan="7" class="px-4 py-4 text-small text-muted">No sites{q || served !== 'all' ? ' match' : ' yet'}.</td></tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
