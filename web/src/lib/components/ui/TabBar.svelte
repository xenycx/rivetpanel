<script lang="ts">
	import { goto } from '$app/navigation';
	import Icon, { type IconName } from './Icon.svelte';
	import Menu, { type MenuItem } from './Menu.svelte';

	// One row of section tabs that never scrolls sideways: the tabs that do not
	// fit the available width move into a "More" menu, and the current tab is
	// always kept in the row. Widths are measured from an invisible copy, so the
	// split follows the real labels, the font and the window size.
	type Tab = { id: string; label: string; icon: IconName; href: string };
	// fill: the shown tabs grow to share the whole row instead of leaving a gap on the right.
	let { tabs, current, label, fill = false }: { tabs: Tab[]; current: string; label: string; fill?: boolean } = $props();

	let row = $state<HTMLElement | null>(null);
	let probe = $state<HTMLElement | null>(null);
	let width = $state(0);
	let widths = $state<number[]>([]);
	let moreWidth = $state(76);

	$effect(() => {
		if (!row) return;
		const ro = new ResizeObserver(() => (width = row!.clientWidth));
		ro.observe(row);
		width = row.clientWidth;
		return () => ro.disconnect();
	});
	// Re-measure when the tabs (labels, permissions) change or fonts load.
	$effect(() => {
		void tabs.map((t) => t.label).join();
		if (!probe) return;
		const measure = () => {
			const els = [...probe!.querySelectorAll<HTMLElement>('[data-probe]')];
			widths = els.map((e) => Math.ceil(e.getBoundingClientRect().width));
			const more = probe!.querySelector<HTMLElement>('[data-probe-more]');
			moreWidth = more ? Math.ceil(more.getBoundingClientRect().width) : 84;
		};
		measure();
		document.fonts?.ready.then(measure);
	});

	const split = $derived.by(() => {
		const all = tabs.map((_, i) => i);
		if (!width || widths.length !== tabs.length) return { shown: all, hidden: [] as number[] };
		const total = widths.reduce((a, b) => a + b, 0);
		if (total <= width) return { shown: all, hidden: [] as number[] };
		let used = moreWidth;
		let n = 0;
		while (n < tabs.length && used + widths[n] <= width) used += widths[n++];
		let shown = all.slice(0, Math.max(n, 1));
		const at = tabs.findIndex((t) => t.id === current);
		if (at >= 0 && !shown.includes(at)) {
			// Swap the current tab in for the last one that fits.
			shown = [...shown.slice(0, Math.max(shown.length - 1, 0)), at];
			while (shown.length > 1 && shown.reduce((a, i) => a + widths[i], moreWidth) > width) shown.splice(shown.length - 2, 1);
		}
		return { shown, hidden: all.filter((i) => !shown.includes(i)) };
	});
	const overflow = $derived<MenuItem[]>(
		split.hidden.map((i) => ({ label: tabs[i].label, onselect: () => goto(tabs[i].href, { noScroll: true, keepFocus: true }) }))
	);
	const tabClass = 'relative flex shrink-0 items-center justify-center gap-1.5 px-2 py-2.5 text-[13px] leading-5 font-medium whitespace-nowrap';
</script>

<nav class="relative flex min-w-0 items-stretch overflow-hidden" aria-label={label} bind:this={row}>
	{#each split.shown as i (tabs[i].id)}
		{@const t = tabs[i]}
		<a
			href={t.href}
			data-sveltekit-noscroll
			data-sveltekit-keepfocus
			class="{tabClass} {fill ? 'grow' : ''} {current === t.id ? 'text-ink after:absolute after:inset-x-1 after:-bottom-px after:h-0.5 after:rounded-pill after:bg-action' : 'text-muted hover:text-ink'}"
			aria-current={current === t.id ? 'page' : undefined}
		>
			<Icon name={t.icon} size={14} />{t.label}
		</a>
	{/each}
	{#if split.hidden.length}
		<Menu label="More sections" items={overflow} fixed align="end" triggerClass="{tabClass} text-muted hover:text-ink focus-visible:-outline-offset-2">
			{#snippet trigger()}More<span class="rounded-pill bg-paper-2 px-1.5 text-[11px] leading-4">{split.hidden.length}</span><Icon name="chevronDown" size={13} />{/snippet}
		</Menu>
	{/if}
	<!-- Invisible copy used only to measure each tab's natural width. -->
	<div class="pointer-events-none invisible absolute top-0 left-0 flex w-max" aria-hidden="true" bind:this={probe}>
		{#each tabs as t (t.id)}<span data-probe class={tabClass}><Icon name={t.icon} size={14} />{t.label}</span>{/each}
		<span data-probe-more class={tabClass}>More<span class="rounded-pill px-1.5 text-[11px] leading-4">99</span><Icon name="chevronDown" size={13} /></span>
	</div>
</nav>
