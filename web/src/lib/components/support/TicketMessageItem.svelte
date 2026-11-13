<script lang="ts">
	import { tick } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtWhen } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import { type TicketMessage, type ReactionKind, reactionKinds } from '$lib/api/support';
	import { support } from '$lib/supportData.svelte';
	import { untrack } from 'svelte';
	import { pulse } from '$lib/ui/glide';
	import PersonAvatar from './PersonAvatar.svelte';
	import Popover from './Popover.svelte';

	// One reply or internal note: picture, author, role, time and body
	// (Markdown, never HTML), with an action bar on hover or focus: react,
	// quote, copy a link, and for the author edit or remove; staff who manage
	// tickets may also remove. Removed messages leave a short line.
	let {
		m,
		ticketId,
		ticketKey,
		canEdit,
		canRemove,
		canReact,
		showHistory,
		isNew = false,
		pending = false,
		onedit,
		onremove,
		onreact,
		onquote
	}: {
		m: TicketMessage;
		ticketId: string;
		ticketKey: string;
		canEdit: boolean;
		canRemove: boolean;
		canReact: boolean;
		showHistory: boolean;
		isNew?: boolean;
		pending?: boolean;
		onedit: (body: string) => Promise<boolean>;
		onremove: () => void;
		onreact: (kind: ReactionKind, on: boolean) => void;
		onquote: () => void;
	} = $props();

	const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });
	const iso = (ms: number) => new Date(ms).toISOString();
	const team = $derived(m.staff && !m.person && !m.mine);
	const name = $derived(m.mine ? 'You' : (m.person?.name ?? m.author));
	const mentioned = $derived((m.mentions ?? []).map((id) => support.staff.find((s) => s.id === id)?.display_name || support.staff.find((s) => s.id === id)?.email || 'a colleague'));

	// A message removed while it is on screen fades to its "removed" line;
	// one saved after an edit flashes once.
	const wasLive = untrack(() => !m.deleted);
	let item = $state<HTMLElement>();
	function flash() {
		item?.classList.remove('flash');
		void item?.offsetWidth;
		item?.classList.add('flash');
		setTimeout(() => item?.classList.remove('flash'), 1700);
	}

	// Editing in place.
	let editing = $state(false);
	let draft = $state('');
	let saving = $state(false);
	let box = $state<HTMLTextAreaElement>();
	export async function startEdit() {
		if (!canEdit) return;
		draft = m.body;
		editing = true;
		await tick();
		box?.focus();
		box?.setSelectionRange(draft.length, draft.length);
	}
	async function save() {
		if (!draft.trim() || saving) return;
		saving = true;
		if (await onedit(draft)) {
			editing = false;
			flash();
		}
		saving = false;
	}
	function editKey(e: KeyboardEvent) {
		if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
			e.preventDefault();
			save();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			editing = false;
		}
	}

	// Earlier versions (author and staff).
	let history = $state<{ id: string; body: string; editor: string; created_at_ms: number }[] | null>(null);
	let historyError = $state('');
	async function loadHistory() {
		historyError = '';
		try {
			history = (await api<{ revisions: NonNullable<typeof history> }>('GET', `/tickets/${ticketId}/messages/${m.id}/revisions`)).revisions;
		} catch (e) {
			historyError = e instanceof ApiError ? e.message : 'The history could not be loaded.';
		}
	}

	// Runs once when a popover's content appears.
	function onOpen(_node: HTMLElement, run: () => void) {
		run();
	}

	async function copyLink() {
		const url = `${location.origin}/support/${ticketKey}#m-${m.id}`;
		try {
			await navigator.clipboard.writeText(url);
			const { toast } = await import('$lib/ui/toast.svelte');
			toast('Link to the message copied');
		} catch {
			/* clipboard refused: nothing to do */
		}
	}
	const reacted = (k: ReactionKind) => m.reactions?.find((r) => r.kind === k);
</script>

{#if m.deleted}
	<li id="m-{m.id}" class="flex items-center gap-2 px-1 text-small text-muted italic" class:just-removed={wasLive}>
		<Icon name="trash" size={13} />Message removed{#if m.deleted_by}{' '}by {m.deleted_by}{/if}{#if m.internal}<span class="rounded-pill border border-warn/30 px-1.5 text-label text-warn not-italic">internal</span>{/if}
	</li>
{:else}
	<li
		bind:this={item}
		id="m-{m.id}"
		class="msg group relative flex gap-3 rounded-card border p-3.5 {m.internal ? 'border-warn/40 bg-warn/6' : m.staff ? 'border-action/25 bg-action/4' : 'border-rule-soft bg-raised'}"
		class:msg-new={isNew || pending}
		class:opacity-60={pending}
		aria-busy={pending}
	>
		<PersonAvatar person={team ? null : (m.person ?? null)} {team} size={32} class="mt-0.5" />
		<div class="min-w-0 flex-1">
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-small">
				<span class="font-semibold">{name}</span>
				{#if m.staff && !m.internal}<span class="rounded-pill border border-action/30 px-1.5 text-label text-action">support</span>{/if}
				{#if m.internal}<span class="flex items-center gap-1 rounded-pill border border-warn/40 px-1.5 text-label text-warn"><Icon name="lock" size={10} />internal note · staff only</span>{/if}
				<span class="text-muted tabular-nums">
					{#if pending}Sending…{:else}<a class="hover:underline" href="#m-{m.id}" title={fmtWhen(m.created_at_ms)}><time datetime={iso(m.created_at_ms)}>{timeFmt.format(m.created_at_ms)}</time></a>{/if}
				</span>
				{#if m.edited_at_ms}
					{#if showHistory}
						<Popover label="Edit history" width="22rem" triggerClass="text-muted hover:text-ink hover:underline" align="start">
							{#snippet trigger()}edited{/snippet}
							{#snippet children()}
								<div class="grid gap-2 p-2" role="presentation" use:onOpen={loadHistory}>
									<p class="text-label font-semibold text-muted">Earlier versions</p>
									{#if historyError}<p class="text-small text-fail">{historyError}</p>{/if}
									{#each history ?? [] as r (r.id)}
										<div class="rounded-tile border border-rule-soft p-2">
											<p class="text-label text-muted">Replaced {fmtWhen(r.created_at_ms)}{r.editor ? ` by ${r.editor}` : ''}</p>
											<p class="mt-1 text-small break-words whitespace-pre-wrap">{r.body}</p>
										</div>
									{:else}
										{#if history}<p class="text-small text-muted">No earlier versions are kept.</p>{:else}<p class="text-small text-muted">Loading…</p>{/if}
									{/each}
								</div>
							{/snippet}
						</Popover>
					{:else}
						<span class="text-muted" title="Edited {fmtWhen(m.edited_at_ms)}">edited</span>
					{/if}
				{/if}
			</div>

			{#if editing}
				<div class="mt-2 grid gap-2">
					<label class="sr-only" for="edit-{m.id}">Edit message</label>
					<textarea id="edit-{m.id}" bind:this={box} bind:value={draft} onkeydown={editKey} class="field min-h-24" maxlength="10000"></textarea>
					<div class="flex items-center gap-2">
						<button type="button" class="btn btn-primary h-8 px-3" disabled={saving || !draft.trim()} onclick={save}>{saving ? 'Saving…' : 'Save'}</button>
						<button type="button" class="btn btn-quiet h-8" onclick={() => (editing = false)}>Cancel</button>
						<span class="hidden text-small text-muted sm:inline"><kbd class="key-hint">Ctrl</kbd> <kbd class="key-hint">Enter</kbd> saves · <kbd class="key-hint">Esc</kbd> cancels</span>
					</div>
				</div>
			{:else}
				<div class="msg-body mt-1.5 max-w-[75ch] break-words"><SafeMarkdown source={m.body} relativeLinks /></div>
			{/if}

			{#if mentioned.length}
				<p class="mt-2 flex flex-wrap items-center gap-1 text-small text-muted">Mentioned {#each mentioned as n, i (i)}<span class="mention">@{n}</span>{/each}</p>
			{/if}

			{#if m.reactions?.length}
				<div class="mt-2 flex flex-wrap gap-1" aria-label="Reactions">
					{#each reactionKinds.filter((k) => reacted(k.kind)) as k (k.kind)}
						{@const r = reacted(k.kind)!}
						<button type="button" class="react-chip" aria-pressed={r.mine} disabled={!canReact} onclick={(e) => (pulse(e.currentTarget), onreact(k.kind, !r.mine))} title="{k.label}: {r.names.join(', ') || `${r.count}`}">
							<Icon name={k.icon} size={12} /><span class="tabular-nums">{r.count}</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>

		{#if !pending && !editing}
			<div class="msg-actions" role="toolbar" aria-label="Message actions">
				{#if canReact}
					<Popover label="React" width="auto" align="end" triggerClass="msg-act">
						{#snippet trigger()}<Icon name="star" size={13} /><span class="sr-only">React</span>{/snippet}
						{#snippet children(close)}
							<div class="flex gap-0.5 p-0.5">
								{#each reactionKinds as k (k.kind)}
									<button type="button" class="react-opt" aria-pressed={!!reacted(k.kind)?.mine} title={k.label} onclick={() => (onreact(k.kind, !reacted(k.kind)?.mine), close())}>
										<Icon name={k.icon} size={15} /><span class="text-label">{k.label}</span>
									</button>
								{/each}
							</div>
						{/snippet}
					</Popover>
				{/if}
				<button type="button" class="msg-act" title="Quote in the reply" onclick={onquote}>
					<svg viewBox="0 0 16 16" width="13" height="13" fill="currentColor" aria-hidden="true"><path d="M3 9.5C3 6.6 4.6 4.6 7 4v1.6c-1.2.4-2 1.4-2.1 2.7H7V12H3V9.5Zm6 0C9 6.6 10.6 4.6 13 4v1.6c-1.2.4-2 1.4-2.1 2.7H13V12H9V9.5Z" /></svg><span class="sr-only">Quote</span>
				</button>
				<button type="button" class="msg-act" title="Copy a link to this message" onclick={copyLink}><Icon name="link" size={13} /><span class="sr-only">Copy link</span></button>
				{#if canEdit}<button type="button" class="msg-act" title="Edit" onclick={startEdit}><Icon name="pencil" size={13} /><span class="sr-only">Edit</span></button>{/if}
				{#if canRemove}<button type="button" class="msg-act danger" title="Remove" onclick={onremove}><Icon name="trash" size={13} /><span class="sr-only">Remove</span></button>{/if}
			</div>
		{/if}
	</li>
{/if}

<style>
	.msg:target,
	.msg:global(.flash) {
		animation: flash 1.6s ease-out;
	}
	@keyframes flash {
		0%,
		30% {
			box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-action) 45%, transparent);
		}
	}
	.just-removed {
		animation: removed 420ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes removed {
		from {
			opacity: 0;
			transform: scaleY(0.6);
			filter: blur(3px);
		}
	}
	/* A reaction that appears or changes count pops once. */
	.react-chip {
		animation: chip-in 260ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes chip-in {
		from {
			opacity: 0;
			transform: scale(0.7);
		}
	}
	.msg-new {
		animation: settle 380ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes settle {
		from {
			opacity: 0;
			transform: translateY(10px);
			filter: blur(2px);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.msg-new,
		.just-removed,
		.react-chip {
			animation: fade 200ms ease-out both;
		}
	}
	.msg-actions {
		position: absolute;
		top: -0.85rem;
		right: 0.75rem;
		display: flex;
		gap: 1px;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-control);
		background: var(--color-raised);
		box-shadow: var(--shadow-overlay);
		padding: 2px;
		opacity: 0;
		transform: translateY(3px);
		pointer-events: none;
		transition:
			opacity 120ms,
			transform 160ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.msg:hover .msg-actions,
	.msg:focus-within .msg-actions {
		opacity: 1;
		transform: none;
		pointer-events: auto;
	}
	/* Touch screens have no hover: the actions sit under the message. */
	@media (pointer: coarse) {
		.msg {
			flex-wrap: wrap;
		}
		.msg-actions {
			position: static;
			flex-basis: 100%;
			justify-content: flex-end;
			margin-top: -0.25rem;
			border: 0;
			background: none;
			box-shadow: none;
			opacity: 1;
			transform: none;
			pointer-events: auto;
		}
	}
	:global(.msg-act) {
		display: grid;
		place-items: center;
		width: 1.75rem;
		height: 1.75rem;
		border-radius: var(--radius-inner);
		color: var(--color-muted);
	}
	:global(.msg-act:hover),
	:global(.msg-act:focus-visible) {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 7%, transparent);
	}
	:global(.msg-act.danger:hover) {
		color: var(--color-fail);
	}
	.react-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-pill);
		padding: 0.05rem 0.5rem;
		font-size: 12px;
		color: var(--color-muted);
		background: var(--color-panel);
	}
	.react-chip[aria-pressed='true'] {
		color: var(--color-action);
		border-color: color-mix(in srgb, var(--color-action) 45%, transparent);
		background: color-mix(in srgb, var(--color-action) 9%, var(--color-panel));
	}
	.react-chip:hover:not(:disabled) {
		border-color: var(--color-rule);
	}
	.react-opt {
		display: grid;
		justify-items: center;
		gap: 0.15rem;
		min-width: 3.75rem;
		border-radius: var(--radius-control);
		padding: 0.4rem 0.4rem 0.3rem;
		color: var(--color-muted);
	}
	.react-opt:hover,
	.react-opt:focus-visible {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 6%, transparent);
	}
	.react-opt[aria-pressed='true'] {
		color: var(--color-action);
	}
	.mention {
		border-radius: var(--radius-inner);
		padding: 0 0.3rem;
		font-weight: 500;
		color: var(--color-action);
		background: color-mix(in srgb, var(--color-action) 10%, transparent);
	}
	.msg-body :global(p + p) {
		margin-top: 0.5rem;
	}
</style>
