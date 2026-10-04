<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import { oauthErrors, type Connection, type Identity, type Provider } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	const meta: Record<Provider, { name: string; blurb: string }> = {
		discord: { name: 'Discord', blurb: 'Connect to receive deployment notifications via Discord.' },
		github: { name: 'GitHub', blurb: 'Connect to deploy from your repositories, and to create repositories from a bot’s code.' }
	};

	let connections = $state<Connection[]>([]);
	let loaded = $state(false);
	let error = $state('');
	let notice = $state('');
	let busy = $state<string>('');

	let identities = $state<Identity[]>([]);
	let available = $state<{ slug: string; name: string }[]>([]);

	async function load() {
		const [c, i] = await Promise.all([
			api<{ connections: Connection[] }>('GET', '/me/connections'),
			api<{ identities: Identity[]; available: { slug: string; name: string }[] }>('GET', '/me/identities')
		]);
		connections = c.connections;
		identities = i.identities;
		available = i.available;
		loaded = true;
	}

	async function linkSSO(slug: string) {
		busy = 'oidc:' + slug;
		error = '';
		try {
			const r = await api<{ url: string }>('POST', `/me/identities/${slug}/start`);
			window.location.assign(r.url); // full navigation to the provider
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not start the connection.';
			busy = '';
		}
	}

	async function unlinkSSO(i: Identity) {
		const ok = await confirmDialog({ title: `Disconnect ${i.name}?`, body: `You can no longer sign in with ${i.name}.`, confirmLabel: 'Disconnect', tone: 'danger' });
		if (!ok) return;
		busy = 'oidc:' + i.slug;
		error = notice = '';
		try {
			await api('DELETE', `/me/identities/${i.provider_id}`);
			await load();
			toast(`${i.name} disconnected`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not disconnect.';
		} finally {
			busy = '';
		}
	}

	onMount(async () => {
		const code = page.url.searchParams.get('error');
		if (code) error = oauthErrors[code] ?? 'Could not connect the account.';
		const linked = page.url.searchParams.get('linked');
		if (linked) notice = `${meta[linked as Provider]?.name ?? 'Single sign-on'} connected.`;
		try {
			await load();
		} catch {
			error = error || 'Could not load your connected accounts.';
		}
	});

	async function connect(p: Provider, notifications = false, repoAccess = false) {
		busy = p;
		error = '';
		try {
			const r = await api<{ url: string }>('POST', `/me/connections/${p}/start`, { notifications, repo_access: repoAccess });
			window.location.assign(r.url); // full navigation to the provider
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not start the connection.';
			busy = '';
		}
	}

	async function disconnect(c: Connection) {
		const ok = await confirmDialog({
			title: `Disconnect ${meta[c.provider].name}?`,
			body: c.provider === 'github' ? 'You can no longer sign in with GitHub, and deployments that use your GitHub access stop working until you connect again.' : 'You can no longer sign in with Discord, and Discord notifications stop.',
			confirmLabel: 'Disconnect',
			tone: 'danger'
		});
		if (!ok) return;
		busy = c.provider;
		error = notice = '';
		try {
			await api('DELETE', `/me/connections/${c.provider}`);
			await load();
			toast(`${meta[c.provider].name} disconnected`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'Could not disconnect.';
		} finally {
			busy = '';
		}
	}

	const description = (c: Connection) =>
		c.linked
			? c.provider === 'discord'
				? `Connected as ${c.username}. Notifications ${c.notifications ? 'are on' : 'are off'}.`
				: `Connected as ${c.username}. ${c.repo_access ? 'Private repositories, webhooks and pushing code are enabled.' : 'Public repositories only; grant repository access to deploy private code or push to GitHub.'}`
			: meta[c.provider].blurb;
</script>

<svelte:head><title>Connected accounts · RivetPanel</title></svelte:head>

<SettingsSection title="Connected accounts" description="Sign in with GitHub, Discord or your organization's single sign-on instead of a password, deploy from and push to your repositories, and receive notifications in Discord. GitHub and Discord are only linked from here, never matched by email address.">
{#if notice}<Notice tone="success" class="mb-4" live>{notice}</Notice>{/if}
{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
<ul class="card divide-y divide-rule-soft overflow-hidden">
	{#each connections as c (c.provider)}
		<li class="flex flex-wrap items-center gap-4 px-4 py-4">
			<span class="grid size-10 shrink-0 place-items-center rounded-tile {c.provider === 'discord' ? 'bg-[#5865f2] text-white' : 'bg-ink text-paper'}" aria-hidden="true"><Icon name={c.provider} size={20} /></span>
			<div class="min-w-0 flex-1 basis-56">
				<p class="flex flex-wrap items-center gap-2">
					<span class="font-semibold">{meta[c.provider].name}</span>
					<span class="pill" data-tone={c.linked ? 'run' : undefined}>{c.linked ? 'Linked' : c.configured ? 'Not linked' : 'Not set up on this panel'}</span>
				</p>
				<p class="text-small text-muted">{c.configured ? description(c) : `The administrator has to configure ${meta[c.provider].name} sign-in first (docs/oauth.md).`}</p>
			</div>
			<div class="flex flex-wrap gap-2">
				{#if c.linked && c.provider === 'discord' && !c.notifications}
					<button class="btn" disabled={busy !== ''} onclick={() => connect('discord', true)}>Turn on notifications</button>
				{/if}
				{#if c.linked && c.provider === 'github' && !c.repo_access}
					<button class="btn" disabled={busy !== ''} onclick={() => connect('github', false, true)} title="Needed to deploy private repositories and add webhooks">Grant repository access</button>
				{/if}
				{#if c.linked}
					<button class="btn btn-danger" disabled={!c.can_disconnect || busy !== ''} title={c.can_disconnect ? '' : 'This is your only way to sign in. Add a password first.'} onclick={() => disconnect(c)}>Disconnect</button>
				{:else}
					<button class="btn btn-primary" disabled={!c.configured || busy !== ''} onclick={() => connect(c.provider, c.provider === 'discord')}>Connect</button>
				{/if}
			</div>
		</li>
	{:else}
		{#if loaded}<li class="px-4 py-5 text-muted">No sign-in providers are available on this panel.</li>{/if}
	{/each}
</ul>
{#if identities.length || available.length}
	<h3 class="mt-6 font-semibold">Single sign-on</h3>
	<p class="text-small text-muted">Your organization's sign-in providers. Connecting one here links it to this account whatever address it reports.</p>
	<ul class="card mt-2 divide-y divide-rule-soft overflow-hidden">
		{#each identities as i (i.provider_id)}
			<li class="flex flex-wrap items-center gap-4 px-4 py-4">
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-ink text-paper" aria-hidden="true"><Icon name="key" size={20} /></span>
				<div class="min-w-0 flex-1 basis-56">
					<p class="flex flex-wrap items-center gap-2"><span class="font-semibold">{i.name}</span><span class="pill" data-tone="run">Linked</span></p>
					<p class="text-small text-muted">{i.email ? `As ${i.email}${i.email_verified ? '' : ' (not verified by the provider)'}` : 'No email address shared'} · {i.last_login_at_ms ? `last used ${fmtAgo(i.last_login_at_ms)}` : 'not used to sign in yet'}</p>
				</div>
				<button class="btn btn-danger" disabled={!i.can_unlink || busy !== ''} title={i.can_unlink ? '' : 'This is your only way to sign in. Add a password first.'} onclick={() => unlinkSSO(i)}>Disconnect</button>
			</li>
		{/each}
		{#each available as p (p.slug)}
			<li class="flex flex-wrap items-center gap-4 px-4 py-4">
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-panel text-muted" aria-hidden="true"><Icon name="key" size={20} /></span>
				<div class="min-w-0 flex-1 basis-56"><p class="flex flex-wrap items-center gap-2"><span class="font-semibold">{p.name}</span><span class="pill">Not linked</span></p></div>
				<button class="btn btn-primary" disabled={busy !== ''} onclick={() => linkSSO(p.slug)}>Connect</button>
			</li>
		{/each}
	</ul>
{/if}
<p class="mt-3 text-small text-muted">You always keep at least one way to sign in: Disconnect is unavailable for your only method. A password counts; add one under <a class="link" href="/settings/security">Security</a>.</p>
</SettingsSection>
