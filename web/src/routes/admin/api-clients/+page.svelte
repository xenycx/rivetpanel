<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { ApiClient } from '$lib/api/types';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { can } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// Every account's API clients. Revoking needs users.manage, and a delegated
	// account manager cannot revoke clients of accounts it does not outrank
	// (the server refuses; administrators can revoke any).
	let clients = $state<ApiClient[] | null>(null);
	let error = $state('');
	let filter = $state('');
	const manage = $derived(can('users.manage'));
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');
	const shown = $derived((clients ?? []).filter((c) => !filter || `${c.name} ${c.owner_email} ${c.prefix}`.toLowerCase().includes(filter.toLowerCase())));

	async function load() {
		try {
			clients = (await api<{ clients: ApiClient[] }>('GET', '/admin/api-clients')).clients;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	async function revoke(c: ApiClient) {
		const ok = await confirmDialog({
			title: `Revoke “${c.name}” of ${c.owner_email}?`,
			body: 'Programs using it are refused from their next request. The owner can create a new one.',
			confirmLabel: 'Revoke client',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/admin/api-clients/${c.id}`);
			toast('API client revoked');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	function scope(c: ApiClient) {
		if (!c.bot_ids && !c.workspace_ids) return 'Not limited';
		const b = c.bot_ids?.length ?? 0;
		const w = c.workspace_ids?.length ?? 0;
		return `Limited to ${b} bot${b === 1 ? '' : 's'} and ${w} workspace${w === 1 ? '' : 's'}`;
	}
</script>

<svelte:head><title>API clients · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">API clients</h2>
		<p class="mt-1 max-w-3xl text-muted">Application credentials accounts created under Settings → SFTP and API keys. Each carries some of its owner's permissions and is re-checked against the owner's role on every request.</p>
	</div>
	<input class="field max-w-60" type="search" placeholder="Filter by name or owner" bind:value={filter} aria-label="Filter API clients" />
</div>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

<div class="mt-4">
	{#if clients === null && !error}
		<Skeleton rows={3} label="Loading API clients" />
	{:else if clients}
		<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
			{#each shown as c (c.id)}
				{@const expired = c.expires_at_ms !== null && c.expires_at_ms < Date.now()}
				<li class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
					<div class="min-w-0 flex-1 basis-60">
						<p class="font-medium">{c.name} <code class="ml-1 font-mono text-[12px] text-muted">{c.prefix}…</code></p>
						<p class="text-small text-muted"><a class="link" href="/admin/users/{c.owner_id}">{c.owner_email}</a> · {scope(c)}</p>
						<p class="mt-0.5 flex flex-wrap gap-1">{#each c.permissions as p (p)}<span class="eyebrow rounded-control border border-rule px-1.5 py-px">{p}</span>{/each}</p>
						<p class="text-small text-muted">Created {fmtWhen(c.created_at_ms)} · {c.last_used_at_ms ? `used ${fmtAgo(c.last_used_at_ms)}` : 'never used'} · {c.expires_at_ms === null ? 'does not expire' : expired ? 'expired' : `expires ${fmtWhen(c.expires_at_ms)}`}</p>
					</div>
					{#if manage}<button class="btn btn-sm btn-danger" onclick={() => revoke(c)}>Revoke</button>{/if}
				</li>
			{:else}
				<li class="px-4 py-4 text-small text-muted">{filter ? 'No API client matches.' : 'No account has an API client.'}</li>
			{/each}
		</ul>
	{/if}
</div>
