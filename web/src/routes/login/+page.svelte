<script lang="ts">
	import Notice from '$lib/components/ui/Notice.svelte';
	import AuthFrame from '$lib/components/AuthFrame.svelte';
	import { goto } from '$app/navigation';
	import { loadSession, login, session, takeNext, verifyMFA } from '$lib/session.svelte';
	import { passkeySecondStep, passkeySignIn, passkeysSupported } from '$lib/passkeys';
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { oauthErrors } from '$lib/api/types';
	import Icon from '$lib/components/ui/Icon.svelte';

	let email = $state('');
	let password = $state('');
	let error = $state('');
	let busy = $state(false);
	let providers = $state<string[]>([]);
	let sso = $state<{ slug: string; name: string }[]>([]);
	let passkeys = $state(false);
	let reachable = $state(true);
	let canReset = $state(false);
	let errorEl: HTMLParagraphElement | undefined = $state();
	// Second step: after a correct password (or a provider sign-in) for an
	// account with two-step sign-in.
	let step = $state<'password' | 'mfa'>('password');
	let code = $state('');
	let useRecovery = $state(false);
	let codeEl: HTMLInputElement | undefined = $state();
	const labels: Record<string, string> = { github: 'GitHub', discord: 'Discord' };

	onMount(async () => {
		const ecode = page.url.searchParams.get('error');
		if (ecode) error = oauthErrors[ecode] ?? 'Sign-in failed.';
		if (page.url.searchParams.get('step') === 'mfa') showMFA();
		try {
			canReset = (await api<{ available: boolean }>('GET', '/auth/password-reset')).available;
		} catch {
			/* older server: no reset link */
		}
		try {
			const r = await api<{ providers: { id: string }[]; oidc?: { slug: string; name: string }[]; passkeys?: boolean }>('GET', '/auth/providers');
			providers = r.providers.map((p) => p.id);
			sso = r.oidc ?? [];
			passkeys = !!r.passkeys && passkeysSupported();
		} catch (e) {
			if (!(e instanceof ApiError) || e.status >= 500 || e.status === 0) reachable = false;
		}
	});

	$effect(() => {
		if (session.user) goto(takeNext());
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			if ((await login(email, password)) === 'mfa') {
				password = '';
				showMFA();
				return;
			}
			// The session effect above navigates to where the user was going.
		} catch (err) {
			// Keep the email; clear only the password.
			password = '';
			error =
				err instanceof ApiError && err.status === 429
					? 'Too many attempts. Wait a minute, then try again.'
					: err instanceof ApiError && err.status === 401
						? 'The email or password is not correct.'
						: 'The panel is not reachable. Check your connection and try again.';
			await Promise.resolve();
			errorEl?.focus();
		} finally {
			busy = false;
		}
	}

	// Passkeys: the browser asks for the device's PIN or biometric. A
	// cancelled prompt is not an error worth showing.
	async function withPasskey(second: boolean) {
		busy = true;
		error = '';
		try {
			if (second) await passkeySecondStep();
			else await passkeySignIn();
			await loadSession(); // the session effect navigates
		} catch (err) {
			if (err instanceof DOMException && err.name === 'NotAllowedError') return;
			error = err instanceof ApiError && err.status < 500 ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' : 'The passkey could not be used.';
			if (err instanceof ApiError && /expired/.test(err.message)) step = 'password';
			await Promise.resolve();
			errorEl?.focus();
		} finally {
			busy = false;
		}
	}

	async function showMFA() {
		step = 'mfa';
		code = '';
		await Promise.resolve();
		codeEl?.focus();
	}

	async function submitCode(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await verifyMFA(code.trim()); // the session effect navigates
		} catch (err) {
			code = '';
			error =
				err instanceof ApiError && err.status === 429
					? 'Too many attempts. Wait a minute, then try again.'
					: err instanceof ApiError && err.status < 500
						? err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.'
						: 'The panel is not reachable. Check your connection and try again.';
			if (err instanceof ApiError && /expired/.test(err.message)) step = 'password';
			await Promise.resolve();
			errorEl?.focus();
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Sign in · RivetPanel</title></svelte:head>

<AuthFrame>
		{#if step === 'mfa'}<p class="eyebrow">Step 2 of 2</p>{/if}
		<h1 class="text-page font-semibold">{step === 'mfa' ? 'Two-step verification' : 'Sign in'}</h1>
		<p class="mt-1 text-muted">{step === 'mfa' ? (useRecovery ? 'Enter one of your recovery codes. Each works once.' : 'Enter the 6-digit code from your authenticator app.') : 'Run Discord bots and Minecraft servers on this machine.'}</p>

		{#if !reachable}<Notice tone="warn" class="mt-4">The panel does not respond right now. It may be restarting; try again in a minute.</Notice>{/if}
		{#if error}<p bind:this={errorEl} tabindex="-1" class="mt-4 rounded-tile border border-fail/30 bg-fail/7 px-3.5 py-2.5 text-fail outline-none" role="alert">{error}</p>{/if}

		{#if step === 'mfa'}
			<form class="mt-6 space-y-4" onsubmit={submitCode}>
				<label class="block">
					<span class="label">{useRecovery ? 'Recovery code' : 'Code'}</span>
					{#if useRecovery}
						<input bind:this={codeEl} class="field font-mono" autocomplete="off" spellcheck="false" placeholder="xxxx-xxxx-xxxx" required bind:value={code} />
					{:else}
						<input bind:this={codeEl} class="field text-center font-mono text-section tracking-[0.4em]" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9 ]*" maxlength="7" placeholder="000000" required bind:value={code} />
					{/if}
				</label>
				<button class="btn btn-primary w-full" disabled={busy}>{busy ? 'Checking…' : 'Verify and sign in'}</button>
			</form>
			{#if passkeys}<button class="btn mt-2 w-full" disabled={busy} onclick={() => withPasskey(true)}><Icon name="key" />Use a passkey instead</button>{/if}
			<div class="mt-4 flex flex-wrap justify-between gap-2 text-small">
				<button class="link" onclick={() => { useRecovery = !useRecovery; code = ''; codeEl?.focus(); }}>{useRecovery ? 'Use the authenticator app' : 'Use a recovery code'}</button>
				<button class="link" onclick={() => { step = 'password'; error = ''; }}>Start over</button>
			</div>
		{:else}
		{#if providers.length || sso.length || passkeys}
			<div class="mt-6 grid gap-2">
				{#if passkeys}<button class="btn w-full" disabled={busy} onclick={() => withPasskey(false)}><Icon name="key" />Sign in with a passkey</button>{/if}
				<!-- Full navigation: the provider redirect must not go through fetch. -->
				{#each sso as p (p.slug)}
					<a class="btn w-full" href={`/api/v1/auth/oidc/${p.slug}/login`} data-sveltekit-reload><Icon name="key" />Continue with {p.name}</a>
				{/each}
				{#each providers as p (p)}
					<a class="btn w-full" href={`/api/v1/auth/${p}/login`} data-sveltekit-reload><Icon name={p as 'github'} />Continue with {labels[p] ?? p}</a>
				{/each}
			</div>
			<div class="my-5 flex items-center gap-3 text-small text-muted"><hr class="flex-1 border-rule" />or with a password<hr class="flex-1 border-rule" /></div>
		{/if}

		<form class="{providers.length || sso.length || passkeys ? '' : 'mt-6'} space-y-4" onsubmit={submit}>
			<label class="block">
				<span class="label">Email</span>
				<input class="field" type="email" autocomplete="username" required bind:value={email} />
			</label>
			<label class="block">
				<span class="label">Password</span>
				<input class="field" type="password" autocomplete="current-password" required bind:value={password} />
				{#if canReset}<a class="help link" href="/reset">Forgot your password?</a>{/if}
			</label>
			<button class="btn btn-primary w-full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
		</form>
		{/if}
		<p class="mt-6 border-t border-rule-soft pt-4 text-small text-muted">No account? Ask the administrator of this panel to add you. <a class="link" href="/">What is RivetPanel?</a></p>
</AuthFrame>
