<script lang="ts">
	import { untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { OutputChunk } from '$lib/api/types';
	import Icon from '$lib/components/ui/Icon.svelte';

	// Retained build output, polled by offset while the build runs. The client
	// keeps a bounded tail, follows new output unless the reader scrolled up,
	// and can filter what it has loaded.
	let { botId, opId, height = 'h-80' }: { botId: string; opId: string; height?: string } = $props();

	const MAX_CHARS = 512 * 1024;
	let text = $state('');
	let offset = 0;
	let live = $state(true);
	let truncated = $state(false);
	let error = $state('');
	let follow = $state(true);
	let filter = $state('');
	let pre: HTMLPreElement | undefined = $state();
	let copied = $state(false);

	const base = $derived(`/bots/${botId}/operations/${opId}/output`);

	// One poll of url; nothing is applied once the loop for url was stopped
	// (another build was opened in this component).
	async function poll(url: string, stopped: () => boolean) {
		try {
			const c = await api<OutputChunk>('GET', `${url}?offset=${offset}`);
			if (stopped()) return;
			if (c.truncated) truncated = true;
			if (c.text) {
				text = (text + c.text).slice(-MAX_CHARS);
				if (follow) queueMicrotask(() => pre && (pre.scrollTop = pre.scrollHeight));
			}
			offset = c.next_offset;
			live = c.live || c.next_offset < c.total;
			error = '';
		} catch (e) {
			if (stopped()) return;
			live = false;
			error = e instanceof ApiError && e.status === 404 ? 'No output was kept for this build.' : e instanceof ApiError ? e.message : 'The output could not be loaded.';
		}
	}
	// (Re)starts polling whenever the build changes.
	$effect(() => {
		const url = base;
		let stop = false;
		untrack(() => {
			text = '';
			offset = 0;
			live = true;
			truncated = false;
			error = '';
		});
		(async () => {
			while (!stop) {
				await poll(url, () => stop);
				if (stop || !live) break;
				await new Promise((r) => setTimeout(r, document.visibilityState === 'visible' ? 1000 : 4000));
			}
		})();
		return () => (stop = true);
	});

	function onScroll() {
		if (!pre) return;
		follow = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 24;
	}
	const shown = $derived(
		filter.trim()
			? text
					.split('\n')
					.filter((l) => l.toLowerCase().includes(filter.trim().toLowerCase()))
					.join('\n')
			: text
	);
	async function copy() {
		try {
			await navigator.clipboard.writeText(text);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard unavailable */
		}
	}
</script>

<div class="border border-rule-soft">
	<div class="flex flex-wrap items-center gap-2 border-b border-rule-soft bg-panel px-2 py-1.5">
		<span class="text-small {live ? 'text-warn' : 'text-muted'}" aria-live="polite">{live ? 'Receiving output…' : error || 'Complete'}</span>
		<span class="flex-1"></span>
		<label class="relative">
			<span class="sr-only">Filter lines</span>
			<Icon name="search" size={14} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-muted" />
			<input class="field h-8 min-h-8 w-40 py-0 pl-7 text-small" type="search" placeholder="Filter lines" bind:value={filter} />
		</label>
		{#if !follow && live}<button class="btn btn-sm" onclick={() => { follow = true; pre && (pre.scrollTop = pre.scrollHeight); }}>Follow</button>{/if}
		<button class="btn btn-sm btn-quiet" onclick={copy} aria-label="Copy output"><Icon name={copied ? 'check' : 'copy'} size={14} />{copied ? 'Copied' : 'Copy'}</button>
		<a class="btn btn-sm btn-quiet" href="/api/v1{base}?download=1" download aria-label="Download output"><Icon name="download" size={14} />Download</a>
	</div>
	{#if truncated}<p class="bg-paper px-3 py-1 text-small text-muted">Earlier output was dropped; only the last part is kept.</p>{/if}
	<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
	<pre bind:this={pre} onscroll={onScroll} class="{height} overflow-auto bg-term p-3 font-mono text-[12.5px] leading-relaxed whitespace-pre-wrap break-words text-term-ink" tabindex="0" aria-label="Build output">{shown || (live ? 'Waiting for output…' : '')}</pre>
</div>
