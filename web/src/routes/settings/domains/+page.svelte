<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { UserDomain } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { can } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	let domains = $state<UserDomain[]>([]);
	let max = $state(50);
	let loaded = $state(false);
	let error = $state('');
	let name = $state('');
	let adding = $state(false);
	let checking = $state<Record<string, boolean>>({});
	const canAdd = can('sites.create');
	const msg = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
	const copy = (v: string) => navigator.clipboard?.writeText(v).then(() => toast('Copied'));

	async function load() {
		try {
			const r = await api<{ domains: UserDomain[]; max: number }>('GET', '/me/domains');
			domains = r.domains;
			max = r.max;
			error = '';
		} catch (e) {
			error = msg(e, 'Your domains could not be loaded.');
		} finally {
			loaded = true;
		}
	}
	onMount(load);

	async function add(e: SubmitEvent) {
		e.preventDefault();
		adding = true;
		try {
			const d = await api<UserDomain>('POST', '/me/domains', { domain: name });
			name = '';
			toast(d.verified ? `${d.domain} is verified; choose it when you create a site` : `Added ${d.domain}; add its TXT record to verify it`);
			await load();
		} catch (err) {
			toast(msg(err, 'The domain could not be added.'), 'fail');
		} finally {
			adding = false;
		}
	}

	async function verify(d: UserDomain) {
		checking[d.id] = true;
		try {
			const r = await api<UserDomain>('POST', `/me/domains/${d.id}/verify`);
			toast(r.verified ? `${r.domain} is verified` : (r.last_error ?? 'Not verified yet'), r.verified ? 'success' : 'fail');
			await load();
		} catch (e) {
			toast(msg(e, 'The check could not run.'), 'fail');
		} finally {
			checking[d.id] = false;
		}
	}

	async function remove(d: UserDomain) {
		const ok = await confirmDialog({
			title: `Remove ${d.domain}?`,
			body: d.uses
				? `New sites can no longer use it. The ${d.uses === 1 ? 'site address' : `${d.uses} site addresses`} it verified keep working, but each then needs its own DNS record at the next six-hourly check.`
				: 'New sites can no longer use it.',
			confirmLabel: 'Remove domain',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/me/domains/${d.id}`);
			await load();
		} catch (e) {
			toast(msg(e, 'The domain could not be removed.'), 'fail');
		}
	}
</script>

<svelte:head><title>Domains · RivetPanel</title></svelte:head>

<SettingsSection title="Your domains" description="Prove a domain once and use it for every site you make: pick it when creating a site to serve it at an address such as maps.example.com. One TXT record covers the domain and all its subdomains.">
	{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
	<div class="card overflow-hidden">
		<ul class="divide-y divide-rule-soft">
			{#each domains as d (d.id)}
				<li class="px-4 py-3.5">
					<div class="flex flex-wrap items-center gap-x-3 gap-y-1">
						<span class="min-w-0 truncate font-mono font-medium">{d.domain}</span>
						<span class="pill" data-tone={d.verified ? 'run' : 'warn'}>{d.verified ? 'Verified' : 'Waiting for DNS'}</span>
						<button class="btn btn-sm btn-quiet btn-icon ml-auto" onclick={() => remove(d)} aria-label="Remove {d.domain}" title="Remove"><Icon name="x" size={14} /></button>
					</div>
					{#if d.verified}
						<p class="mt-1 text-small text-muted">
							Verified {fmtAgo(d.verified_at_ms ?? 0)}{d.uses ? ` · ${d.uses === 1 ? '1 site address' : `${d.uses} site addresses`}` : ' · not used by a site yet'}.
							{#if d.last_error}<span class="text-warn">Last re-check: {d.last_error}</span>{/if}
						</p>
					{:else}
						<p class="mt-2 text-small text-muted">Add this record at your DNS provider, then check. Some providers want only <code>_rivetpanel-verify</code> as the name.</p>
						<div class="mt-2 rounded-control bg-paper px-2.5 py-2 text-small">
							<p class="flex items-start gap-2"><span class="pill shrink-0 !px-1.5 !py-0">TXT</span><span class="text-muted">Proves you control {d.domain} and every subdomain of it</span></p>
							<dl class="mt-1.5 grid grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1">
								<dt class="text-muted">Name</dt><dd class="contents"><code class="break-all">{d.txt_name}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(d.txt_name)} aria-label="Copy TXT name"><Icon name="copy" size={13} /></button></dd>
								<dt class="text-muted">Value</dt><dd class="contents"><code class="break-all">{d.txt_value}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(d.txt_value)} aria-label="Copy TXT value"><Icon name="copy" size={13} /></button></dd>
							</dl>
						</div>
						{#if d.last_error}<p class="mt-2 text-small text-warn">{d.last_error}</p>{/if}
						<button class="btn btn-sm mt-3" onclick={() => verify(d)} disabled={checking[d.id]}>{checking[d.id] ? 'Checking DNS…' : 'Check DNS now'}</button>
					{/if}
				</li>
			{:else}
				{#if loaded}<li class="px-4 py-4 text-small text-muted">No domains yet. Add one you own, such as <code>example.com</code>.</li>{/if}
			{/each}
		</ul>
		{#if canAdd}
			<form class="flex gap-2 border-t border-rule-soft bg-paper/40 p-4" onsubmit={add}>
				<label class="min-w-0 flex-1"><span class="sr-only">Domain</span><input class="field font-mono" required bind:value={name} placeholder="example.com" disabled={domains.length >= max} /></label>
				<button class="btn" disabled={adding || domains.length >= max}><Icon name="plus" size={14} />{adding ? 'Adding…' : 'Add'}</button>
			</form>
		{/if}
	</div>
	<p class="mt-3 text-small text-muted">Each site address still needs a CNAME (or A) record pointing at this server; a wildcard such as <code>*.example.com</code> covers them all. Only you can use your domains; teammates add their own.</p>
</SettingsSection>
