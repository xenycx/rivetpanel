<script lang="ts">
	import Dialog from './Dialog.svelte';
	import { dialogs, next } from '$lib/ui/dialogs.svelte';

	let open = $state(false);
	let checked = $state(false);
	let text = $state('');
	let typed = $state('');
	let problem = $state('');
	let settled = false;

	const req = $derived(dialogs.current);

	$effect(() => {
		const r = dialogs.current;
		if (!r) return;
		settled = false;
		problem = '';
		typed = '';
		checked = r.kind === 'confirm' ? (r.o.checkbox?.checked ?? false) : false;
		text = r.kind === 'prompt' ? (r.o.value ?? '') : '';
		open = true;
	});

	function finish(apply: () => void) {
		if (settled) return;
		settled = true;
		apply();
		open = false;
	}
	function closed() {
		// Escape, backdrop and the close button all mean "cancel".
		const r = dialogs.current;
		if (r && !settled) {
			settled = true;
			if (r.kind === 'confirm') r.resolve({ ok: false, checked: false });
			else r.resolve(null);
		}
		// Let the closing dialog restore focus before the next one opens.
		setTimeout(next, 0);
	}
	function submitPrompt(e: SubmitEvent) {
		e.preventDefault();
		if (req?.kind !== 'prompt') return;
		const v = text.trim();
		const err = v === '' ? `${req.o.label} is required.` : (req.o.validate?.(v) ?? null);
		if (err) {
			problem = err;
			return;
		}
		finish(() => req.resolve(v));
	}
	const preview = $derived(req?.kind === 'prompt' && !problem && text.trim() ? (req.o.preview?.(text.trim()) ?? null) : null);
	// Selects a file name's stem once the dialog has focused the field.
	function selectStem(el: HTMLInputElement) {
		const r = dialogs.current;
		if (r?.kind !== 'prompt' || r.o.selectTo === undefined) return;
		const n = r.o.selectTo;
		requestAnimationFrame(() => {
			if (document.activeElement === el) el.setSelectionRange(0, n);
		});
	}
	const paragraphs = (s?: string) => (s ? s.split(/\n{1,}/).filter(Boolean) : []);
</script>

{#if req}
	<Dialog bind:open title={req.o.title} size="sm" onclose={closed} stackFooter={req.kind === 'choice' && req.o.choices.length > 2}>
		{#each paragraphs(req.o.body) as p, i (i)}<p class="mb-2 text-ink/90">{p}</p>{/each}
		{#if req.o.details?.length}
			<dl class="mb-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 border-y border-rule-soft py-2">
				{#each req.o.details as [k, v] (k)}<dt class="text-muted">{k}</dt><dd class="min-w-0 break-words">{v}</dd>{/each}
			</dl>
		{/if}
		{#if req.kind === 'confirm' && req.o.checkbox}
			<label class="mt-3 flex items-start gap-2.5">
				<input type="checkbox" class="mt-0.5" bind:checked />
				<span>{req.o.checkbox.label}{#if req.o.checkbox.hint}<span class="help">{req.o.checkbox.hint}</span>{/if}</span>
			</label>
		{/if}
		{#if req.kind === 'confirm' && req.o.typeToConfirm}
			<label class="mt-3 block">
				<span class="label">Type <code class="rounded-control bg-paper px-1">{req.o.typeToConfirm}</code> to confirm</span>
				<input class="field" bind:value={typed} autocomplete="off" spellcheck="false" />
			</label>
		{/if}
		{#if req.kind === 'prompt'}
			<form id="prompt-form" onsubmit={submitPrompt} novalidate>
				<label class="block">
					<span class="label">{req.o.label}</span>
					<input
						class="field {req.o.mono ? 'font-mono' : ''}"
						bind:value={text}
						oninput={() => (problem = '')}
						{@attach selectStem}
						placeholder={req.o.placeholder}
						aria-invalid={problem ? 'true' : undefined}
						aria-describedby={problem ? 'prompt-err' : req.o.preview ? 'prompt-preview' : undefined}
						autocomplete="off"
						spellcheck="false"
					/>
				</label>
				{#if problem}<p id="prompt-err" class="mt-1 text-small text-fail">{problem}</p>
				{:else if req.o.preview}<p id="prompt-preview" class="mt-1 min-h-[1lh] text-small text-muted" aria-live="polite">{preview ?? ''}</p>{/if}
			</form>
		{/if}
		{#snippet footer()}
			{#if req.kind === 'choice'}
				<button class="btn" onclick={() => finish(() => req.resolve(null))}>{req.o.cancelLabel ?? 'Cancel'}</button>
				<!-- Stacked footers are column-reversed: reverse so choices read top to bottom as listed. -->
				{#each req.o.choices.length > 2 ? [...req.o.choices].reverse() : req.o.choices as c (c.value)}
					<button
						class="btn {c.tone === 'primary' ? 'btn-primary' : c.tone === 'danger' ? 'btn-danger' : ''}"
						onclick={() => finish(() => req.resolve(c.value))}>{c.label}</button
					>
				{/each}
			{:else if req.kind === 'prompt'}
				<button class="btn" onclick={() => finish(() => req.resolve(null))}>Cancel</button>
				<button class="btn btn-primary" type="submit" form="prompt-form">{req.o.confirmLabel}</button>
			{:else}
				<button class="btn" onclick={() => finish(() => req.resolve({ ok: false, checked: false }))}>{req.o.cancelLabel ?? 'Cancel'}</button>
				<button
					class="btn {req.o.tone === 'danger' ? 'btn-danger-solid' : 'btn-primary'}"
					disabled={!!req.o.typeToConfirm && typed.trim() !== req.o.typeToConfirm}
					onclick={() => finish(() => req.resolve({ ok: true, checked }))}>{req.o.confirmLabel}</button
				>
			{/if}
		{/snippet}
	</Dialog>
{/if}
