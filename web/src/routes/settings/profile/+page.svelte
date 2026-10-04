<script lang="ts">
	import { describePermissions } from '$lib/permissions';
	import { api, ApiError } from '$lib/api/client';
	import { loadSession, session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	let name = $state(session.user?.display_name ?? '');
	let avatar = $state<string | null>(null);
	let preview = $state(session.user?.avatar_url ?? '');
	let error = $state('');
	let saving = $state(false);

	async function choose(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		if (!file) return;
		if (file.size > 8 * 1024 * 1024) { error = 'Choose an image smaller than 8 MB.'; return; }
		try {
			const bitmap = await createImageBitmap(file);
			const side = Math.min(bitmap.width, bitmap.height);
			const canvas = document.createElement('canvas');
			canvas.width = canvas.height = 128;
			canvas.getContext('2d')!.drawImage(bitmap, (bitmap.width - side) / 2, (bitmap.height - side) / 2, side, side, 0, 0, 128, 128);
			bitmap.close();
			let quality = 0.86;
			let data = canvas.toDataURL('image/jpeg', quality);
			while (Math.ceil((data.length * 3) / 4) > 64 * 1024 && quality > 0.35) data = canvas.toDataURL('image/jpeg', (quality -= 0.1));
			avatar = data.split(',')[1]; preview = data; error = '';
		} catch { error = 'That image could not be read. Try a JPEG, PNG, or WebP file.'; }
	}

	async function save(e: SubmitEvent) {
		e.preventDefault(); saving = true; error = '';
		try {
			await api('PUT', '/me/profile', { display_name: name, ...(avatar !== null ? { avatar_jpeg: avatar } : {}) });
			await loadSession(); avatar = null; preview = session.user?.avatar_url ?? preview; toast('Profile saved', 'success');
		} catch (e) { error = e instanceof ApiError ? e.message : 'The profile could not be saved.'; }
		finally { saving = false; }
	}
	function remove() { avatar = ''; preview = ''; }

	// Email address: verification and changes (the address changes only
	// after the link sent to the new one is used).
	const v = $derived(session.verification);
	let sending = $state(false);
	let changeOpen = $state(false);
	let newEmail = $state('');
	let changePw = $state('');
	let changeError = $state('');
	async function sendLink() {
		sending = true;
		try {
			await api('POST', '/me/email/verification');
			await loadSession();
			toast(`Verification link sent to ${session.user?.email}`, 'success');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The link could not be sent.', 'fail');
		} finally { sending = false; }
	}
	async function changeEmail(e: SubmitEvent) {
		e.preventDefault(); sending = true; changeError = '';
		try {
			await api('POST', '/me/email', { email: newEmail, current_password: changePw });
			await loadSession();
			toast(`Confirmation link sent to ${newEmail}`, 'success');
			changeOpen = false; newEmail = ''; changePw = '';
		} catch (err) { changeError = err instanceof ApiError ? err.message : 'The change could not be started.'; }
		finally { sending = false; }
	}

	let alertsOn = $state(session.emailAlerts);
	let newsOn = $state(session.emailNews);
	let alertsBusy = $state(false);
	async function savePref(kind: 'alerts' | 'news') {
		await Promise.resolve(); // let the bound value settle first
		alertsBusy = true;
		const want = kind === 'alerts' ? alertsOn : newsOn;
		try {
			await api('PUT', `/me/email-${kind}`, { enabled: want });
			if (kind === 'alerts') session.emailAlerts = want;
			else session.emailNews = want;
			toast(`${kind === 'alerts' ? 'Alert' : 'News'} emails turned ${want ? 'on' : 'off'}`, 'success');
		} catch (e) {
			if (kind === 'alerts') alertsOn = !want;
			else newsOn = !want;
			toast(e instanceof ApiError ? e.message : 'The setting could not be saved.', 'fail');
		} finally { alertsBusy = false; }
	}
</script>

<svelte:head><title>Profile · RivetPanel</title></svelte:head>
<SettingsSection title="Profile" description="How your account appears around this panel: in sharing lists, activity and the header. Your email remains the sign-in name.">
	{#if error}<Notice tone="fail" class="mb-4">{error}</Notice>{/if}
	<form class="card grid gap-6 p-5 @container" onsubmit={save}>
		<div class="flex flex-col gap-5 sm:flex-row sm:items-center">
			{#if preview}<img src={preview} alt="Profile preview" class="size-20 rounded-pill border border-rule object-cover" />{:else}<div class="grid size-20 place-items-center rounded-pill bg-paper-2 text-section font-semibold text-muted">{(name || session.user?.email || '?').slice(0, 2).toUpperCase()}</div>{/if}
			<div>
				<div class="flex flex-wrap gap-2">
					<label class="btn cursor-pointer"><Icon name="upload" size={14} />Choose picture<input class="sr-only" type="file" accept="image/jpeg,image/png,image/webp" onchange={choose} /></label>
					{#if preview}<button type="button" class="btn btn-quiet" onclick={remove}>Remove</button>{/if}
				</div>
				<p class="mt-2 text-small text-muted">Cropped to a square and compressed to 128 × 128 pixels before upload.</p>
			</div>
		</div>
		<div class="grid gap-4 @xl:grid-cols-2">
			<label class="block"><span class="label">Display name</span><input class="field" maxlength="64" bind:value={name} placeholder="How people should see you" /></label>
			<label class="block"><span class="label">Email</span><input class="field" disabled value={session.user?.email ?? ''} /></label>
		</div>
		<div class="flex justify-end border-t border-rule-soft pt-4"><button class="btn btn-primary" disabled={saving}>{saving ? 'Saving…' : 'Save profile'}</button></div>
	</form>
</SettingsSection>
<SettingsSection id="email" title="Email address" description="Your sign-in name. Confirming it proves it is yours; changing it takes effect only after you confirm the new address.">
	<div class="card grid gap-4 p-5">
		<div class="flex flex-wrap items-center gap-3">
			<span class="font-medium break-all">{session.user?.email}</span>
			{#if session.user?.email_verified}<span class="rounded-pill bg-run/10 px-2 py-0.5 text-small text-run">Verified</span>{:else}<span class="rounded-pill bg-warn/10 px-2 py-0.5 text-small text-warn">Not verified</span>{/if}
		</div>
		{#if v.withheld?.length}<Notice tone="warn">Until your address is verified you cannot use {describePermissions(v.withheld)}.</Notice>{/if}
		{#if v.pending_email}<p class="text-small text-muted">A link was sent to <strong>{v.pending_email}</strong>{v.pending_email !== session.user?.email ? ' to change your address' : ''}. It works once and expires after 24 hours.</p>{/if}
		{#if !v.available}<p class="text-small text-muted">This panel cannot send email, so an administrator has to verify or change your address.</p>{/if}
		<div class="flex flex-wrap gap-2">
			{#if !session.user?.email_verified}<button class="btn" disabled={sending || !v.available} onclick={sendLink}><Icon name="send" size={14} />{v.pending_email === session.user?.email ? 'Send the link again' : 'Send verification link'}</button>{/if}
			<button class="btn btn-quiet" disabled={!v.available} onclick={() => { changeOpen = !changeOpen; changeError = ''; }}>Change email address</button>
		</div>
		{#if changeOpen}
			<form class="grid gap-4 border-t border-rule-soft pt-4 @container" onsubmit={changeEmail}>
				<div class="grid gap-4 @xl:grid-cols-2">
					<label class="block"><span class="label">New email address</span><input class="field" type="email" required autocomplete="email" bind:value={newEmail} /></label>
					{#if session.hasPassword}<label class="block"><span class="label">Current password</span><input class="field" type="password" required autocomplete="current-password" bind:value={changePw} /></label>{/if}
				</div>
				{#if !session.hasPassword}<p class="text-small text-muted">Accounts without a password confirm by having signed in within the last 10 minutes.</p>{/if}
				{#if changeError}<p class="text-small text-fail" role="alert">{changeError}</p>{/if}
				<div class="flex justify-end"><button class="btn btn-primary" disabled={sending}>Send confirmation link</button></div>
			</form>
		{/if}
	</div>
</SettingsSection>
{#if session.features.mail}
	<SettingsSection title="Email" description="Bot alerts can also reach you by email at the address you sign in with.">
		<div class="card grid gap-5 p-5">
			<Switch bind:checked={alertsOn} disabled={alertsBusy} label="Email me bot and server alerts" onchange={() => savePref('alerts')}>Crashes, failed deployments and backups, and bots that stop reporting, for the bots and servers whose notifications are on (Settings → Notifications on each).</Switch>
			<Switch bind:checked={newsOn} disabled={alertsBusy} label="Email me news and announcements" onchange={() => savePref('news')}>Optional news from the administrators. Notices about policy, service and security, and security emails about your account, are always sent.</Switch>
		</div>
	</SettingsSection>
{/if}
