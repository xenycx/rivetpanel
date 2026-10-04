<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtTime } from '$lib/args';
	import type { ApiKey } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import AutomationTokens from '$lib/components/AutomationTokens.svelte';
	import ApiClients from '$lib/components/ApiClients.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	let sftp = $state<{ enabled: boolean; port?: string; fingerprint?: string } | null>(null);
	let keys = $state<ApiKey[]>([]);
	let name = $state('');
	let days = $state(90);
	let created = $state('');
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		keys = (await api<{ keys: ApiKey[] }>('GET', '/me/api-keys')).keys;
	}
	onMount(async () => {
		try {
			sftp = await api('GET', '/me/sftp');
			await load();
		} catch (e) {
			error = msg(e);
		}
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		error = created = '';
		try {
			created = (await api<{ key: string }>('POST', '/me/api-keys', { name, expires_in_days: days })).key;
			name = '';
			await load();
		} catch (err) {
			error = msg(err);
		}
	}
	async function remove(k: ApiKey) {
		const ok = await confirmDialog({ title: `Delete the key “${k.name}”?`, body: 'Clients using it are disconnected within seconds and cannot sign in again.', confirmLabel: 'Delete key', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/me/api-keys/${k.id}`);
			await load();
			toast(`Deleted ${k.name}`);
		} catch (e) {
			error = msg(e);
		}
	}
	const host = location.hostname;
</script>

<svelte:head><title>SFTP & API keys · RivetPanel</title></svelte:head>

<SettingsSection title="SFTP access" description="Use any SFTP client (FileZilla, Cyberduck, sftp). You see one folder per bot you can edit.">
	{#if sftp && !sftp.enabled}
		<Notice>The SFTP server is turned off on this panel. The administrator can enable it with <code class="font-mono">RIVET_SFTP_LISTEN</code>.</Notice>
	{:else if sftp}
		<dl class="card grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2.5 p-5">
			<dt class="text-muted">Host</dt><dd><code class="copyable font-mono">{host}</code> <span class="text-small text-muted">connect to the server itself: SFTP does not go through a web proxy or tunnel</span></dd>
			<dt class="text-muted">Port</dt><dd><code class="copyable font-mono">{sftp.port}</code></dd>
			<dt class="text-muted">Username</dt><dd><code class="copyable font-mono">{session.user?.email}</code></dd>
			<dt class="text-muted">Password</dt><dd class="text-small">Your panel password, or an SFTP key from below (required if you sign in with GitHub or Discord only, or use two-step sign-in).</dd>
			<dt class="text-muted">Host key</dt><dd><code class="break-all font-mono text-[13px]">{sftp.fingerprint}</code></dd>
		</dl>
	{/if}
</SettingsSection>

<SettingsSection title="SFTP keys" description="Keys let SFTP clients sign in without your password. A key works only for your account and can be deleted at any time.">
	<div class="card overflow-hidden">
		<ul class="divide-y divide-rule-soft">
			{#each keys as k (k.id)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<div class="font-medium">{k.name} <code class="ml-1 font-mono text-[12px] text-muted">{k.prefix}…</code></div>
						<div class="text-small text-muted">Created {fmtTime(k.created_at_ms)} · {k.last_used_at_ms ? `last used ${fmtTime(k.last_used_at_ms)}` : 'never used'} · {k.expires_at_ms ? `expires ${fmtTime(k.expires_at_ms)}` : 'no expiry'}</div>
					</div>
					<button class="btn btn-sm btn-danger" onclick={() => remove(k)}>Delete</button>
				</li>
			{:else}
				<li class="px-4 py-4 text-small text-muted">No SFTP keys yet.</li>
			{/each}
		</ul>
		<form class="grid gap-3 border-t border-rule-soft bg-paper/40 p-4 sm:grid-cols-[2fr_1fr_auto] sm:items-end" onsubmit={create}>
			<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} placeholder="Laptop FileZilla" /></label>
			<label class="block"><span class="label">Expires in (days)</span><input class="field" type="number" min="0" max="3650" bind:value={days} /></label>
			<button class="btn btn-primary"><Icon name="plus" size={14} />Create key</button>
			<p class="text-small text-muted sm:col-span-3">0 days means the key never expires.</p>
		</form>
	</div>
	{#if created}
		<Notice tone="warn" class="mt-3" title="Copy the key now">
			It is shown only once.<br /><code class="break-all">{created}</code>
			{#snippet action()}<button class="btn btn-sm" onclick={() => navigator.clipboard?.writeText(created).then(() => toast('Key copied'))}>Copy</button>{/snippet}
		</Notice>
	{/if}
	{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
</SettingsSection>

{#if session.features.automation}<AutomationTokens />{/if}
{#if session.features.api_clients}<ApiClients />{/if}
