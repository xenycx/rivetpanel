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
	let busy = $state<Record<string, string>>({});
	const canAdd = can('sites.create');
	const msg = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
	const copy = (v: string) => navigator.clipboard?.writeText(v).then(() => toast('Copied'));
	const host = (u: string) => u.replace(/^https?:\/\//, '').replace(/\/$/, '');
	// Splits a check message so quoted DNS values render as code.
	const parts = (m: string) => m.split(/“([^”]*)”/).map((text, i) => ({ text, code: i % 2 === 1 }));

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

	async function act(d: UserDomain, kind: string, run: () => Promise<void>) {
		busy[d.id] = kind;
		try {
			await run();
			await load();
		} catch (e) {
			toast(msg(e, 'That did not work. Try again.'), 'fail');
		} finally {
			delete busy[d.id];
		}
	}

	const check = (d: UserDomain) =>
		act(d, 'check', async () => {
			const r = await api<UserDomain>('POST', `/me/domains/${d.id}/verify`);
			if (r.last_error) toast(`${r.domain}: the record ${r.verified ? 'was not found on the re-check' : 'does not match yet'}. The details are under the domain.`, 'fail');
			else toast(`${r.domain} is verified`);
		});

	const setDefault = (d: UserDomain, on: boolean) =>
		act(d, 'default', async () => {
			await api('PATCH', `/me/domains/${d.id}`, { default: on });
			toast(on ? `New sites start on ${d.domain}` : `${d.domain} is no longer the default`);
		});

	async function remove(d: UserDomain) {
		const ok = await confirmDialog({
			title: `Remove ${d.domain}?`,
			body: d.uses
				? `New sites can no longer use it. The ${d.uses === 1 ? 'site address' : `${d.uses} site addresses`} it verified keep answering, but each needs its own DNS record by the next six-hourly check.`
				: 'New sites can no longer use it. You can add it again at any time.',
			confirmLabel: 'Remove domain',
			tone: 'danger'
		});
		if (ok) await act(d, 'remove', () => api('DELETE', `/me/domains/${d.id}`));
	}
</script>

<svelte:head><title>Domains · RivetPanel</title></svelte:head>

{#snippet message(m: string)}{#each parts(m) as p, i (i)}{#if p.code}<code class="break-all text-[0.9em] text-ink">{p.text}</code>{:else}{p.text}{/if}{/each}{/snippet}

{#snippet record(d: UserDomain)}
	<dl class="grid grid-cols-[3.5rem_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1 rounded-control bg-paper px-2.5 py-2 text-small">
		<dt class="text-muted">Type</dt><dd class="col-span-2"><span class="pill !px-1.5 !py-0">TXT</span></dd>
		<dt class="text-muted">Name</dt><dd class="contents"><code class="break-all">{d.txt_name}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(d.txt_name)} aria-label="Copy TXT name"><Icon name="copy" size={13} /></button></dd>
		<dt class="text-muted">Value</dt><dd class="contents"><code class="break-all">{d.txt_value}</code><button class="btn btn-sm btn-quiet btn-icon" onclick={() => copy(d.txt_value)} aria-label="Copy TXT value"><Icon name="copy" size={13} /></button></dd>
	</dl>
{/snippet}

<SettingsSection title="Your domains" description="Prove a domain once and use it for every site you make. One TXT record covers the domain and all of its subdomains; pick the domain when creating a site, or add any address under it to a site, and it works at once.">
	{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
	<div class="card overflow-hidden">
		<ul class="divide-y divide-rule-soft">
			{#each domains as d (d.id)}
				<li class="px-4 py-4">
					<div class="flex flex-wrap items-center gap-x-2.5 gap-y-2">
						<span class="min-w-0 truncate font-mono font-medium">{d.domain}</span>
						<span class="pill" data-tone={!d.verified ? 'warn' : d.last_error ? 'warn' : 'run'}>{!d.verified ? 'Waiting for DNS' : d.last_error ? 'Re-check failed' : 'Verified'}</span>
						{#if d.default}<span class="pill">Default</span>{/if}
						<div class="ml-auto flex flex-wrap items-center gap-1.5">
							{#if d.verified}
								<button class="btn btn-sm btn-quiet" disabled={!!busy[d.id]} onclick={() => setDefault(d, !d.default)} title={d.default ? 'Stop preselecting it for new sites' : 'Preselect it when you create a site'}>{d.default ? 'Unset default' : 'Make default'}</button>
							{/if}
							<button class="btn btn-sm {d.verified ? 'btn-quiet' : ''}" disabled={!!busy[d.id]} onclick={() => check(d)}>{busy[d.id] === 'check' ? 'Checking…' : d.verified ? 'Check again' : 'Check DNS now'}</button>
							<button class="btn btn-sm btn-quiet btn-icon" disabled={!!busy[d.id]} onclick={() => remove(d)} aria-label="Remove {d.domain}" title="Remove"><Icon name="trash" size={14} /></button>
						</div>
					</div>

					{#if !d.verified}
						<p class="mt-2 text-small text-muted">Add this record at your DNS provider, then check. Some providers want only <code>_rivetpanel-verify</code> as the name.</p>
						<div class="mt-2">{@render record(d)}</div>
						{#if d.last_error}
							<Notice tone="warn" class="mt-3" title="The record does not match yet">{@render message(d.last_error)}</Notice>
						{/if}
					{:else}
						<p class="mt-1 text-small text-muted">Verified {fmtAgo(d.verified_at_ms ?? 0)}{#if d.last_checked_at_ms}, last checked {fmtAgo(d.last_checked_at_ms)}{/if}. Keep the record in place: it is checked again every six hours.</p>
						{#if d.last_error}
							<Notice tone="warn" class="mt-3" title="The last check did not find the record">{@render message(d.last_error)} Sites on it keep answering meanwhile.</Notice>
						{/if}
						{#if d.addresses.length}
							<ul class="mt-3 grid gap-1.5">
								{#each d.addresses as a (a.domain)}
									<li class="flex min-w-0 flex-wrap items-center gap-x-2 text-small">
										<a class="link min-w-0 truncate font-mono" href={a.url} target="_blank" rel="noopener" title={host(a.url)}>{a.domain}</a>
										<Icon name="external" size={12} class="shrink-0 text-muted" />
										<a class="ml-auto truncate text-muted hover:text-ink" href="/sites/{a.site_id}">{a.site_name}</a>
									</li>
								{/each}
							</ul>
						{:else}
							<p class="mt-2 text-small text-muted">No site uses it yet. Pick <code>.{d.domain}</code> next to the address when you create a site, or give an app a web address from its Overview.</p>
						{/if}
						<details class="group mt-3 text-small">
							<summary class="flex cursor-pointer list-none items-center gap-1.5 text-muted hover:text-ink [&::-webkit-details-marker]:hidden"><Icon name="chevronRight" size={13} class="transition-transform group-open:rotate-90" />TXT record</summary>
							<div class="mt-2">{@render record(d)}</div>
						</details>
					{/if}
				</li>
			{:else}
				{#if loaded}<li class="px-4 py-5 text-small text-muted">No domains yet. Add one you own, such as <code>example.com</code>.</li>{/if}
			{/each}
		</ul>
		{#if canAdd}
			<form class="flex gap-2 border-t border-rule-soft bg-paper/40 p-4" onsubmit={add}>
				<label class="min-w-0 flex-1"><span class="sr-only">Domain</span><input class="field font-mono" required bind:value={name} placeholder="example.com" disabled={domains.length >= max} /></label>
				<button class="btn" disabled={adding || domains.length >= max}><Icon name="plus" size={14} />{adding ? 'Adding…' : 'Add'}</button>
			</form>
		{/if}
	</div>
	<div class="mt-3 grid gap-1.5 text-small text-muted">
		<p>Each address still needs a CNAME (or A) record pointing at this server; one wildcard such as <code>*.example.com</code> covers them all.</p>
		<p>Checks ask your domain's own nameservers, so a record you just saved is seen at once. If a check names a different value than the one shown here, that is what your DNS provider has: copy the value again.</p>
		<p>Only you can use your domains; teammates add the same domain under their own account.</p>
	</div>
</SettingsSection>
