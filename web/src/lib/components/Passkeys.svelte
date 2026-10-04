<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { addPasskey, passkeysSupported } from '$lib/passkeys';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	type Passkey = { id: string; name: string; created_at_ms: number; last_used_at_ms: number | null };
	let keys = $state<Passkey[] | null>(null);
	let available = $state(true);
	let error = $state('');
	const supported = passkeysSupported();
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load() {
		try {
			const r = await api<{ passkeys: Passkey[]; available: boolean }>('GET', '/me/passkeys');
			keys = r.passkeys;
			available = r.available;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	let open = $state(false);
	let name = $state('');
	let password = $state('');
	let busy = $state(false);
	let formError = $state('');
	function start() {
		name = 'Passkey';
		password = '';
		formError = '';
		open = true;
	}
	async function add(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			await addPasskey(name, password);
			toast('Passkey added', 'success');
			open = false;
			await load();
		} catch (err) {
			formError = err instanceof DOMException ? (err.name === 'NotAllowedError' ? 'The browser prompt was cancelled.' : err.message) : msg(err);
		} finally {
			busy = false;
		}
	}
	async function rename(k: Passkey) {
		const n = prompt('Name this passkey', k.name)?.trim();
		if (!n || n === k.name) return;
		try {
			await api('PATCH', `/me/passkeys/${k.id}`, { name: n });
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(k: Passkey) {
		const ok = await confirmDialog({ title: `Remove “${k.name}”?`, body: 'You can no longer sign in with it. Remove it from the device or password manager too.', confirmLabel: 'Remove passkey', tone: 'danger' });
		if (!ok) return;
		try {
			await api('DELETE', `/me/passkeys/${k.id}`);
			toast('Passkey removed');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
</script>

<SettingsSection title="Passkeys" description="Sign in with your device's fingerprint, face or PIN instead of a password. A passkey also works instead of the code when two-step sign-in is on. Only the public key is kept on the panel.">
	{#if error}<Notice tone="fail" class="mb-3">{error}</Notice>{/if}
	{#if !available}<Notice tone="info" class="mb-3">Passkeys need the panel address (a host name, not an IP address); an administrator sets it under Panel settings.</Notice>{/if}
	{#if !supported}<Notice tone="info" class="mb-3">This browser cannot use passkeys.</Notice>{/if}
	<div class="card overflow-hidden">
		<div class="flex items-center justify-between gap-2 border-b border-rule-soft px-4 py-2.5">
			<p class="text-small text-muted">{keys ? `${keys.length} passkey${keys.length === 1 ? '' : 's'}` : 'Loading…'}</p>
			<button class="btn btn-sm btn-primary" onclick={start} disabled={!available || !supported}><Icon name="plus" size={14} />Add a passkey</button>
		</div>
		<ul class="divide-y divide-rule-soft">
			{#each keys ?? [] as k (k.id)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<p class="font-medium">{k.name}</p>
						<p class="text-small text-muted">Added {fmtWhen(k.created_at_ms)} · {k.last_used_at_ms ? `used ${fmtAgo(k.last_used_at_ms)}` : 'not used yet'}</p>
					</div>
					<button class="btn btn-sm" onclick={() => rename(k)}>Rename</button>
					<button class="btn btn-sm btn-danger" onclick={() => remove(k)}>Remove</button>
				</li>
			{:else}
				<li class="px-4 py-4 text-small text-muted">{keys ? 'No passkeys yet.' : 'Loading…'}</li>
			{/each}
		</ul>
	</div>
</SettingsSection>

<Dialog bind:open title="Add a passkey" size="md">
	<form id="passkey-form" class="grid gap-4" onsubmit={add}>
		<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} placeholder="Work laptop" /></label>
		{#if session.hasPassword}
			<label class="block"><span class="label">Current password</span><input class="field" type="password" autocomplete="current-password" required bind:value={password} /></label>
		{:else}
			<p class="help mt-0">Without a password, add passkeys within 10 minutes of signing in.</p>
		{/if}
		<p class="help mt-0">Your browser then asks you to confirm with the device.</p>
		{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="passkey-form" disabled={busy}>{busy ? 'Waiting for the device…' : 'Continue'}</button>
	{/snippet}
</Dialog>
