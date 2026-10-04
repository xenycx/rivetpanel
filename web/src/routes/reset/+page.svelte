<script lang="ts">
	import Notice from '$lib/components/ui/Notice.svelte';
	import AuthFrame from '$lib/components/AuthFrame.svelte';
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';

	// The reset link carries its token in the URL fragment, so it never reaches
	// the server's logs. It is read once and removed from the address bar.
	let token = $state('');
	let available = $state<boolean | null>(null);
	let email = $state('');
	let password = $state('');
	let again = $state('');
	let error = $state('');
	let busy = $state(false);
	let step = $state<'ask' | 'asked' | 'set' | 'done'>('ask');

	onMount(async () => {
		token = location.hash.slice(1);
		if (token) {
			history.replaceState(null, '', '/reset');
			step = 'set';
			available = true;
			return;
		}
		try {
			available = (await api<{ available: boolean }>('GET', '/auth/password-reset')).available;
		} catch {
			available = false;
		}
	});

	function message(err: unknown, fallback: string) {
		if (err instanceof ApiError && err.status === 429) return 'Too many attempts. Wait a few minutes, then try again.';
		if (err instanceof ApiError && err.status < 500 && err.status > 0) return err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.';
		return fallback;
	}

	async function ask(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await api('POST', '/auth/password-reset/request', { email });
			step = 'asked';
		} catch (err) {
			error = message(err, 'The panel is not reachable. Check your connection and try again.');
		} finally {
			busy = false;
		}
	}

	async function set(e: SubmitEvent) {
		e.preventDefault();
		if (password !== again) {
			error = 'The two passwords do not match.';
			return;
		}
		busy = true;
		error = '';
		try {
			await api('POST', '/auth/password-reset/confirm', { token, password });
			step = 'done';
		} catch (err) {
			error = message(err, 'The password could not be changed. Try again.');
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Reset password · RivetPanel</title></svelte:head>

<AuthFrame>
		<h1 class="text-page font-semibold">{step === 'set' ? 'Choose a new password' : step === 'done' ? 'Password changed' : 'Reset your password'}</h1>

		{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

		{#if available === false && step !== 'set'}
			<p class="mt-4 text-muted">Password reset by email is not set up on this panel. Ask its administrator to reset your password.</p>
		{:else if step === 'ask' && available}
			<p class="mt-1 text-muted">Enter your account email and we will send you a link to choose a new password.</p>
			<form class="mt-6 space-y-4" onsubmit={ask}>
				<label class="block"><span class="label">Email</span><input class="field" type="email" autocomplete="username" required bind:value={email} /></label>
				<button class="btn btn-primary w-full" disabled={busy}>{busy ? 'Sending…' : 'Send reset link'}</button>
			</form>
		{:else if step === 'asked'}
			<Notice tone="success" class="mt-4" live>If an account uses that email, a reset link is on its way. It works once and expires in an hour.</Notice>
		{:else if step === 'set'}
			<p class="mt-1 text-muted">Signing in again will be needed on every device.</p>
			<form class="mt-6 space-y-4" onsubmit={set}>
				<label class="block"><span class="label">New password</span><input class="field" type="password" required minlength="12" autocomplete="new-password" bind:value={password} /><span class="help">At least 12 characters.</span></label>
				<label class="block"><span class="label">Repeat the password</span><input class="field" type="password" required minlength="12" autocomplete="new-password" bind:value={again} /></label>
				<button class="btn btn-primary w-full" disabled={busy}>{busy ? 'Saving…' : 'Change password'}</button>
			</form>
		{:else if step === 'done'}
			<Notice tone="success" class="mt-4" live>Your password was changed and every session was signed out.</Notice>
			<a class="btn btn-primary mt-6 w-full" href="/login">Sign in</a>
		{/if}
		{#if step !== 'done'}<p class="mt-6 border-t border-rule-soft pt-4 text-small text-muted"><a class="link" href="/login">Back to sign in</a></p>{/if}
</AuthFrame>
