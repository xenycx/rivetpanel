<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	type Status = { enabled: boolean; enabled_at_ms: number | null; recovery_left: number };
	let status = $state<Status | null>(null);
	let error = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load() {
		try {
			status = await api<Status>('GET', '/me/mfa');
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	// Enrollment: password -> key -> first code -> recovery codes.
	let open = $state(false);
	let stage = $state<'confirm' | 'scan' | 'codes'>('confirm');
	let password = $state('');
	let setup = $state<{ key: string; uri: string } | null>(null);
	let code = $state('');
	let codes = $state<string[]>([]);
	let busy = $state(false);
	let formError = $state('');

	function start() {
		stage = 'confirm';
		password = code = formError = '';
		setup = null;
		codes = [];
		open = true;
	}
	async function begin(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			setup = await api('POST', '/me/mfa/setup', { password });
			password = '';
			stage = 'scan';
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}
	async function confirmCode(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			codes = (await api<{ recovery_codes: string[] }>('POST', '/me/mfa/enable', { code: code.trim() })).recovery_codes;
			stage = 'codes';
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}

	// Turning off / new recovery codes: both need a current code.
	let actOpen = $state(false);
	let action = $state<'disable' | 'codes'>('disable');
	let actCode = $state('');
	function startAct(a: 'disable' | 'codes') {
		action = a;
		actCode = formError = '';
		actOpen = true;
	}
	async function act(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		formError = '';
		try {
			if (action === 'disable') {
				await api('POST', '/me/mfa/disable', { code: actCode.trim() });
				actOpen = false;
				toast('Two-step sign-in is off');
			} else {
				codes = (await api<{ recovery_codes: string[] }>('POST', '/me/mfa/recovery-codes', { code: actCode.trim() })).recovery_codes;
				actOpen = false;
				stage = 'codes';
				open = true;
			}
			await load();
		} catch (err) {
			formError = msg(err);
		} finally {
			busy = false;
		}
	}

	const grouped = (k: string) => k.match(/.{1,4}/g)?.join(' ') ?? k;
	function copyCodes() {
		navigator.clipboard?.writeText(codes.join('\n')).then(() => toast('Recovery codes copied', 'success'));
	}
	function downloadCodes() {
		const a = document.createElement('a');
		a.href = URL.createObjectURL(new Blob([`RivetPanel recovery codes for ${session.user?.email}\nEach code works once.\n\n${codes.join('\n')}\n`], { type: 'text/plain' }));
		a.download = 'rivetpanel-recovery-codes.txt';
		a.click();
		URL.revokeObjectURL(a.href);
	}
</script>

<SettingsSection title="Two-step sign-in" description="After your password or provider sign-in, the panel also asks for a code from an authenticator app such as 1Password, Google Authenticator or Aegis.">
	{#snippet badge()}
		{#if status?.enabled}<span class="pill" data-tone="run">On</span>{:else if status}<span class="pill">Off</span>{/if}
	{/snippet}
	<div class="card p-5">
		{#if error}<Notice tone="fail" class="mb-3">{error}</Notice>{/if}
		{#if status?.enabled}
			<div class="flex flex-wrap items-center gap-4">
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-run/12 text-run"><Icon name="shield" /></span>
				<div class="min-w-0 flex-1 basis-56">
					<p class="font-medium">On since {fmtWhen(status.enabled_at_ms ?? 0)}</p>
					<p class="text-small {status.recovery_left <= 3 ? 'text-warn' : 'text-muted'}">{status.recovery_left} of 10 recovery codes left. SFTP now needs an <a class="link" href="/settings/sftp">SFTP key</a> instead of your password.</p>
				</div>
				<div class="flex flex-wrap gap-2">
					<button class="btn" onclick={() => startAct('codes')}>New recovery codes</button>
					<button class="btn btn-danger" onclick={() => startAct('disable')}>Turn off</button>
				</div>
			</div>
		{:else if status}
			<div class="flex flex-wrap items-center gap-4">
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 text-muted"><Icon name="shield" /></span>
				<p class="min-w-0 flex-1 basis-56 text-small text-muted">Not set up. It takes about a minute and protects your bots if your password leaks.</p>
				<button class="btn btn-primary" onclick={start}><Icon name="shield" />Set up two-step sign-in</button>
			</div>
		{:else if !error}
			<Skeleton rows={1} />
		{/if}
	</div>
</SettingsSection>

<Dialog bind:open title={stage === 'codes' ? 'Save your recovery codes' : 'Set up two-step sign-in'} size="md">
	{#if stage === 'confirm'}
		<form id="mfa-begin" class="grid gap-4" onsubmit={begin}>
			{#if session.hasPassword}
				<label class="block"><span class="label">Current password</span><input class="field" type="password" autocomplete="current-password" required bind:value={password} /><span class="help">Confirms it is you before changing how you sign in.</span></label>
			{:else}
				<p>You sign in with a provider. For your protection this works within 10 minutes of signing in; if it fails, sign out and in again first.</p>
			{/if}
			{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
		</form>
	{:else if stage === 'scan' && setup}
		<form id="mfa-enable" class="grid gap-4" onsubmit={confirmCode}>
			<ol class="grid gap-3">
				<li>
					<p><span class="eyebrow mr-1.5">1</span>Add an account in your authenticator app with this key:</p>
					<p class="mt-2 rounded-control border border-rule bg-paper px-3 py-2 text-center font-mono text-title tracking-wider select-all">{grouped(setup.key)}</p>
					<p class="mt-1 text-small text-muted">Time-based, 6 digits. On a phone you can also <a class="link" href={setup.uri}>open it in the app</a>.</p>
				</li>
				<li>
					<label class="block">
						<span><span class="eyebrow mr-1.5">2</span>Enter the code it shows</span>
						<input class="field mt-2 text-center font-mono text-section tracking-[0.4em]" inputmode="numeric" autocomplete="one-time-code" maxlength="7" placeholder="000000" required bind:value={code} />
					</label>
				</li>
			</ol>
			{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
		</form>
	{:else if stage === 'codes'}
		<p>If you lose your phone, each of these codes signs you in once. Store them somewhere safe, apart from your password. They are not shown again.</p>
		<ul class="my-3 grid grid-cols-2 gap-x-6 gap-y-1 rounded-control border border-rule bg-paper px-4 py-3 font-mono">
			{#each codes as c (c)}<li>{c}</li>{/each}
		</ul>
		<div class="flex flex-wrap gap-2">
			<button class="btn btn-sm" onclick={copyCodes}><Icon name="copy" size={14} />Copy</button>
			<button class="btn btn-sm" onclick={downloadCodes}><Icon name="download" size={14} />Download</button>
		</div>
		<p class="mt-3 text-small text-muted">Lost both? An administrator with shell access can run <code>rivetpanel reset-mfa EMAIL</code>.</p>
	{/if}
	{#snippet footer()}
		{#if stage === 'codes'}
			<button class="btn btn-primary" onclick={() => (open = false)}>I saved them</button>
		{:else}
			<button class="btn" onclick={() => (open = false)}>Cancel</button>
			{#if stage === 'confirm'}<button class="btn btn-primary" type="submit" form="mfa-begin" disabled={busy}>Continue</button>{/if}
			{#if stage === 'scan'}<button class="btn btn-primary" type="submit" form="mfa-enable" disabled={busy}>Turn on</button>{/if}
		{/if}
	{/snippet}
</Dialog>

<Dialog bind:open={actOpen} title={action === 'disable' ? 'Turn off two-step sign-in?' : 'Replace your recovery codes?'} size="sm">
	<form id="mfa-act" class="grid gap-4" onsubmit={act}>
		<p>{action === 'disable' ? 'Signing in will need only your password or provider again.' : 'Your current recovery codes stop working.'}</p>
		<label class="block"><span class="label">Code from your app, or a recovery code</span><input class="field font-mono" autocomplete="one-time-code" required bind:value={actCode} /></label>
		{#if formError}<Notice tone="fail" live>{formError}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (actOpen = false)}>Cancel</button>
		<button class="btn {action === 'disable' ? 'btn-danger-solid' : 'btn-primary'}" type="submit" form="mfa-act" disabled={busy}>{action === 'disable' ? 'Turn off' : 'Replace codes'}</button>
	{/snippet}
</Dialog>
