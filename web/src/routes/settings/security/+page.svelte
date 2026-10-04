<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import TwoStep from '$lib/components/TwoStep.svelte';
	import Passkeys from '$lib/components/Passkeys.svelte';

	type Sess = { id: string; device: string; created_at_ms: number; last_seen_at_ms: number; expires_at_ms: number; current: boolean };
	let sessions = $state<Sess[] | null>(null);
	let error = $state('');
	let current = $state('');
	let next = $state('');
	let confirm = $state('');
	let pwError = $state('');
	let saving = $state(false);
	// The password form stays folded until it is wanted: most visits to this
	// page are about sessions or two-step sign-in.
	let pwOpen = $state(false);

	async function load() {
		try {
			sessions = (await api<{ sessions: Sess[] }>('GET', '/me/sessions')).sessions;
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Sessions could not be loaded.';
		}
	}
	onMount(load);

	async function changePassword(e: SubmitEvent) {
		e.preventDefault();
		pwError = '';
		if (next.length < 12) return (pwError = 'Use at least 12 characters.');
		if (next !== confirm) return (pwError = 'The new passwords do not match.');
		saving = true;
		try {
			await api('POST', '/me/password', { current_password: current, new_password: next });
			const had = session.hasPassword;
			session.hasPassword = true;
			current = next = confirm = '';
			pwOpen = false;
			toast(had ? 'Password changed. Your other sessions were signed out.' : 'Password added. Your other sessions were signed out.');
			await load();
		} catch (err) {
			pwError = err instanceof ApiError ? err.message : 'The password could not be changed.';
		} finally {
			saving = false;
		}
	}
	function cancelPassword() {
		pwOpen = false;
		current = next = confirm = pwError = '';
	}

	async function revoke(s: Sess) {
		const ok = await confirmDialog({ title: `Sign out ${s.device || 'this session'}?`, body: 'That browser has to sign in again.', confirmLabel: 'Sign out session' });
		if (!ok) return;
		try {
			await api('DELETE', `/me/sessions/${s.id}`);
			toast('Session signed out');
			await load();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The session could not be signed out.', 'fail');
		}
	}
	async function revokeOthers() {
		const ok = await confirmDialog({ title: 'Sign out everywhere else?', body: 'Every other browser signed in to your account has to sign in again. This one stays signed in.', confirmLabel: 'Sign out other sessions' });
		if (!ok) return;
		try {
			const r = await api<{ revoked: number }>('POST', '/me/sessions/revoke-others');
			toast(`${r.revoked} session${r.revoked === 1 ? '' : 's'} signed out`);
			await load();
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'Sessions could not be signed out.', 'fail');
		}
	}
	const deviceIcon = (d: string) => (/curl|python|go-http|bot|automation/i.test(d) ? 'terminal' : 'monitor');
</script>

<svelte:head><title>Security · RivetPanel</title></svelte:head>

<SettingsSection
	title="Password"
	description={session.hasPassword
		? 'Changing it signs out every other session. SFTP connections that use the password end too.'
		: 'You sign in with GitHub or Discord. A password lets you sign in without them and use SFTP. For your protection this works within 10 minutes of signing in.'}
>
	<div class="card p-5">
		{#if !pwOpen}
			<div class="flex flex-wrap items-center gap-4">
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 {session.hasPassword ? 'text-run' : 'text-muted'}"><Icon name="key" /></span>
				<div class="min-w-0 flex-1">
					<p class="font-medium">{session.hasPassword ? 'A password is set' : 'No password yet'}</p>
					<p class="text-small text-muted">{session.hasPassword ? 'Use at least 12 characters; a few unrelated words work well.' : 'Add one to sign in without a provider.'}</p>
				</div>
				<button class="btn {session.hasPassword ? '' : 'btn-primary'}" onclick={() => (pwOpen = true)}>{session.hasPassword ? 'Change password' : 'Add a password'}</button>
			</div>
		{:else}
			<form class="grid gap-4 @container" onsubmit={changePassword}>
				{#if session.hasPassword}
					<label class="block max-w-md"><span class="label">Current password</span><input class="field" type="password" autocomplete="current-password" required bind:value={current} /></label>
				{/if}
				<div class="grid gap-4 @lg:grid-cols-2">
					<label class="block"><span class="label">New password</span><input class="field" type="password" autocomplete="new-password" required minlength="12" bind:value={next} /></label>
					<label class="block"><span class="label">Repeat the new password</span><input class="field" type="password" autocomplete="new-password" required bind:value={confirm} /></label>
				</div>
				<p class="-mt-2 text-small text-muted">At least 12 characters. A few unrelated words work well.</p>
				{#if pwError}<Notice tone="fail" live>{pwError}</Notice>{/if}
				<div class="flex flex-wrap gap-2">
					<button class="btn btn-primary" disabled={saving}>{saving ? 'Saving…' : session.hasPassword ? 'Change password' : 'Add password'}</button>
					<button type="button" class="btn btn-quiet" onclick={cancelPassword}>Cancel</button>
				</div>
			</form>
		{/if}
	</div>
</SettingsSection>

{#if session.features.passkeys}<Passkeys />{/if}

{#if session.features.mfa}<TwoStep />{/if}

<SettingsSection title="Signed-in sessions" description="Browsers signed in to your account. Sign out any you do not recognize. At most 20 are kept; signing in again replaces the oldest.">
	{#if error}<Notice tone="fail" class="mb-3">{error}</Notice>{/if}
	{#if sessions === null && !error}
		<Skeleton rows={2} />
	{:else if sessions}
		<div class="card overflow-hidden">
			<ul class="divide-y divide-rule-soft">
				{#each sessions as s (s.id)}
					<li class="flex flex-wrap items-center gap-3 px-4 py-3">
						<span class="grid size-9 shrink-0 place-items-center rounded-tile bg-paper-2 {s.current ? 'text-run' : 'text-muted'}"><Icon name={deviceIcon(s.device)} /></span>
						<div class="min-w-0 flex-1 basis-56">
							<p class="truncate font-medium">{s.device || 'Unknown device'}{#if s.current}<span class="pill ml-2 align-middle" data-tone="run">This browser</span>{/if}</p>
							<p class="text-small text-muted">Active {fmtAgo(s.last_seen_at_ms)} · signed in {fmtWhen(s.created_at_ms)} · expires {fmtWhen(s.expires_at_ms)}</p>
						</div>
						{#if !s.current}<button class="btn btn-sm" onclick={() => revoke(s)}>Sign out</button>{/if}
					</li>
				{/each}
			</ul>
			{#if sessions.length > 1}
				<div class="flex justify-end border-t border-rule-soft bg-paper/40 px-4 py-2.5">
					<button class="btn btn-sm" onclick={revokeOthers}><Icon name="logout" size={14} />Sign out all other sessions</button>
				</div>
			{/if}
		</div>
	{/if}
</SettingsSection>
