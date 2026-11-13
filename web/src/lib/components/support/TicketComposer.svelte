<script lang="ts">
	import { tick, untrack } from 'svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { type TicketView, drafts } from '$lib/api/support';
	import { support, loadStaff, loadReplies } from '$lib/supportData.svelte';
	import PersonAvatar from './PersonAvatar.svelte';
	import Picker from './Picker.svelte';
	import SavedReplyManager from './SavedReplyManager.svelte';
	import MarkdownEditor from './MarkdownEditor.svelte';
	import { uploadsFor } from '$lib/support/uploads.svelte';

	// The reply box. Staff switch between a reply and an internal note (the
	// box turns amber); in a note, @ mentions a colleague (they are notified).
	// Staff insert saved replies with / in an empty box or the button. Drafts
	// are kept per ticket while the tab is open. Ctrl+Enter sends.
	let {
		id,
		view,
		embedded = false,
		sending = false,
		onsend,
		onescape
	}: {
		id: string;
		view: TicketView;
		embedded?: boolean;
		sending?: boolean;
		onsend: (msg: { body: string; internal: boolean; mentions: string[]; attachments: string[] }) => Promise<boolean>;
		onescape?: () => void;
	} = $props();

	const t = $derived(view.ticket);
	const saved = untrack(() => drafts.get(id));
	let reply = $state(saved?.body ?? '');
	let internal = $state(saved?.internal ?? false);
	let box = $state<HTMLTextAreaElement>();
	let editor = $state<{ focus: () => Promise<void> }>();
	// Files stay with the ticket's draft while the panel is open.
	const uploads = untrack(() => uploadsFor(id));
	const ready = $derived((reply.trim().length > 0 || uploads.ids.length > 0) && !uploads.busy);
	$effect(() => {
		const body = reply;
		const note = internal;
		untrack(() => (body.trim() ? drafts.set(id, { body, internal: note }) : drafts.delete(id)));
	});
	$effect(() => {
		if (view.manage) {
			loadStaff();
			loadReplies();
		}
	});

	/** Focus the box, as a reply or as an internal note. */
	export function compose(note = false) {
		internal = note && view.manage;
		editor?.focus();
	}
	/** Quote a message: its lines as a Markdown quote, then the caret. */
	export async function quote(author: string, body: string) {
		const q = `> **${author}** wrote:\n` + body.split('\n').map((l) => `> ${l}`).join('\n') + '\n\n';
		reply = reply.trim() ? `${reply.trimEnd()}\n\n${q}` : q;
		await tick();
		await editor?.focus();
		box?.setSelectionRange(reply.length, reply.length);
	}

	// ---- Mentions ----------------------------------------------------------
	let mentions = new Map<string, string>(); // "@Name" → account id
	let mq = $state<string | null>(null); // the word after @ at the caret
	let mi = $state(0);
	const staffList = $derived(
		support.staff.map((s) => ({ id: s.id, name: s.display_name || s.email, email: s.email, avatar_url: s.avatar_url, logo_variant: s.logo_variant }))
	);
	const mentionHits = $derived(
		mq === null ? [] : staffList.filter((s) => s.name.toLowerCase().includes(mq!.toLowerCase()) || s.email.toLowerCase().startsWith(mq!.toLowerCase())).slice(0, 6)
	);
	function caretWord() {
		if (!internal || !box) return (mq = null);
		const before = reply.slice(0, box.selectionStart);
		const m = before.match(/(?:^|\s)@([\w.\-]{0,32})$/);
		mq = m ? m[1] : null;
		mi = 0;
	}
	async function insertMention(p: { id: string; name: string }) {
		if (!box) return;
		const at = box.selectionStart;
		const before = reply.slice(0, at).replace(/@([\w.\-]{0,32})$/, '');
		const token = `@${p.name}`;
		reply = `${before}${token} ${reply.slice(at)}`;
		mentions.set(token, p.id);
		mq = null;
		await tick();
		const pos = before.length + token.length + 1;
		box.focus();
		box.setSelectionRange(pos, pos);
	}

	// ---- Saved replies -------------------------------------------------------
	let managing = $state(false);
	const replyOptions = $derived(support.replies.map((r) => ({ value: r.id, text: r.title, data: r })));
	async function insertSaved(rid: string) {
		const r = support.replies.find((x) => x.id === rid);
		if (!r) return;
		reply = reply.trim() ? `${reply.trimEnd()}\n\n${r.body}` : r.body;
		await tick();
		await editor?.focus();
	}
	let savedPicker = $state<{ show?: () => void }>();

	async function send(e?: SubmitEvent) {
		e?.preventDefault();
		const body = reply.trim();
		if (!ready || sending) return;
		const ids = internal ? [...mentions].filter(([tok]) => body.includes(tok)).map(([, id]) => id) : [];
		const note = internal;
		const files = uploads.ids;
		reply = '';
		internal = false;
		if (await onsend({ body, internal: note, mentions: ids, attachments: files })) {
			mentions = new Map();
			uploads.sent();
		} else {
			reply = body;
			internal = note;
		}
	}
	function key(e: KeyboardEvent) {
		if (mq !== null && mentionHits.length) {
			if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
				e.preventDefault();
				mi = (mi + (e.key === 'ArrowDown' ? 1 : -1) + mentionHits.length) % mentionHits.length;
				return;
			}
			if (e.key === 'Enter' || e.key === 'Tab') {
				e.preventDefault();
				insertMention(mentionHits[mi]);
				return;
			}
			if (e.key === 'Escape') {
				e.preventDefault();
				mq = null;
				return;
			}
		}
		if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
			e.preventDefault();
			send();
		} else if (e.key === '/' && !reply && view.manage && support.replies.length) {
			e.preventDefault();
			savedPicker?.show?.();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			box?.blur();
			onescape?.();
		}
	}
</script>

<form class="composer {embedded ? 'border-t border-rule-soft px-5 pt-3 pb-4' : 'sticky bottom-0 z-10 -mx-1 mt-4 px-1 pt-3 pb-4'}" data-note={internal} data-embedded={embedded} onsubmit={send}>
	<div class="composer-box relative rounded-tile border" data-idle={embedded && !reply.trim() && !uploads.count}>
		{#if view.manage}
			<div class="flex flex-wrap items-center gap-2 px-3 pt-2.5">
				<div role="group" aria-label="Write" class="seg">
					<button type="button" class="seg-item" aria-pressed={!internal} onclick={() => compose(false)}>Reply<kbd class="key-hint">R</kbd></button>
					<button type="button" class="seg-item" aria-pressed={internal} onclick={() => compose(true)}><Icon name="lock" size={12} />Internal note<kbd class="key-hint">N</kbd></button>
				</div>
				{#if internal}<span class="text-small text-warn">The requester never sees it. Type @ to mention a colleague.</span>{/if}
			</div>
		{/if}
		<MarkdownEditor
			bind:this={editor}
			bind:value={reply}
			bind:textarea={box}
			id="reply-{id}"
			label={internal ? 'Internal note' : 'Reply'}
			bare
			{uploads}
			maxlength={10000}
			autocomplete={internal ? 'list' : undefined}
			inputClass="composer-input"
			onkeydown={key}
			oninput={caretWord}
			onclick={caretWord}
			placeholder={internal ? 'Note for the support team… @ to mention' : view.manage ? `Reply to ${t.requester?.name ?? t.requester_email ?? 'the requester'}… ${support.replies.length ? '/ for a saved reply' : ''}` : 'Write a reply… drop or paste screenshots and logs'}
		>
			{#if mq !== null && mentionHits.length}
				<ul class="mention-list" role="listbox" aria-label="Mention a colleague">
					{#each mentionHits as p, i (p.id)}
						<li role="option" aria-selected={i === mi}>
							<button type="button" class="mention-opt" class:on={i === mi} onmousedown={(e) => (e.preventDefault(), insertMention(p))}>
								<PersonAvatar person={p} size={20} /><span class="truncate">{p.name}</span><span class="ml-auto truncate text-label text-muted">{p.email}</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</MarkdownEditor>
		<div class="composer-actions flex flex-wrap items-center gap-2 px-3 pb-2.5">
			<button class="btn btn-primary h-8 px-3" disabled={sending || !ready} title={uploads.busy ? 'Waiting for files to finish uploading' : undefined}><Icon name="send" size={14} />{uploads.busy ? 'Uploading…' : internal ? 'Add note' : 'Send reply'}</button>
			{#if view.manage}
				<Picker
					bind:this={savedPicker}
					label="Saved replies"
					options={replyOptions}
					onpick={([v]) => insertSaved(v)}
					width="20rem"
					empty="No saved replies yet"
					triggerClass="btn btn-quiet h-8 px-2.5 text-small"
				>
					{#snippet trigger()}<Icon name="file" size={13} />Saved replies{/snippet}
					{#snippet row(o)}
						<span class="min-w-0"><span class="block truncate font-medium">{o.text}</span><span class="block truncate text-label text-muted">{o.data?.body}</span></span>
					{/snippet}
					{#snippet footer(close)}
						<button type="button" class="pick-foot" onclick={() => (close(), (managing = true))}><Icon name="sliders" size={13} />Manage saved replies…</button>
					{/snippet}
				</Picker>
			{/if}
			<span class="ml-auto hidden text-small text-muted md:inline"><kbd class="key-hint">Ctrl</kbd> <kbd class="key-hint">Enter</kbd> sends</span>
		</div>
	</div>
</form>

{#if view.manage}<SavedReplyManager bind:open={managing} />{/if}

<style>
	.composer[data-embedded='false'] {
		background: linear-gradient(to bottom, transparent, var(--color-paper) 0.75rem);
	}
	.composer-box {
		border-color: var(--color-rule);
		background: var(--color-panel);
		transition:
			border-color 160ms,
			background-color 160ms,
			box-shadow 160ms;
	}
	.composer-box:focus-within {
		border-color: color-mix(in srgb, var(--color-action) 55%, var(--color-rule));
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-action) 14%, transparent);
	}
	.composer[data-note='true'] .composer-box {
		border-color: color-mix(in srgb, var(--color-warn) 45%, var(--color-rule));
		background: color-mix(in srgb, var(--color-warn) 7%, var(--color-panel));
	}
	.composer[data-note='true'] .composer-box:focus-within {
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-warn) 16%, transparent);
	}
	.composer-box :global(.composer-input) {
		min-height: 4.5rem;
		max-height: 16rem;
	}
	.composer-box[data-idle='true']:not(:focus-within) :global(.composer-input) {
		min-height: 2.6rem;
		height: 2.6rem;
		overflow: hidden;
	}
	.composer-box[data-idle='true']:not(:focus-within) .composer-actions,
	.composer-box[data-idle='true']:not(:focus-within) :global(.md-bar) {
		display: none;
	}
	.mention-list {
		position: absolute;
		bottom: calc(100% - 0.5rem);
		left: 0.75rem;
		z-index: 20;
		width: min(20rem, calc(100% - 1.5rem));
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-overlay);
		background: var(--color-raised);
		box-shadow: var(--shadow-overlay);
		padding: 0.25rem;
		animation: var(--animate-enter);
	}
	.mention-opt {
		display: flex;
		width: 100%;
		align-items: center;
		gap: 0.5rem;
		border-radius: var(--radius-control);
		padding: 0.3rem 0.5rem;
		font-size: 13px;
		text-align: left;
	}
	.mention-opt.on,
	.mention-opt:hover {
		background: color-mix(in srgb, var(--color-ink) 6%, transparent);
	}
</style>
