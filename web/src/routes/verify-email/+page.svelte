<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { loadSession, session } from '$lib/session.svelte';

	// The link carries its token in the URL fragment, so it never reaches the
	// server's logs. It is read once, removed from the address bar and sent
	// in a request body.
	let phase = $state<'working' | 'done' | 'failed' | 'missing'>('working');
	let email = $state('');
	let changed = $state(false);
	let error = $state('');

	onMount(async () => {
		const token = location.hash.slice(1);
		history.replaceState(null, '', '/verify-email');
		if (!token) {
			phase = 'missing';
			return;
		}
		try {
			const r = await api<{ email: string; changed: boolean }>('POST', '/auth/email/verify', { token });
			email = r.email;
			changed = r.changed;
			phase = 'done';
			if (session.user) await loadSession();
		} catch (err) {
			error = err instanceof ApiError && err.status > 0 && err.status < 500 ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + '.' : 'The panel is not reachable. Check your connection and open the link again.';
			phase = 'failed';
		}
	});
</script>

<svelte:head><title>Verify email · RivetPanel</title></svelte:head>

<main class="grid min-h-dvh place-items-center px-4 py-10">
	<div class="w-full max-w-sm">
		<a href="/" class="flex items-center gap-2 text-title font-semibold tracking-tight"><img src="/favicon.svg" alt="" width="28" height="28" class="rounded-tile" />RivetPanel</a>
		<h1 class="mt-8 text-page">{phase === 'done' ? 'Email address confirmed' : 'Confirm your email address'}<span class="text-action">.</span></h1>
		{#if phase === 'working'}
			<p class="mt-4 text-muted" role="status">Confirming…</p>
		{:else if phase === 'done'}
			<p class="mt-4 border-l-[3px] border-action bg-panel px-3 py-2" role="status">
				{changed ? `Your account's email address is now ${email}. Sign in with it from now on.` : `${email} is verified.`}
			</p>
			<a class="btn btn-primary mt-6 w-full" href={session.user ? '/settings/profile' : '/login'}>{session.user ? 'Back to your profile' : 'Sign in'}</a>
		{:else if phase === 'failed'}
			<p class="mt-4 border-l-[3px] border-fail bg-panel px-3 py-2 text-fail" role="alert">{error}</p>
			<p class="mt-4 text-muted">Sign in and ask for a new link on your profile page.</p>
			<a class="btn mt-6 w-full" href={session.user ? '/settings/profile' : '/login'}>{session.user ? 'Open your profile' : 'Sign in'}</a>
		{:else}
			<p class="mt-4 text-muted">This page confirms the link from a verification email. Open the link from the email again; it must include everything after the # sign.</p>
		{/if}
	</div>
</main>
