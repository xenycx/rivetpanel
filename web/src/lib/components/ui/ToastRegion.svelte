<script lang="ts">
	import { toasts, dismiss } from '$lib/ui/toast.svelte';
	import Icon from './Icon.svelte';

	const glyph = { success: 'check', info: 'info', warn: 'alert', fail: 'alert' } as const;
</script>

<!-- Polite announcements: completed actions never interrupt the user. Toasts
     sit on the terminal surface, so their state colours come from its palette. -->
<div
	class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-3 sm:inset-x-auto sm:right-0 sm:items-end"
	role="status"
	aria-live="polite"
>
	{#each toasts as t (t.id)}
		<div class="pointer-events-auto flex w-full max-w-sm animate-enter items-start gap-2.5 rounded-overlay border border-white/5 bg-term px-3.5 py-2.5 text-term-ink shadow-overlay sm:w-auto sm:min-w-72">
			<Icon name={glyph[t.tone]} class="mt-[3px] {t.tone === 'success' ? 'text-run' : t.tone === 'fail' ? 'text-fail' : t.tone === 'warn' ? 'text-warn' : 'text-data'}" />
			<p class="min-w-0 flex-1">{t.text}</p>
			{#if t.action}
				{#if t.action.href}
					<a class="shrink-0 font-medium text-data underline underline-offset-2" href={t.action.href} onclick={() => dismiss(t.id)}>{t.action.label}</a>
				{:else}
					<button class="shrink-0 font-medium text-data underline underline-offset-2" onclick={() => { t.action?.run?.(); dismiss(t.id); }}>{t.action.label}</button>
				{/if}
			{/if}
			<button class="-mr-1 shrink-0 rounded-control p-0.5 text-term-ink/60 hover:text-term-ink" aria-label="Dismiss" onclick={() => dismiss(t.id)}><Icon name="x" size={14} /></button>
		</div>
	{/each}
</div>
