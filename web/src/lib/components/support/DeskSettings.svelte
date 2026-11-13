<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import type { DeskSettings } from '$lib/api/support';
	import { support, loadDesk } from '$lib/supportData.svelte';

	// How the desk answers a new ticket before anyone picks it up: the usual
	// response time requesters see, and an automatic reply sent at once on
	// the team's behalf. Placeholders fill in the requester's name, the
	// ticket and the response time.
	let { open = $bindable(false) }: { open?: boolean } = $props();

	let enabled = $state(false);
	let body = $state('');
	let amount = $state(0);
	let unit = $state<'hours' | 'days'>('days');
	let error = $state('');
	let busy = $state(false);
	let box = $state<HTMLTextAreaElement>();

	$effect(() => {
		if (!open) return;
		error = '';
		loadDesk(true).then((d) => {
			enabled = d.auto_reply;
			body = d.auto_reply_body ?? '';
			const h = d.response_hours;
			if (h > 0 && h % 24 === 0) [amount, unit] = [h / 24, 'days'];
			else if (h > 0) [amount, unit] = [h, 'hours'];
			else [amount, unit] = [0, 'days'];
		});
	});

	const hours = $derived(Math.max(0, Math.round((Number(amount) || 0) * (unit === 'days' ? 24 : 1))));
	function within(h: number) {
		if (h <= 0) return 'as soon as possible';
		if (h === 1) return 'within an hour';
		if (h < 48 && h % 24 !== 0) return `within ${h} hours`;
		if (h === 24) return 'within a day';
		return `within ${Math.ceil(h / 24)} days`;
	}
	const placeholders = [
		{ token: '{name}', text: 'Requester name' },
		{ token: '{ticket}', text: 'Ticket key' },
		{ token: '{subject}', text: 'Subject' },
		{ token: '{response_time}', text: 'Response time' }
	];
	const sample = $derived(
		body
			.replaceAll('{name}', session.user?.display_name || 'Alex')
			.replaceAll('{ticket}', 'RT-000042')
			.replaceAll('{subject}', 'My bot does not start after the update')
			.replaceAll('{response_time}', within(hours))
	);
	function insert(token: string) {
		const el = box;
		if (!el) return;
		const s = el.selectionStart;
		const e = el.selectionEnd;
		body = body.slice(0, s) + token + body.slice(e);
		requestAnimationFrame(() => {
			el.focus();
			el.setSelectionRange(s + token.length, s + token.length);
		});
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			support.desk = await api<DeskSettings>('PUT', '/tickets/settings', { auto_reply: enabled, auto_reply_body: body, response_hours: hours });
			toast(enabled ? 'New tickets now get the automatic reply' : 'Support desk settings saved', 'success');
			open = false;
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The request failed.';
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open title="Automatic reply and response time" description="What requesters hear before someone on the team picks up their ticket." size="lg">
	<form id="desk-settings" class="grid gap-5" onsubmit={save}>
		<fieldset class="grid gap-2">
			<legend class="label">Usual time to a first answer</legend>
			<div class="flex flex-wrap items-center gap-2">
				<input class="field h-9 w-24 tabular-nums" type="number" min="0" max={unit === 'days' ? 60 : 1440} step="1" bind:value={amount} aria-label="Amount" />
				<select class="field h-9 w-auto" bind:value={unit} aria-label="Unit">
					<option value="hours">hours</option>
					<option value="days">days</option>
				</select>
				<span class="text-small text-muted">{hours > 0 ? `Requesters see “usually answered ${within(hours)}”.` : 'Set 0 to hide the estimate.'}</span>
			</div>
		</fieldset>

		<Switch bind:checked={enabled} label="Send an automatic reply to every new ticket">
			It arrives at once as a message from the support team. The ticket stays open and waiting for staff, so it still shows in the queue.
		</Switch>

		<div class="grid gap-2" class:dimmed={!enabled}>
			<div class="flex flex-wrap items-end justify-between gap-2">
				<label class="label !mb-0" for="desk-auto-body">Reply</label>
				<div class="flex flex-wrap gap-1" role="group" aria-label="Insert a placeholder">
					{#each placeholders as p (p.token)}
						<button type="button" class="token" title="Insert {p.text}" onclick={() => insert(p.token)}>{p.token}</button>
					{/each}
				</div>
			</div>
			<textarea id="desk-auto-body" bind:this={box} class="field min-h-32 font-[inherit]" maxlength="4000" bind:value={body} placeholder="Hi {'{name}'}, thanks for getting in touch…"></textarea>
			{#if body.trim()}
				<div class="preview" aria-label="Preview">
					<p class="mb-1.5 text-label font-medium text-muted">Preview, as a requester sees it</p>
					<SafeMarkdown source={sample} relativeLinks />
				</div>
			{/if}
		</div>
		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	</form>
	{#snippet footer()}
		<button type="button" class="btn btn-quiet" onclick={() => (open = false)}>Cancel</button>
		<button type="submit" form="desk-settings" class="btn btn-primary" disabled={busy || (enabled && !body.trim())}>{busy ? 'Saving…' : 'Save'}</button>
	{/snippet}
</Dialog>

<style>
	.dimmed {
		opacity: 0.6;
	}
	.token {
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-inner);
		padding: 0.05rem 0.4rem;
		font-family: var(--font-mono);
		font-size: 11.5px;
		color: var(--color-muted);
		background: var(--color-panel);
	}
	.token:hover,
	.token:focus-visible {
		color: var(--color-action);
		border-color: color-mix(in srgb, var(--color-action) 40%, transparent);
	}
	.preview {
		border: 1px solid color-mix(in srgb, var(--color-action) 25%, transparent);
		border-radius: var(--radius-card);
		background: color-mix(in srgb, var(--color-action) 4%, transparent);
		padding: 0.75rem 0.9rem;
	}
</style>
