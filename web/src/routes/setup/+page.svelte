<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, setCsrf } from '$lib/api/client';
	import { loadSession } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import ProviderSetup from '$lib/components/ProviderSetup.svelte';

	// First-run setup: runs only while the installation has no account, and
	// only with the setup code the server printed in its log.
	const steps = ['Setup code', 'Administrator', 'Panel address', 'GitHub', 'Discord', 'AI operator', 'Finish'];
	let step = $state(0);
	let codeFile = $state('');
	let done = $state(false);
	let busy = $state(false);
	let error = $state('');

	let code = $state('');
	let email = $state('');
	let password = $state('');
	let confirm = $state('');
	let publicUrl = $state('');
	let useGitHub = $state(false);
	let ghId = $state('');
	let ghSecret = $state('');
	let useDiscord = $state(false);
	let dcId = $state('');
	let dcSecret = $state('');
	let allowSignup = $state(false);
	// Optional first AI operator provider (an OpenAI-compatible endpoint).
	let useAI = $state(false);
	let aiName = $state('DeepSeek');
	let aiBaseUrl = $state('https://api.deepseek.com');
	let aiModel = $state('deepseek-chat');
	let aiKey = $state('');

	onMount(async () => {
		publicUrl = location.origin;
		try {
			const r = await api<{ needed: boolean; code_file: string }>('GET', '/setup/status');
			codeFile = r.code_file;
			if (!r.needed) done = true;
		} catch {
			error = 'The panel does not respond. Is it running?';
		}
	});

	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');
	const httpsOk = $derived(/^https:\/\//.test(publicUrl) || /^http:\/\/(localhost|127\.0\.0\.1|\[::1\])(:\d+)?\/?$/.test(publicUrl));

	async function next(e?: SubmitEvent) {
		e?.preventDefault();
		error = '';
		if (step === 0) {
			busy = true;
			try {
				await api('POST', '/setup/check', { code });
			} catch (err) {
				error = msg(err);
				busy = false;
				return;
			}
			busy = false;
		}
		if (step === 1) {
			if (password.length < 12) return (error = 'Use at least 12 characters.');
			if (password !== confirm) return (error = 'The passwords do not match.');
		}
		if (step === 3 && useGitHub && (!ghId.trim() || !ghSecret.trim())) return (error = 'Paste both the client ID and the secret, or skip GitHub.');
		if (step === 4 && useDiscord && (!dcId.trim() || !dcSecret.trim())) return (error = 'Paste both the client ID and the secret, or skip Discord.');
		if (step === 5 && useAI && (!aiName.trim() || !aiBaseUrl.trim() || !aiModel.trim() || !aiKey.trim())) return (error = 'Fill in the provider, address, model and API key, or skip the AI operator.');
		step = Math.min(step + 1, steps.length - 1);
	}

	async function finish() {
		busy = true;
		error = '';
		const settings: Record<string, unknown> = { public_url: publicUrl, oauth_allow_signup: allowSignup };
		if (useGitHub) Object.assign(settings, { github_client_id: ghId.trim(), github_client_secret: ghSecret.trim() });
		if (useDiscord) Object.assign(settings, { discord_client_id: dcId.trim(), discord_client_secret: dcSecret.trim() });
		try {
			const body: Record<string, unknown> = { code, email, password, settings };
			if (useAI) body.ai_provider = { name: aiName.trim(), base_url: aiBaseUrl.trim(), default_model: aiModel.trim(), key: aiKey.trim(), max_output_tokens: 4096, timeout_ms: 120000 };
			const r = await api<{ csrf_token: string; settings_error?: string }>('POST', '/setup/complete', body);
			aiKey = '';
			setCsrf(r.csrf_token);
			await loadSession();
			if (r.settings_error) sessionStorage.setItem('rivetpanel.setupWarning', r.settings_error);
			await goto(r.settings_error ? '/admin/settings' : '/');
		} catch (err) {
			error = msg(err);
			if (err instanceof ApiError && /code/.test(err.message)) step = 0;
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Set up RivetPanel</title></svelte:head>

<main class="min-h-dvh px-4 py-8 sm:py-14">
	<div class="mx-auto max-w-5xl">
		<div class="flex items-center gap-2.5">
			<img src="/favicon.svg" alt="" width="34" height="34" class="rounded-tile" />
			<span class="font-semibold tracking-[0.08em] uppercase">RivetPanel</span>
			<span class="pill ml-2">First-run setup</span>
		</div>

		{#if done}
			<section class="card card-glow mt-8 max-w-xl p-8">
				<p class="eyebrow">Setup</p>
				<h1 class="mt-2 text-page">This panel is already set up<span class="text-action">.</span></h1>
				<p class="mt-2 text-muted">Sign in with the administrator account. Settings live under Administration → Panel settings.</p>
				<a class="btn btn-primary mt-5" href="/login">Sign in</a>
			</section>
		{:else}
			<div class="mt-8 grid gap-8 lg:grid-cols-[15rem_minmax(0,1fr)]">
				<ol class="flex gap-2 overflow-x-auto lg:flex-col lg:gap-1" aria-label="Setup steps">
					{#each steps as s, i (s)}
						<li class="flex shrink-0 items-center gap-3 rounded-tile px-3 py-2 {i === step ? 'bg-panel font-medium' : 'text-muted'}" aria-current={i === step ? 'step' : undefined}>
							<span class="grid size-6 place-items-center rounded-pill font-mono text-[11px] {i < step ? 'bg-run text-white' : i === step ? 'bg-action text-white' : 'border border-rule'}">{#if i < step}<Icon name="check" size={12} />{:else}{i + 1}{/if}</span>
							{s}
						</li>
					{/each}
				</ol>

				<section class="card card-glow p-6 sm:p-8" aria-live="polite">
					<form onsubmit={next} class="grid gap-5">
						{#if step === 0}
							<div>
								<p class="eyebrow">Welcome</p>
								<h1 class="mt-2 text-page">Let's set up your panel<span class="text-action">.</span></h1>
								<p class="mt-2 max-w-prose text-muted">A few minutes: an administrator account, the address people use to reach this panel, and (optionally) GitHub and Discord sign-in and an AI operator provider. You can change everything later.</p>
							</div>
							<label class="block max-w-md">
								<span class="label">Setup code</span>
								<input class="field text-center font-mono text-title tracking-[0.2em] uppercase" autocomplete="off" spellcheck="false" placeholder="XXXX-XXXX-XXXX-XXXX" required bind:value={code} />
								<span class="help">It proves you run this server. Find it in the panel's log (<code>docker logs rivetpanel</code> or <code>journalctl -u rivetpanel</code>){#if codeFile}, or in <code>{codeFile}</code>{/if}.</span>
							</label>
						{:else if step === 1}
							<div>
								<p class="eyebrow">Step 2</p>
								<h2 class="mt-2 text-section">The administrator account</h2>
								<p class="mt-1 text-muted">Administrators manage users, the host and these settings. You can add more people later.</p>
							</div>
							<label class="block max-w-md"><span class="label">Email</span><input class="field" type="email" autocomplete="username" required bind:value={email} /></label>
							<div class="grid max-w-2xl gap-3 sm:grid-cols-2">
								<label class="block"><span class="label">Password</span><input class="field" type="password" autocomplete="new-password" minlength="12" required bind:value={password} /><span class="help">At least 12 characters.</span></label>
								<label class="block"><span class="label">Repeat the password</span><input class="field" type="password" autocomplete="new-password" required bind:value={confirm} /></label>
							</div>
						{:else if step === 2}
							<div>
								<p class="eyebrow">Step 3</p>
								<h2 class="mt-2 text-section">Where people reach this panel</h2>
								<p class="mt-1 max-w-prose text-muted">The address in the browser bar, without a path. GitHub and Discord send people back to it after they sign in, and bots use it to send statistics.</p>
							</div>
							<label class="block max-w-md">
								<span class="label">Panel address</span>
								<input class="field font-mono" required bind:value={publicUrl} placeholder="https://panel.example.com" />
								{#if !httpsOk}<span class="help text-warn">Use https for a public address. Plain http only works for localhost.</span>{:else}<span class="help">Detected from this browser. Change it if you use a domain.</span>{/if}
							</label>
						{:else if step === 3}
							<div class="flex flex-wrap items-start justify-between gap-3">
								<div>
									<p class="eyebrow">Step 4 · optional</p>
									<h2 class="mt-2 flex items-center gap-2 text-section"><Icon name="github" size={20} />GitHub sign-in and deployments</h2>
									<p class="mt-1 max-w-prose text-muted">Lets people sign in with GitHub and deploy bots straight from their repositories, with automatic deploys on push.</p>
								</div>
								<label class="flex items-center gap-2 font-medium"><input type="checkbox" bind:checked={useGitHub} />Set up GitHub</label>
							</div>
							{#if useGitHub}
								<div class="rounded-overlay border border-rule-soft bg-paper p-4 sm:p-5">
									<ProviderSetup provider="github" {publicUrl} bind:clientId={ghId} bind:secret={ghSecret} />
								</div>
							{/if}
						{:else if step === 4}
							<div class="flex flex-wrap items-start justify-between gap-3">
								<div>
									<p class="eyebrow">Step 5 · optional</p>
									<h2 class="mt-2 flex items-center gap-2 text-section"><Icon name="discord" size={20} />Discord sign-in and notifications</h2>
									<p class="mt-1 max-w-prose text-muted">Lets people sign in with Discord and receive crash, deployment and heartbeat alerts in a channel they choose.</p>
								</div>
								<label class="flex items-center gap-2 font-medium"><input type="checkbox" bind:checked={useDiscord} />Set up Discord</label>
							</div>
							{#if useDiscord}
								<div class="rounded-overlay border border-rule-soft bg-paper p-4 sm:p-5">
									<ProviderSetup provider="discord" {publicUrl} bind:clientId={dcId} bind:secret={dcSecret} />
								</div>
							{/if}
							{#if useGitHub || useDiscord}
								<label class="flex max-w-prose items-start gap-2.5">
									<input type="checkbox" class="mt-0.5" bind:checked={allowSignup} />
									<span>Let new people create an account by signing in with {useGitHub && useDiscord ? 'GitHub or Discord' : useGitHub ? 'GitHub' : 'Discord'}<span class="help">Off: only accounts you create can sign in. Accounts are never merged by email.</span></span>
								</label>
							{/if}
						{:else if step === 5}
							<div class="flex flex-wrap items-start justify-between gap-3">
								<div>
									<p class="eyebrow">Step 6 · optional</p>
									<h2 class="mt-2 flex items-center gap-2 text-section"><Icon name="bolt" size={20} />AI operator</h2>
									<p class="mt-1 max-w-prose text-muted">An incident assistant inside each bot and site. It needs an OpenAI-compatible provider (DeepSeek is filled in). Hosting works without it, and you can add or change providers later under Administration → Panel settings.</p>
								</div>
								<label class="flex items-center gap-2 font-medium"><input type="checkbox" bind:checked={useAI} />Set up the AI operator</label>
							</div>
							{#if useAI}
								<div class="grid max-w-2xl gap-3 rounded-overlay border border-rule-soft bg-paper p-4 sm:grid-cols-2 sm:p-5">
									<label class="block"><span class="label">Provider name</span><input class="field" maxlength="80" bind:value={aiName} /></label>
									<label class="block"><span class="label">Model ID</span><input class="field font-mono" bind:value={aiModel} /></label>
									<label class="block sm:col-span-2"><span class="label">Base API URL</span><input class="field font-mono" bind:value={aiBaseUrl} /></label>
									<label class="block sm:col-span-2"><span class="label">API key</span><input class="field" type="password" autocomplete="new-password" bind:value={aiKey} /><span class="help">Encrypted with this server's key and never shown again.</span></label>
								</div>
							{/if}
						{:else}
							<div>
								<p class="eyebrow">Ready</p>
								<h2 class="mt-2 text-section">Review and finish</h2>
							</div>
							<dl class="grid max-w-2xl grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 rounded-overlay border border-rule-soft bg-paper p-4">
								<dt class="text-muted">Administrator</dt><dd class="truncate">{email}</dd>
								<dt class="text-muted">Panel address</dt><dd class="truncate font-mono text-small">{publicUrl}</dd>
								<dt class="text-muted">GitHub</dt><dd>{useGitHub ? `Client ${ghId}` : 'Not now'}</dd>
								<dt class="text-muted">Discord</dt><dd>{useDiscord ? `Client ${dcId}` : 'Not now'}</dd>
								<dt class="text-muted">AI operator</dt><dd class="truncate">{useAI ? `${aiName} · ${aiModel}` : 'Not now'}</dd>
								<dt class="text-muted">Sign-up</dt><dd>{allowSignup && (useGitHub || useDiscord) ? 'New people may create accounts' : 'Only accounts you create'}</dd>
							</dl>
							<p class="max-w-prose text-small text-muted">Secrets are stored encrypted with this server's key. Values in the environment file always win over these.</p>
						{/if}

						{#if error}<Notice tone="fail" live>{error}</Notice>{/if}

						<div class="flex items-center gap-2 border-t border-rule-soft pt-5">
							{#if step > 0}<button type="button" class="btn" onclick={() => ((step -= 1), (error = ''))}><Icon name="chevronLeft" size={14} />Back</button>{/if}
							<span class="flex-1"></span>
							{#if step === 3 || step === 4 || step === 5}
								<button type="button" class="btn btn-quiet" onclick={() => { if (step === 3) useGitHub = false; else if (step === 4) useDiscord = false; else useAI = false; error = ''; step += 1; }}>Skip</button>
							{/if}
							{#if step < steps.length - 1}
								<button class="btn btn-primary" disabled={busy}>Continue<Icon name="chevronRight" size={14} /></button>
							{:else}
								<button type="button" class="btn btn-primary" disabled={busy} onclick={finish}>{busy ? 'Setting up…' : 'Finish setup'}</button>
							{/if}
						</div>
					</form>
				</section>
			</div>
		{/if}
	</div>
</main>
