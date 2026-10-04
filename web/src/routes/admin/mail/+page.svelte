<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	type Result = { recipients: number; sent: number; failed: number; skipped: number; error?: string; in_panel?: number };
	const starter = `<h2>Policy update</h2>
<p>Hello,</p>
<p>We are updating our terms. The changes take effect on <strong>1 November</strong>.</p>
<ul>
  <li>What changed: …</li>
  <li>What you need to do: nothing</li>
</ul>
<p><a href="https://example.com/policy">Read the full update</a></p>`;

	let subject = $state('');
	let html = $state('');
	let kind = $state<'notice' | 'news'>('notice');
	let audience = $state<'all' | 'admins'>('all');
	let counts = $state<Record<string, number>>({});
	let busy = $state<'' | 'test' | 'send'>('');
	let error = $state('');
	let result = $state<Result | null>(null);
	let testedOk = $state(false);
	// Channels: email (needs Mailgun) and the in-panel notification inbox.
	let emailOn = $state(session.features.mail);
	let inPanel = $state(true);

	const reach = $derived(emailOn ? (counts[`${audience}_${kind}`] ?? 0) : (counts[`${audience}_notice`] ?? 0));
	const ready = $derived((emailOn && testedOk) || (!emailOn && inPanel && !!subject.trim() && !!html.trim()));
	// Scripts cannot run in the preview: the frame is sandboxed with no permissions.
	const preview = $derived(`<!doctype html><meta charset="utf-8"><base target="_blank"><body style="margin:0;background:#f4f5f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1c2330"><div style="max-width:600px;margin:0 auto;padding:16px"><div style="background:#fff;border:1px solid #e3e6ec;border-radius:10px;padding:24px;font-size:14px;line-height:1.55">${html}</div></div>`);

	onMount(async () => {
		try {
			counts = (await api<{ counts: Record<string, number> }>('GET', '/admin/mail/audience')).counts;
		} catch {
			/* counts are a convenience */
		}
	});

	$effect(() => {
		void subject; void html; void kind; void audience; void emailOn;
		testedOk = false;
	});

	function problem(e: unknown) {
		return e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) + '.' : 'The request failed. Check your connection and try again.';
	}

	async function post(test: boolean) {
		return api<Result>('POST', '/admin/mail/announcements', { subject, html, audience, kind, test, email: emailOn, in_panel: inPanel });
	}

	async function sendTest() {
		busy = 'test';
		error = '';
		result = null;
		try {
			await post(true);
			testedOk = true;
			toast(`Test sent to ${session.user?.email}`, 'success');
		} catch (e) {
			error = problem(e);
		} finally {
			busy = '';
		}
	}

	async function send() {
		const ok = await confirmDialog({
			title: `Send to ${reach} ${reach === 1 ? 'person' : 'people'}?`,
			body: emailOn ? 'This emails every account in the audience now and cannot be undone.' : 'This posts it to the notification inbox of every account in the audience.',
			details: [
				['Subject', subject],
				['Kind', kind === 'notice' ? 'Notice (sent to everyone in the audience)' : 'News (skips people who turned news off)'],
				['Audience', audience === 'all' ? 'Every enabled account' : 'Administrators only'],
				['Channels', [emailOn && 'email', inPanel && 'notification inbox'].filter(Boolean).join(' and ')]
			],
			confirmLabel: `Send to ${reach}`
		});
		if (!ok) return;
		busy = 'send';
		error = '';
		result = null;
		try {
			result = await post(false);
			toast(result.failed ? 'Sent with some failures' : 'Announcement sent', result.failed ? 'fail' : 'success');
		} catch (e) {
			error = problem(e);
		} finally {
			busy = '';
		}
	}
</script>

<svelte:head><title>Announcements · RivetPanel</title></svelte:head>

<h2 class="text-section">Announcements</h2>
<p class="mt-1 max-w-3xl text-muted">Send news or policy updates to the people with accounts on this panel, by email and/or to their notification inbox (the bell). Write the message in HTML, check it in the preview, send yourself a test email, then send it.</p>

{#if !session.features.mail}
	<Notice tone="warn" class="mt-4" title="Email is not set up">Announcements can only go to the notification inbox. To email them, add your Mailgun key, domain and sender in <a class="link" href="/admin/settings">Panel settings</a>.</Notice>
{/if}
	{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
	{#if result}
		<Notice tone={result.failed ? 'warn' : 'success'} class="mt-4" title={result.failed ? 'Sent with problems' : 'Announcement sent'} live>
			{#if result.recipients}Mailgun accepted it for {result.sent} of {result.recipients} {result.recipients === 1 ? 'person' : 'people'}{#if result.skipped}; {result.skipped} skipped because they turned news off{/if}.{/if}
			{#if result.in_panel}Posted to the notification inbox of {result.in_panel} {result.in_panel === 1 ? 'account' : 'accounts'} (unless they turned announcements off).{/if}
			{#if result.failed}{result.failed} could not be sent: {result.error}. Mailgun accepting a message does not guarantee delivery; check its logs for bounces.{/if}
		</Notice>
	{/if}

	<div class="mt-6 grid gap-6 xl:grid-cols-2">
		<section class="card flex flex-col gap-4 p-5 sm:p-6">
			<label class="block"><span class="label">Subject</span><input class="field" maxlength="150" bind:value={subject} placeholder="Update to our terms" /></label>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block">
					<span class="label">Kind</span>
					<select class="field" bind:value={kind}>
						<option value="notice">Notice: policy, service, security</option>
						<option value="news">News: optional</option>
					</select>
					<span class="help">{kind === 'notice' ? 'Goes to everyone in the audience, whatever their preferences.' : 'Skips people who turned off news emails in their profile.'}</span>
				</label>
				<label class="block">
					<span class="label">Audience</span>
					<select class="field" bind:value={audience}>
						<option value="all">Every enabled account</option>
						<option value="admins">Administrators only</option>
					</select>
					<span class="help">Reaches {reach} {reach === 1 ? 'person' : 'people'}. Each recipient sees only their own address.</span>
				</label>
			</div>
			<fieldset class="flex flex-wrap gap-x-6 gap-y-2">
				<legend class="label">Send by</legend>
				<label class="flex items-center gap-2"><input type="checkbox" bind:checked={emailOn} disabled={!session.features.mail} />Email</label>
				<label class="flex items-center gap-2"><input type="checkbox" bind:checked={inPanel} />Notification inbox (plain text)</label>
			</fieldset>
			<label class="block">
				<span class="flex items-center justify-between"><span class="label">Message (HTML)</span>{#if !html}<button type="button" class="link text-small" onclick={() => (html = starter)}>Insert an example</button>{/if}</span>
				<textarea class="field min-h-72 font-mono text-small" spellcheck="false" bind:value={html} placeholder="<h2>Title</h2><p>Your message…</p>"></textarea>
				<span class="help">Use simple HTML with inline styles; email apps ignore most CSS and all scripts. Scripts, frames, forms and event handlers are removed when sending. Keep images hosted elsewhere and under 80 KB of HTML.</span>
			</label>
			<div class="mt-auto flex flex-wrap items-center justify-end gap-2 border-t border-rule-soft pt-4">
				{#if emailOn}<button class="btn" onclick={sendTest} disabled={!!busy || !subject.trim() || !html.trim()}><Icon name="send" size={14} />{busy === 'test' ? 'Sending…' : 'Send test to me'}</button>{/if}
				<button class="btn btn-primary" onclick={send} disabled={!!busy || !ready || reach === 0 || (!emailOn && !inPanel)} title={ready || !emailOn ? '' : 'Send yourself a test of this exact message first'}>{busy === 'send' ? 'Sending…' : `Send to ${reach}`}</button>
			</div>
			{#if emailOn && !testedOk && subject.trim() && html.trim()}<p class="help text-right">Send yourself a test of this exact message to unlock sending.</p>{/if}
		</section>

		<section class="card flex flex-col p-5 sm:p-6">
			<h3 class="text-title font-semibold">Preview</h3>
			<p class="mt-1 text-small text-muted">Shown without scripts. Your email app may style it slightly differently.</p>
			{#if html.trim()}
				<iframe title="Announcement preview" class="mt-4 min-h-96 w-full flex-1 rounded-tile border border-rule bg-white" sandbox="" srcdoc={preview}></iframe>
			{:else}
				<p class="mt-4 grid min-h-48 flex-1 place-items-center rounded-tile border border-dashed border-rule text-muted">Nothing to preview yet.</p>
			{/if}
		</section>
	</div>
