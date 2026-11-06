<script lang="ts">
	import type { Bot } from '$lib/api/types';
	import { describe } from '$lib/status';

	// State as text plus a small glyph; the glyph shape (not just its color)
	// distinguishes running, in progress, failed and stopped.
	let { bot, now = Date.now(), size = 'md' }: { bot: Bot; now?: number; size?: 'sm' | 'md' } = $props();
	const s = $derived(describe(bot, now));
	const color = $derived({ run: 'text-run', warn: 'text-warn', fail: 'text-fail', idle: 'text-muted' }[s.tone]);
</script>

<span class="inline-flex items-center gap-1.5 font-medium {size === 'sm' ? 'text-small' : ''}" title={s.detail || undefined}>
	<svg width="10" height="10" viewBox="0 0 10 10" class="shrink-0 transition-colors duration-300 {color}" aria-hidden="true">
		{#if s.tone === 'run'}
			<circle cx="5" cy="5" r="4.5" fill="currentColor" />
		{:else if s.tone === 'fail'}
			<path d="M5 .6 9.6 9.2H.4z" fill="currentColor" />
		{:else if s.busy}
			<circle cx="5" cy="5" r="3.9" fill="none" stroke="currentColor" stroke-width="1.4" />
			<path class="busy-turn" d="M5 1.1A3.9 3.9 0 0 1 8.9 5H5z" fill="currentColor" />
		{:else}
			<circle cx="5" cy="5" r="3.9" fill="none" stroke="currentColor" stroke-width="1.4" />
		{/if}
	</svg>
	<span class={s.tone === 'fail' ? 'text-fail' : ''}>{s.label}</span>
</span>
