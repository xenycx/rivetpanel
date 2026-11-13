<script lang="ts">
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import Inlines from './Inlines.svelte';
	import Self from './Blocks.svelte';
	import { figures } from './figures';
	import type { Block, CalloutKind } from './types';

	let { blocks, nested = false }: { blocks: Block[]; nested?: boolean } = $props();

	const CALLOUT: Record<CalloutKind, { label: string; icon: IconName }> = {
		note: { label: 'Note', icon: 'info' },
		tip: { label: 'Tip', icon: 'sparkle' },
		important: { label: 'Important', icon: 'alert' },
		warning: { label: 'Warning', icon: 'alert' },
		caution: { label: 'Caution', icon: 'shield' }
	};

	let copied = $state<number | null>(null);
	async function copy(text: string, i: number) {
		try {
			await navigator.clipboard.writeText(text);
			copied = i;
			setTimeout(() => copied === i && (copied = null), 1600);
		} catch {
			/* clipboard unavailable (insecure origin); the text is selectable */
		}
	}
</script>

{#each blocks as b, i (i)}
	{#if b.t === 'h'}
		{#if b.d === 2}
			<h2 id={b.id} class="doc-h2"><a href={'#' + b.id} class="doc-anchor"><Inlines c={b.c} /></a></h2>
		{:else if b.d === 3}
			<h3 id={b.id} class="doc-h3"><a href={'#' + b.id} class="doc-anchor"><Inlines c={b.c} /></a></h3>
		{:else}
			<h4 id={b.id} class="doc-h4"><Inlines c={b.c} /></h4>
		{/if}
	{:else if b.t === 'p'}
		<p class="doc-p"><Inlines c={b.c} /></p>
	{:else if b.t === 'ul'}
		<ul class="doc-ul">
			{#each b.items as item, j (j)}<li><Self blocks={item} nested /></li>{/each}
		</ul>
	{:else if b.t === 'ol'}
		<ol class="doc-steps" class:nested start={b.start} style={b.start ? `counter-reset: step ${b.start - 1}` : undefined}>
			{#each b.items as item, j (j)}<li><Self blocks={item} nested /></li>{/each}
		</ol>
	{:else if b.t === 'code'}
		<div class="doc-pre">
			<div class="doc-pre-bar">
				<span>{b.lang || 'text'}</span>
				<button type="button" class="doc-pre-copy" onclick={() => copy(b.text, i)} aria-label="Copy code">
					<Icon name={copied === i ? 'check' : 'copy'} size={12} />{copied === i ? 'Copied' : 'Copy'}
				</button>
			</div>
			<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable block must be reachable by keyboard) -->
			<pre tabindex="0"><code>{b.text}</code></pre>
		</div>
	{:else if b.t === 'table'}
		<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable table must be reachable by keyboard) -->
		<div class="doc-table" tabindex="0" role="region" aria-label="Table">
			<table>
				<thead><tr>{#each b.head as cell, j (j)}<th style:text-align={b.align[j] ?? undefined}><Inlines c={cell} /></th>{/each}</tr></thead>
				<tbody>
					{#each b.rows as row, r (r)}<tr>{#each row as cell, j (j)}<td style:text-align={b.align[j] ?? undefined}><Inlines c={cell} /></td>{/each}</tr>{/each}
				</tbody>
			</table>
		</div>
	{:else if b.t === 'callout'}
		<aside class="doc-callout" data-k={b.kind}>
			<p class="doc-callout-label"><Icon name={CALLOUT[b.kind].icon} size={14} />{CALLOUT[b.kind].label}</p>
			<div class="doc-callout-body"><Self blocks={b.c} nested /></div>
		</aside>
	{:else if b.t === 'quote'}
		<blockquote class="doc-quote"><Self blocks={b.c} nested /></blockquote>
	{:else if b.t === 'figure'}
		{@const F = figures[b.name]}
		{#if F}<div class="doc-figure"><F /></div>{/if}
	{:else if b.t === 'hr'}
		<hr class="doc-hr" />
	{/if}
{/each}
