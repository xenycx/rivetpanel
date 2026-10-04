<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { OidcProvider, Role } from '$lib/api/types';
	import { can, session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// OpenID Connect providers. The server validates everything (issuer
	// discovery, no administrator role for new accounts, delegated managers
	// only hand out roles they hold); this page only edits.
	let providers = $state<OidcProvider[] | null>(null);
	let roles = $state<Role[]>([]);
	let publicURL = $state(true);
	let redirectBase = $state('');
	let error = $state('');
	let open = $state(false);
	let editing = $state<OidcProvider | null>(null);
	let form = $state(blank());
	let secret = $state('');
	let clearSecret = $state(false);
	let formError = $state('');
	let busy = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	function blank() {
		return { slug: '', name: '', issuer: '', client_id: '', scopes: 'openid email profile', enabled: true, allow_signup: false, link_by_email: true, default_role_id: '' };
	}
	async function load() {
		try {
			const r = await api<{ providers: OidcProvider[]; public_url: boolean; redirect_base: string }>('GET', '/admin/oidc-providers');
			providers = r.providers;
			publicURL = r.public_url;
			redirectBase = r.redirect_base;
			error = '';
		} catch (e) {
			error = msg(e);
		}
		try {
			roles = (await api<{ roles: Role[] }>('GET', '/admin/roles')).roles.filter((r) => !r.system);
		} catch {
			roles = []; // without users.view the built-in User role is the only choice shown
		}
	}
	onMount(load);

	function openNew() {
		editing = null;
		form = blank();
		secret = '';
		clearSecret = false;
		formError = '';
		open = true;
	}
	function openEdit(p: OidcProvider) {
		editing = p;
		form = { slug: p.slug, name: p.name, issuer: p.issuer, client_id: p.client_id, scopes: p.scopes, enabled: p.enabled, allow_signup: p.allow_signup, link_by_email: p.link_by_email, default_role_id: p.default_role_id };
		secret = '';
		clearSecret = false;
		formError = '';
		open = true;
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		// Omitting client_secret keeps the stored one; "" removes it.
		const body: Record<string, unknown> = { ...form };
		if (secret) body.client_secret = secret;
		else if (clearSecret || !editing) body.client_secret = '';
		try {
			if (editing) await api('PATCH', `/admin/oidc-providers/${editing.id}`, body);
			else await api('POST', '/admin/oidc-providers', body);
			toast(`Saved ${form.name}`, 'success');
			open = false;
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function remove(p: OidcProvider) {
		const ok = await confirmDialog({
			title: `Remove ${p.name}?`,
			body: 'Accounts linked to it can no longer sign in with it; accounts without a password then need a password reset from an account manager.',
			confirmLabel: 'Remove provider',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `/admin/oidc-providers/${p.id}`);
			toast(`Removed ${p.name}`);
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	const roleName = (id: string) => (id ? (roles.find((r) => r.id === id)?.name ?? 'a custom role') : 'User');
	const isAdmin = $derived(session.user?.role === 'admin');
</script>

<svelte:head><title>Single sign-on · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Single sign-on</h2>
		<p class="mt-1 max-w-3xl text-muted">Let people sign in with an OpenID Connect provider such as Keycloak, Authentik, Okta, Microsoft Entra ID or Google. Sign-in uses the authorization code flow with PKCE; the panel checks the identity token's signature, issuer, audience, expiry and nonce. Accounts with two-step sign-in still enter their code.</p>
	</div>
	{#if can('settings.manage')}<button class="btn btn-primary" onclick={openNew}><Icon name="plus" />Add provider</button>{/if}
</div>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if providers && !publicURL}<Notice tone="warn" class="mt-4">Set the panel address under Panel settings first: the provider redirects back to it, so sign-in buttons stay hidden until it is known.</Notice>{/if}

<div class="mt-4">
	{#if providers === null && !error}
		<Skeleton rows={2} label="Loading providers" />
	{:else if providers}
		<ul class="card overflow-hidden [&>li+li]:border-t [&>li+li]:border-rule-soft">
			{#each providers as p (p.id)}
				<li class="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
					<div class="min-w-0 flex-1 basis-60">
						<p class="font-medium">{p.name} <span class="pill ml-2" data-tone={p.enabled ? 'run' : 'idle'}>{p.enabled ? 'On' : 'Off'}</span></p>
						<p class="text-small text-muted break-all">{p.issuer} · client {p.client_id}{p.secret_set ? '' : ' (public client, PKCE only)'}</p>
						<p class="text-small text-muted">{p.allow_signup ? `New people get an account with the ${roleName(p.default_role_id)} role` : 'Only existing accounts can sign in'} · {p.link_by_email ? 'links accounts by provider-verified email' : 'links accounts only from Settings'}</p>
						<p class="text-small text-muted">Redirect URI: <code class="break-all select-all">{p.redirect_uri}</code></p>
					</div>
					<button class="btn btn-sm" onclick={() => openEdit(p)}>Edit</button>
					<button class="btn btn-sm btn-danger" onclick={() => remove(p)}>Remove</button>
				</li>
			{:else}
				<li class="px-4 py-4 text-small text-muted">No single sign-on providers yet.</li>
			{/each}
		</ul>
	{/if}
</div>

<Dialog bind:open title={editing ? `Edit ${editing.name}` : 'Add a single sign-on provider'} size="lg">
	<form id="oidc-form" class="grid gap-4" onsubmit={save}>
		<div class="grid gap-4 sm:grid-cols-2">
			<label class="block"><span class="label">Button name</span><input class="field" required maxlength="64" bind:value={form.name} placeholder="Company SSO" /></label>
			<label class="block"><span class="label">URL name</span><input class="field font-mono" required maxlength="32" pattern="[a-z0-9][a-z0-9-]*" bind:value={form.slug} placeholder="company" /></label>
		</div>
		<p class="help -mt-2">Register this redirect URI with the provider: <code class="break-all">{redirectBase}{form.slug || 'URL-NAME'}/callback</code></p>
		<label class="block"><span class="label">Issuer URL</span><input class="field font-mono" required bind:value={form.issuer} placeholder="https://login.example.com/realms/main" /><span class="help">The panel reads <code>/.well-known/openid-configuration</code> under it when you save. Must be https (http only for a provider on this machine).</span></label>
		<div class="grid gap-4 sm:grid-cols-2">
			<label class="block"><span class="label">Client ID</span><input class="field font-mono" required maxlength="256" bind:value={form.client_id} /></label>
			<label class="block">
				<span class="label">Client secret</span>
				<input class="field font-mono" type="password" autocomplete="off" bind:value={secret} placeholder={editing?.secret_set ? 'Stored; leave empty to keep' : 'Empty for a public client'} />
				{#if editing?.secret_set}<label class="mt-1 flex items-center gap-2 text-small"><input type="checkbox" bind:checked={clearSecret} disabled={!!secret} />Remove the stored secret</label>{/if}
			</label>
		</div>
		<label class="block"><span class="label">Scopes</span><input class="field font-mono" bind:value={form.scopes} /><span class="help">Space separated; <code>openid</code> is always requested. Ask for <code>email</code> so accounts can be matched and created.</span></label>
		<fieldset class="grid gap-2">
			<legend class="label">Accounts</legend>
			<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-1" bind:checked={form.enabled} /><span>Show the sign-in button<span class="help mt-0">Turn off to stop sign-in through this provider without removing links.</span></span></label>
			<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-1" bind:checked={form.link_by_email} /><span>Link to an existing account with the same address<span class="help mt-0">Only when the provider says the address is verified (<code>email_verified</code>), and never for accounts with administration permissions; otherwise people sign in another way and connect the provider under Settings → Connected accounts.</span></span></label>
			<label class="flex items-start gap-2.5"><input type="checkbox" class="mt-1" bind:checked={form.allow_signup} /><span>Create accounts for new people<span class="help mt-0">The address counts as verified only when the provider says so.</span></span></label>
		</fieldset>
		{#if form.allow_signup}
			<label class="block">
				<span class="label">Role for new accounts</span>
				<select class="field" bind:value={form.default_role_id}>
					<option value="">User (built in)</option>
					{#each roles as r (r.id)}<option value={r.id}>{r.name}</option>{/each}
				</select>
				<span class="help">Never Administrator.{isAdmin ? '' : ' You can only choose a role whose permissions you hold.'}</span>
			</label>
		{/if}
		{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="oidc-form" disabled={busy}>{busy ? 'Checking the provider…' : editing ? 'Save' : 'Add provider'}</button>
	{/snippet}
</Dialog>
