<script lang="ts">
	import { page } from '$app/state';
	import Icon, { type IconName } from './Icon.svelte';

	// Section navigation beside a settings-style page. On wide screens it stays
	// in view while the page scrolls; on narrow ones it becomes a scrollable row.
	let { links, label }: { links: { href: string; label: string; icon: IconName; match?: string }[]; label: string } = $props();
	const active = (l: { href: string; match?: string }) => page.url.pathname === l.href || (!!l.match && page.url.pathname.startsWith(l.match));
</script>

<nav class="-mx-4 min-w-0 overflow-x-auto px-4 md:sticky md:top-24 md:mx-0 md:w-52 md:shrink-0 md:self-start md:overflow-visible md:px-0" aria-label={label}>
	<ul class="flex w-max min-w-full gap-1 border-b border-rule-soft pb-2 md:w-auto md:flex-col md:border-b-0 md:pb-0">
		{#each links as l (l.href)}
			{@const on = active(l)}
			<li class="shrink-0">
				<a
					href={l.href}
					class="flex items-center gap-2.5 rounded-control px-2.5 py-1.5 whitespace-nowrap transition-colors {on ? 'bg-ink/7 font-medium text-ink' : 'text-ink/75 hover:bg-panel hover:text-ink'}"
					aria-current={on ? 'page' : undefined}><Icon name={l.icon} class={on ? 'text-action' : 'text-muted'} />{l.label}</a
				>
			</li>
		{/each}
	</ul>
</nav>
