<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, loadSession } from '$lib/session.svelte';
	import type { PermissionInfo } from '$lib/api/types';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import ProviderSetup from '$lib/components/ProviderSetup.svelte';
	import AISettings from '$lib/components/AISettings.svelte';

	type View = {
		public_url: string;
		github_client_id: string;
		github_secret_set: boolean;
		discord_client_id: string;
		discord_secret_set: boolean;
		oauth_allow_signup: boolean;
		locked: Record<string, boolean>;
		github_enabled: boolean;
		discord_enabled: boolean;
		mailgun_key_set: boolean;
		mailgun_domain: string;
		mailgun_region: string;
		mail_from: string;
		mail_enabled: boolean;
		unverified_restrict: string[];
	};
	type MailTest = { message_id: string; domain: string; state: string; sandbox: boolean; dns_verified: boolean };
	let v = $state<View | null>(null);
	let error = $state('');
	let warning = $state('');
	let saving = $state(false);
	let publicUrl = $state('');
	let ghId = $state('');
	let ghSecret = $state('');
	let dcId = $state('');
	let dcSecret = $state('');
	let signup = $state(false);
	let mgKey = $state('');
	let mgDomain = $state('');
	let mgRegion = $state('us');
	let mgFrom = $state('');
	let testTo = $state('');
	let testing = $state(false);
	let testResult = $state<MailTest | null>(null);
	let testError = $state('');
	let catalog = $state<PermissionInfo[]>([]);
	let restrictOn = $state(false);
	let restrict = $state<string[]>([]);
	// Suggested when the policy is first turned on: creating things and
	// minting credentials, not using what already exists.
	const suggested = ['bots.create', 'sites.create', 'workspaces.create', 'api_keys.manage', 'ai.use'];
	const manageSettings = $derived(can('settings.manage'));

	function fill(x: View) {
		v = x;
		publicUrl = x.public_url;
		ghId = x.github_client_id;
		dcId = x.discord_client_id;
		ghSecret = dcSecret = '';
		signup = x.oauth_allow_signup;
		mgDomain = x.mailgun_domain;
		mgRegion = x.mailgun_region || 'us';
		mgFrom = x.mail_from;
		mgKey = '';
		restrict = [...(x.unverified_restrict ?? [])];
		restrictOn = restrict.length > 0;
	}
	function toggleRestrict(p: string, on: boolean) {
		restrict = on ? [...restrict.filter((x) => x !== p), p] : restrict.filter((x) => x !== p);
	}

	onMount(async () => {
		try {
			warning = sessionStorage.getItem('rivetpanel.setupWarning') ?? '';
			sessionStorage.removeItem('rivetpanel.setupWarning');
		} catch {
			/* ignore */
		}
		if (!can('settings.manage')) return;
		try {
			fill(await api<View>('GET', '/admin/settings'));
			catalog = (await api<{ permissions: PermissionInfo[] }>('GET', '/admin/permissions')).permissions;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Settings could not be loaded.';
		}
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!v) return;
		saving = true;
		error = '';
		const body: Record<string, unknown> = {};
		if (!v.locked.public_url) body.public_url = publicUrl;
		if (!v.locked.github_client_id) body.github_client_id = ghId;
		if (!v.locked.github_client_secret && ghSecret) body.github_client_secret = ghSecret;
		if (!v.locked.discord_client_id) body.discord_client_id = dcId;
		if (!v.locked.discord_client_secret && dcSecret) body.discord_client_secret = dcSecret;
		if (!v.locked.oauth_allow_signup) body.oauth_allow_signup = signup;
		if (!v.locked.mailgun_api_key && mgKey) body.mailgun_api_key = mgKey;
		if (!v.locked.mailgun_domain) body.mailgun_domain = mgDomain;
		if (!v.locked.mailgun_region) body.mailgun_region = mgRegion;
		if (!v.locked.mail_from) body.mail_from = mgFrom;
		body.unverified_restrict = restrictOn ? catalog.map((p) => p.name).filter((p) => restrict.includes(p)) : [];
		try {
			fill(await api<View>('PUT', '/admin/settings', body));
			await loadSession(); // feature flags follow the new providers and email
			warning = '';
			toast('Settings saved and applied', 'success');
		} catch (err) {
			error = err instanceof ApiError ? err.message.charAt(0).toUpperCase() + err.message.slice(1) : 'The settings could not be saved.';
		} finally {
			saving = false;
		}
	}
	async function sendTest() {
		testing = true;
		testResult = null;
		testError = '';
		try {
			testResult = await api<MailTest>('POST', '/admin/settings/mail/test', { to: testTo.trim() });
		} catch (err) {
			testError = err instanceof ApiError ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' : 'The test could not be sent.';
		} finally {
			testing = false;
		}
	}
	async function disableMail() {
		try {
			fill(await api<View>('PUT', '/admin/settings', { mailgun_api_key: '' }));
			await loadSession();
			testResult = null;
			toast('Email turned off');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The request failed.', 'fail');
		}
	}
	async function disable(p: 'github' | 'discord') {
		try {
			fill(await api<View>('PUT', '/admin/settings', { [`${p}_client_secret`]: '', [`${p}_client_id`]: '' }));
			await loadSession();
			toast(`${p === 'github' ? 'GitHub' : 'Discord'} sign-in turned off`);
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The request failed.', 'fail');
		}
	}
</script>

<svelte:head><title>Panel settings · RivetPanel</title></svelte:head>

{#if manageSettings}
<h2 class="text-section">Panel settings</h2>
<p class="mt-1 max-w-3xl text-muted">Applied immediately, without a restart. Values set in the environment file are shown read-only and always win.</p>
{#if warning}<Notice tone="warn" class="mt-3" title="Setup finished, but some settings were not saved">{warning}</Notice>{/if}
{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}

{#if !v && !error}
	<div class="mt-4"><Skeleton rows={4} label="Loading settings" /></div>
{:else if v}
	<form class="mt-6 grid w-full min-w-0 grid-cols-[minmax(0,1fr)] gap-4 xl:grid-cols-2" onsubmit={save}>
		<section class="card flex flex-col p-5 sm:p-6">
			<div class="flex items-center gap-2"><Icon name="globe" size={18} class="text-muted" /><h3 class="text-title font-semibold">Panel address</h3></div>
			<p class="mt-1 text-small text-muted">The https origin people use to reach this panel. Sign-in providers return to it, and bots send statistics to it.</p>
			<label class="mt-auto block pt-4">
				<span class="sr-only">Panel address</span>
				<input class="field font-mono" bind:value={publicUrl} disabled={v.locked.public_url} placeholder="https://panel.example.com" />
				{#if v.locked.public_url}<span class="help">Set in the environment file (RIVET_PUBLIC_URL).</span>{/if}
			</label>
		</section>

		<section class="card flex flex-col p-5 sm:p-6">
			<div class="flex items-center gap-2"><Icon name="users" size={18} class="text-muted" /><h3 class="text-title font-semibold">Registration</h3><span class="pill ml-auto" data-tone={signup ? 'run' : 'idle'}>{signup ? 'Open' : 'Closed'}</span></div>
			<p class="mt-1 text-small text-muted">Who can create an account without an administrator.</p>
			<label class="mt-auto flex items-start gap-2.5 pt-4">
				<input type="checkbox" class="mt-0.5" bind:checked={signup} disabled={v.locked.oauth_allow_signup} />
				<span>Allow new accounts from invitations, GitHub, or Discord<span class="help">Turn this off to stop every self-registration path. Administrators can still create accounts directly. Provider identities are never merged into an account by matching email.</span></span>
			</label>
		</section>

		{#each [{ id: 'github', label: 'GitHub', on: v.github_enabled, icon: 'github' }, { id: 'discord', label: 'Discord', on: v.discord_enabled, icon: 'discord' }] as p (p.id)}
			<section class="card p-5 sm:p-6">
				<div class="flex flex-wrap items-center gap-2">
					<Icon name={p.icon as 'github'} size={20} />
					<h3 class="text-title font-semibold">{p.label} sign-in</h3>
					<span class="pill" data-tone={p.on ? 'run' : 'idle'}>{p.on ? 'On' : 'Off'}</span>
					<span class="flex-1"></span>
					{#if p.on && !v.locked[`${p.id}_client_id`]}<button type="button" class="btn btn-sm btn-danger" onclick={() => disable(p.id as 'github')}>Turn off</button>{/if}
				</div>
				<p class="mt-1 text-small text-muted">{p.id === 'github' ? 'Sign-in with GitHub, repository pickers and deployments on push.' : 'Sign-in with Discord and alerts to a Discord channel.'}</p>
				<div class="mt-4">
					{#if p.id === 'github'}
						<ProviderSetup provider="github" {publicUrl} bind:clientId={ghId} bind:secret={ghSecret} secretSet={v.github_secret_set} locked={!!v.locked.github_client_id} />
					{:else}
						<ProviderSetup provider="discord" {publicUrl} bind:clientId={dcId} bind:secret={dcSecret} secretSet={v.discord_secret_set} locked={!!v.locked.discord_client_id} />
					{/if}
				</div>
			</section>
		{/each}


		<section class="card p-5 sm:p-6 xl:col-span-2" id="unverified">
			<div class="flex flex-wrap items-center gap-2">
				<Icon name="shield" size={20} class="text-muted" />
				<h3 class="text-title font-semibold">Unverified email addresses</h3>
				<span class="pill" data-tone={restrictOn ? 'warn' : 'idle'}>{restrictOn ? 'Restricted' : 'Not restricted'}</span>
			</div>
			<p class="mt-1 text-small text-muted">New accounts confirm their address through an emailed link. Accounts that existed before verification, accounts created through GitHub or Discord, and administrators count as verified. The server refuses what is withheld until the address is confirmed.</p>
			<label class="mt-4 flex items-start gap-2.5">
				<input type="checkbox" class="mt-0.5" bind:checked={restrictOn} onchange={() => { if (restrictOn && restrict.length === 0) restrict = [...suggested]; }} />
				<span>Restrict accounts until their email address is verified<span class="help">{v.mail_enabled ? 'They can send themselves a link from their profile.' : 'Email is off, so people cannot verify themselves: an account manager has to mark addresses verified on the Users page.'}</span></span>
			</label>
			{#if restrictOn}
				<p class="label mt-4">Withhold these permissions</p>
				<div class="mt-1 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
					{#each catalog as p (p.name)}
						<label class="flex items-start gap-2.5 text-small">
							<input type="checkbox" class="mt-0.5" checked={restrict.includes(p.name)} onchange={(e) => toggleRestrict(p.name, e.currentTarget.checked)} />
							<span>{p.label}{#if p.group === 'administration'}<span class="text-muted"> (administration)</span>{/if}</span>
						</label>
					{/each}
				</div>
			{/if}
		</section>

		<section class="card p-5 sm:p-6 xl:col-span-2">
			<div class="flex flex-wrap items-center gap-2">
				<Icon name="send" size={20} class="text-muted" />
				<h3 class="text-title font-semibold">Email (Mailgun)</h3>
				<span class="pill" data-tone={v.mail_enabled ? 'run' : 'idle'}>{v.mail_enabled ? 'On' : 'Off'}</span>
				<span class="flex-1"></span>
				{#if v.mail_enabled && !v.locked.mailgun_api_key}<button type="button" class="btn btn-sm btn-danger" onclick={disableMail}>Turn off</button>{/if}
			</div>
			<p class="mt-1 text-small text-muted">Sends password-reset links, invitations, bot alerts and security notices. It needs the key, the sending domain and a sender; nothing runs in the background while it is idle.</p>
			<div class="mt-4 grid gap-4 md:grid-cols-2">
				<label class="block">
					<span class="label">API key</span>
					<input class="field font-mono" type="password" autocomplete="off" spellcheck="false" bind:value={mgKey} disabled={v.locked.mailgun_api_key} placeholder={v.mailgun_key_set ? '•••••••• saved; type to replace' : 'Mailgun private or domain sending key'} />
					<span class="help">{v.locked.mailgun_api_key ? 'Set in the environment file (RIVET_MAILGUN_API_KEY).' : 'Stored encrypted and never shown again.'}</span>
				</label>
				<label class="block">
					<span class="label">Sending domain</span>
					<input class="field font-mono" bind:value={mgDomain} disabled={v.locked.mailgun_domain} placeholder="mg.example.com" />
					<span class="help">{v.locked.mailgun_domain ? 'Set in the environment file (RIVET_MAILGUN_DOMAIN).' : 'A domain you verified in Mailgun. A sandbox domain only delivers to addresses you authorized there.'}</span>
				</label>
				<label class="block">
					<span class="label">Region</span>
					<select class="field" bind:value={mgRegion} disabled={v.locked.mailgun_region}>
						<option value="us">US (api.mailgun.net)</option>
						<option value="eu">EU (api.eu.mailgun.net)</option>
					</select>
					<span class="help">{v.locked.mailgun_region ? 'Set in the environment file (RIVET_MAILGUN_REGION).' : 'The region your Mailgun domain was created in.'}</span>
				</label>
				<label class="block">
					<span class="label">Sender</span>
					<input class="field" bind:value={mgFrom} disabled={v.locked.mail_from} placeholder="RivetPanel <noreply@mg.example.com>" />
					<span class="help">{v.locked.mail_from ? 'Set in the environment file (RIVET_MAIL_FROM).' : 'Must be an address on the sending domain.'}</span>
				</label>
			</div>
			<div class="mt-5 border-t border-rule-soft pt-4">
				<p class="label">Send a test email</p>
				<p class="help">Save first, then send. This also checks the key, region and the domain's DNS records.</p>
				<div class="mt-2 flex flex-wrap gap-2">
					<input class="field max-w-xs" type="email" bind:value={testTo} placeholder="Your address (default: your account email)" aria-label="Test recipient" />
					<button type="button" class="btn" onclick={sendTest} disabled={testing || !v.mail_enabled}>{testing ? 'Sending…' : 'Send test'}</button>
				</div>
				{#if testResult}
					<Notice tone={testResult.dns_verified && !testResult.sandbox ? 'success' : 'warn'} class="mt-3" title="Test email sent">
						Accepted by Mailgun for {testResult.domain}.
						{#if testResult.sandbox}This is a sandbox domain: it only delivers to authorized recipients.{:else if !testResult.dns_verified}The domain's sending DNS records are not all verified yet, so mail may land in spam.{:else}The domain is active and its DNS records are verified.{/if}
					</Notice>
				{/if}
				{#if testError}<Notice tone="fail" class="mt-3" live>{testError}</Notice>{/if}
			</div>
		</section>

		<div class="sticky bottom-4 flex justify-end xl:col-span-2"><button class="btn btn-primary shadow-overlay" disabled={saving}>{saving ? 'Saving…' : 'Save and apply'}</button></div>
	</form>
{/if}
{/if}
{#if can('ai.manage') && (!manageSettings || v)}
	<div class={manageSettings ? 'mt-10 border-t border-rule-soft pt-8' : ''}>
		<p class="eyebrow">AI operator</p>
		<h2 class="mt-1 text-section">AI assistant</h2>
		<p class="mt-1 max-w-3xl text-muted">The assistant behind Ask AI. It needs one provider; web research is an optional extra. Nothing here affects hosting.</p>
		<div class="mt-4"><AISettings /></div>
	</div>
{/if}
