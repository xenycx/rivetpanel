<script lang="ts">
	import Notice from '$lib/components/ui/Notice.svelte';
	import AuthFrame from '$lib/components/AuthFrame.svelte';
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

<AuthFrame>
		<h1 class="text-page font-semibold">{phase === 'done' ? 'Email address confirmed' : 'Confirm your email address'}</h1>
		{#if phase === 'working'}
			<p class="mt-4 text-muted" role="status">Confirming…</p>
		{:else if phase === 'done'}
			<Notice tone="success" class="mt-4" live>
				{changed ? `Your account's email address is now ${email}. Sign in with it from now on.` : `${email} is verified.`}
			</Notice>
			<a class="btn btn-primary mt-6 w-full" href={session.user ? '/settings/profile' : '/login'}>{session.user ? 'Back to your profile' : 'Sign in'}</a>
		{:else if phase === 'failed'}
			<Notice tone="fail" class="mt-4" live>{error}</Notice>
			<p class="mt-4 text-muted">Sign in and ask for a new link on your profile page.</p>
			<a class="btn mt-6 w-full" href={session.user ? '/settings/profile' : '/login'}>{session.user ? 'Open your profile' : 'Sign in'}</a>
		{:else}
			<p class="mt-4 text-muted">This page confirms the link from a verification email. Open the link from the email again; it must include everything after the # sign.</p>
		{/if}
</AuthFrame>
