<script lang="ts">
	import { tick, type Snippet } from 'svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import { format, shortcut, continueList, type FormatAction } from '$lib/support/markdown';
	import { type Uploads, dragHasFiles, filesOf } from '$lib/support/uploads.svelte';
	import AttachmentTray from './AttachmentTray.svelte';

	// A Markdown text box with a formatting bar, a preview, and files: drop
	// them anywhere on the box, paste a screenshot, or pick them with the
	// paperclip. Formatting keys: Ctrl/⌘ B, I, E (code), K (link),
	// Shift+7/8 (lists), Shift+. (quote). Enter continues a list.
	let {
		value = $bindable(''),
		textarea = $bindable(),
		id,
		label,
		placeholder = '',
		maxlength = 10000,
		required = false,
		uploads = null,
		bare = false,
		autocomplete,
		inputClass = '',
		onkeydown,
		oninput,
		onclick,
		children
	}: {
		value?: string;
		textarea?: HTMLTextAreaElement;
		id: string;
		/** The accessible name of the text box. */
		label: string;
		placeholder?: string;
		maxlength?: number;
		required?: boolean;
		/** Where dropped, pasted and picked files go; null hides the paperclip. */
		uploads?: Uploads | null;
		/** No border of its own (inside another box, like the reply composer). */
		bare?: boolean;
		autocomplete?: 'list';
		inputClass?: string;
		onkeydown?: (e: KeyboardEvent) => void;
		oninput?: (e: Event) => void;
		onclick?: (e: MouseEvent) => void;
		/** Overlays anchored to the box (a mention list). */
		children?: Snippet;
	} = $props();

	let mode = $state<'write' | 'preview'>('write');
	let picker = $state<HTMLInputElement>();

	const tools: ({ action: FormatAction; icon: IconName; label: string; keys: string } | null)[] = [
		{ action: 'bold', icon: 'bold', label: 'Bold', keys: 'Ctrl+B' },
		{ action: 'italic', icon: 'italic', label: 'Italic', keys: 'Ctrl+I' },
		{ action: 'code', icon: 'code', label: 'Code', keys: 'Ctrl+E' },
		{ action: 'link', icon: 'link', label: 'Link', keys: 'Ctrl+K' },
		null,
		{ action: 'quote', icon: 'quote', label: 'Quote', keys: 'Ctrl+Shift+.' },
		{ action: 'bullets', icon: 'list', label: 'Bulleted list', keys: 'Ctrl+Shift+8' },
		{ action: 'numbers', icon: 'listOrdered', label: 'Numbered list', keys: 'Ctrl+Shift+7' },
		{ action: 'codeBlock', icon: 'codeBlock', label: 'Code block (logs, errors)', keys: '' }
	];
	const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
	const keyText = (k: string) => (mac ? k.replace('Ctrl', '⌘') : k);

	async function apply(action: FormatAction) {
		if (mode !== 'write') mode = 'write';
		await tick();
		const el = textarea;
		if (!el) return;
		const r = format(action, value, el.selectionStart, el.selectionEnd);
		value = r.value;
		await tick();
		el.focus();
		el.setSelectionRange(r.start, r.end);
		el.dispatchEvent(new Event('input', { bubbles: true }));
	}

	async function key(e: KeyboardEvent) {
		onkeydown?.(e);
		if (e.defaultPrevented || e.isComposing) return;
		const a = shortcut(e);
		if (a) {
			e.preventDefault();
			apply(a);
			return;
		}
		if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && textarea) {
			const r = continueList(value, textarea.selectionStart, textarea.selectionEnd);
			if (!r) return;
			e.preventDefault();
			value = r.value;
			await tick();
			textarea.setSelectionRange(r.start, r.end);
		}
	}

	function paste(e: ClipboardEvent) {
		const files = filesOf(e.clipboardData);
		if (!uploads || !files.length) return;
		e.preventDefault();
		uploads.add(files);
	}

	// Drag and drop: the whole box lights up while files hover over it.
	let depth = $state(0);
	const dragging = $derived(depth > 0);
	function enter(e: DragEvent) {
		if (!uploads || !dragHasFiles(e)) return;
		e.preventDefault();
		depth++;
	}
	function over(e: DragEvent) {
		if (!uploads || !dragHasFiles(e)) return;
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
	}
	function leave(e: DragEvent) {
		if (!uploads || !dragHasFiles(e)) return;
		depth = Math.max(0, depth - 1);
	}
	function drop(e: DragEvent) {
		if (!uploads || !dragHasFiles(e)) return;
		e.preventDefault();
		depth = 0;
		uploads.add(filesOf(e.dataTransfer));
		textarea?.focus();
	}
	function picked(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (uploads && input.files?.length) uploads.add(input.files);
		input.value = '';
		textarea?.focus();
	}

	/** Focus the text box (switching back from the preview). */
	export async function focus() {
		mode = 'write';
		await tick();
		textarea?.focus();
	}
</script>

<div class="md-editor" class:bare data-drag={dragging} role="group" aria-label={label} ondragenter={enter} ondragover={over} ondragleave={leave} ondrop={drop}>
	<div class="md-bar" role="toolbar" aria-label="Formatting">
		<div class="md-tabs" role="tablist" aria-label="Mode">
			<button type="button" role="tab" class="md-tab" aria-selected={mode === 'write'} onclick={() => focus()}>Write</button>
			<button type="button" role="tab" class="md-tab" aria-selected={mode === 'preview'} onclick={() => (mode = 'preview')}>Preview</button>
		</div>
		<span class="md-sep" aria-hidden="true"></span>
		{#each tools as t, i (i)}
			{#if t}
				<button
					type="button"
					class="md-tool"
					title="{t.label}{t.keys ? ` (${keyText(t.keys)})` : ''}"
					aria-label={t.label}
					aria-keyshortcuts={t.keys ? t.keys.replace('Ctrl', 'Control') : undefined}
					disabled={mode !== 'write'}
					onmousedown={(e) => e.preventDefault()}
					onclick={() => apply(t.action)}><Icon name={t.icon} size={14} /></button
				>
			{:else}
				<span class="md-sep" aria-hidden="true"></span>
			{/if}
		{/each}
		{#if uploads}
			<span class="flex-1"></span>
			<button type="button" class="md-tool md-attach" title="Attach files: pictures, logs, PDF, archives (or drop or paste them)" onclick={() => picker?.click()}>
				<Icon name="paperclip" size={14} /><span class="md-attach-text">Attach</span>
			</button>
			<input bind:this={picker} type="file" multiple class="sr-only" tabindex="-1" aria-hidden="true" onchange={picked} accept="image/png,image/jpeg,image/gif,image/webp,application/pdf,text/*,.log,.txt,.json,.yaml,.yml,.toml,.csv,.md,.zip,.gz,.tgz,video/mp4,video/webm" />
		{/if}
	</div>

	<div class="md-body relative">
		{#if mode === 'preview'}
			<div class="md-preview {inputClass}" aria-live="polite">
				{#if value.trim()}<SafeMarkdown source={value} relativeLinks />{:else}<p class="text-muted">Nothing to preview yet.</p>{/if}
			</div>
		{:else}
			<label class="sr-only" for={id}>{label}</label>
			<textarea
				{id}
				bind:this={textarea}
				bind:value
				{placeholder}
				{maxlength}
				{required}
				aria-autocomplete={autocomplete}
				class="md-input block w-full resize-none bg-transparent outline-none {inputClass}"
				onkeydown={key}
				onpaste={paste}
				{oninput}
				{onclick}
			></textarea>
		{/if}
		{@render children?.()}
	</div>

	{#if uploads?.count}<AttachmentTray {uploads} />{/if}

	{#if dragging}
		<div class="md-drop" aria-hidden="true">
			<Icon name="upload" size={20} />
			<span>Drop to attach</span>
		</div>
	{/if}
</div>

<style>
	.md-editor {
		position: relative;
		border-radius: var(--radius-tile);
	}
	.md-editor:not(.bare) {
		border: 1px solid var(--color-rule);
		background: var(--color-panel);
		transition:
			border-color 160ms,
			box-shadow 160ms;
	}
	.md-editor:not(.bare):focus-within {
		border-color: color-mix(in srgb, var(--color-action) 55%, var(--color-rule));
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-action) 14%, transparent);
	}
	.md-bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 1px;
		padding: 0.3rem 0.4rem;
		border-bottom: 1px solid var(--color-rule-soft);
	}
	.md-tabs {
		display: flex;
		gap: 1px;
	}
	.md-tab {
		height: 1.75rem;
		border-radius: var(--radius-inner);
		padding: 0 0.55rem;
		font-size: 12.5px;
		font-weight: 500;
		color: var(--color-muted);
	}
	.md-tab[aria-selected='true'] {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 7%, transparent);
	}
	.md-tab:hover {
		color: var(--color-ink);
	}
	.md-sep {
		width: 1px;
		height: 1rem;
		margin: 0 0.3rem;
		background: var(--color-rule-soft);
	}
	.md-tool {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 0.3rem;
		min-width: 1.75rem;
		height: 1.75rem;
		border-radius: var(--radius-inner);
		color: var(--color-muted);
		transition:
			color 100ms,
			background-color 100ms;
	}
	.md-tool:hover:not(:disabled),
	.md-tool:focus-visible {
		color: var(--color-ink);
		background: color-mix(in srgb, var(--color-ink) 7%, transparent);
	}
	.md-tool:disabled {
		opacity: 0.4;
	}
	.md-attach {
		padding: 0 0.5rem;
		font-size: 12.5px;
		font-weight: 500;
	}
	@media (max-width: 30rem) {
		.md-attach-text {
			display: none;
		}
	}
	.md-input {
		field-sizing: content;
		min-height: 9rem;
		max-height: 28rem;
		padding: 0.6rem 0.75rem;
		line-height: 1.55;
	}
	.md-input::placeholder {
		color: var(--color-muted);
	}
	.md-input::selection {
		background: color-mix(in srgb, var(--color-action) 24%, transparent);
	}
	.md-preview {
		min-height: 9rem;
		max-height: 28rem;
		overflow-y: auto;
		padding: 0.6rem 0.75rem;
	}
	/* Files hovering over the box: a dashed outline and a quiet veil. */
	.md-drop {
		position: absolute;
		inset: 0;
		z-index: 15;
		display: grid;
		place-content: center;
		justify-items: center;
		gap: 0.35rem;
		border: 2px dashed color-mix(in srgb, var(--color-action) 70%, transparent);
		border-radius: inherit;
		background: color-mix(in srgb, var(--color-action) 9%, color-mix(in srgb, var(--color-panel) 88%, transparent));
		color: var(--color-action);
		font-weight: 600;
		font-size: 13px;
		pointer-events: none;
		animation: drop-in 160ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	@keyframes drop-in {
		from {
			opacity: 0;
			transform: scale(0.985);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.md-drop {
			animation: none;
		}
	}
</style>
