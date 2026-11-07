<script lang="ts">
	import { goto } from '$app/navigation';
	import Icon, { type IconName } from './Icon.svelte';
	import Menu, { type MenuItem } from './Menu.svelte';

	// One row of section tabs that never scrolls sideways: the tabs that do not
	// fit the available width move into a "More" menu, and the current tab is
	// always kept in the row. Widths are measured from an invisible copy, so the
	// split follows the real labels, the font and the window size.
	// pin: always shown, at the end of the row (a settings gear); iconOnly: the
	// label is the accessible name and tooltip only.
	type Tab = { id: string; label: string; icon: IconName; href: string; pin?: boolean; iconOnly?: boolean };
	// fill: the shown tabs grow to share the whole row instead of leaving a gap on the right.
	// variant: 'line' underlines the current tab (the page's main tabs, whose
	// underline slides between them); 'pill' marks it with a soft fill, for a
	// second row of tabs inside one group.
	let { tabs, current, label, fill = false, variant = 'line' }: { tabs: Tab[]; current: string; label: string; fill?: boolean; variant?: 'line' | 'pill' } = $props();

	let row = $state<HTMLElement | null>(null);
	let probe = $state<HTMLElement | null>(null);
	let width = $state(0);
	let widths = $state<number[]>([]);
	let moreWidth = $state(76);
	// The space between items, which the measured widths do not include.
	const gap = $derived(variant === 'pill' ? 4 : 0);

	$effect(() => {
		if (!row) return;
		const ro = new ResizeObserver(() => (width = row!.clientWidth));
		ro.observe(row);
		width = row.clientWidth;
		return () => ro.disconnect();
	});
	// Re-measure when the tabs (labels, permissions) change or fonts load.
	$effect(() => {
		void tabs.map((t) => t.label + (t.iconOnly ? '1' : '')).join();
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
		const flow = tabs.map((_, i) => i).filter((i) => !tabs[i].pin);
		const pins = tabs.map((_, i) => i).filter((i) => tabs[i].pin);
		if (!width || widths.length !== tabs.length) return { shown: flow, hidden: [] as number[], pins };
		const w = (i: number) => widths[i] + gap;
		const room = width - pins.reduce((a, i) => a + w(i), 0);
		if (flow.reduce((a, i) => a + w(i), 0) <= room) return { shown: flow, hidden: [] as number[], pins };
		let used = moreWidth + gap;
		let n = 0;
		while (n < flow.length && used + w(flow[n]) <= room) used += w(flow[n++]);
		let shown = flow.slice(0, Math.max(n, 1));
		const at = tabs.findIndex((t) => t.id === current);
		if (at >= 0 && flow.includes(at) && !shown.includes(at)) {
			// Swap the current tab in for the last one that fits.
			shown = [...shown.slice(0, Math.max(shown.length - 1, 0)), at];
			while (shown.length > 1 && shown.reduce((a, i) => a + w(i), moreWidth + gap) > room) shown.splice(shown.length - 2, 1);
		}
		return { shown, hidden: flow.filter((i) => !shown.includes(i)), pins };
	});
	const overflow = $derived<MenuItem[]>(
		split.hidden.map((i) => ({ label: tabs[i].label, onselect: () => goto(tabs[i].href, { noScroll: true, keepFocus: true }) }))
	);
	const tabClass = $derived(
		variant === 'pill'
			? 'relative flex shrink-0 items-center justify-center gap-1.5 rounded-control px-2.5 py-1.5 text-[13px] leading-5 font-medium whitespace-nowrap focus-visible:-outline-offset-2'
			: 'relative flex shrink-0 items-center justify-center gap-1.5 px-2 py-2.5 text-[13px] leading-5 font-medium whitespace-nowrap focus-visible:-outline-offset-2'
	);
	const stateClass = (on: boolean) => (variant === 'pill' ? (on ? 'bg-ink/7 text-ink' : 'text-muted hover:bg-paper-2/60 hover:text-ink') : on ? 'text-ink' : 'text-muted hover:text-ink');
</script>

{#snippet tab(t: Tab, grow: boolean)}
	{@const on = current === t.id}
	<a
		href={t.href}
		data-sveltekit-noscroll
		data-sveltekit-keepfocus
		class="{tabClass} {grow ? 'grow' : ''} {stateClass(on)}"
		aria-current={on ? 'page' : undefined}
		aria-label={t.iconOnly ? t.label : undefined}
		title={t.iconOnly ? t.label : undefined}
	>
		<Icon name={t.icon} size={t.iconOnly ? 16 : 14} />{#if !t.iconOnly}{t.label}{/if}{#if on && variant === 'line'}<span class="absolute inset-x-1 -bottom-px h-0.5 rounded-pill bg-action" data-tab-ink aria-hidden="true"></span>{/if}
	</a>
{/snippet}

<nav class="relative flex min-w-0 items-stretch overflow-hidden {variant === 'pill' ? 'gap-1' : ''}" aria-label={label} bind:this={row}>
	{#each split.shown as i (tabs[i].id)}{@render tab(tabs[i], fill)}{/each}
	{#if split.hidden.length}
		<Menu label="More sections" items={overflow} fixed align="end" triggerClass="{tabClass} {stateClass(false)}">
			{#snippet trigger()}More<span class="rounded-pill bg-paper-2 px-1.5 text-[11px] leading-4">{split.hidden.length}</span><Icon name="chevronDown" size={13} />{/snippet}
		</Menu>
	{/if}
	{#if split.pins.length}
		<div class="ml-auto flex shrink-0 {variant === 'pill' ? 'gap-1' : ''}">{#each split.pins as i (tabs[i].id)}{@render tab(tabs[i], false)}{/each}</div>
	{/if}
	<!-- Invisible copy used only to measure each tab's natural width. -->
	<div class="pointer-events-none invisible absolute top-0 left-0 flex w-max" aria-hidden="true" bind:this={probe}>
		{#each tabs as t (t.id)}<span data-probe class={tabClass}><Icon name={t.icon} size={t.iconOnly ? 16 : 14} />{#if !t.iconOnly}{t.label}{/if}</span>{/each}
		<span data-probe-more class={tabClass}>More<span class="rounded-pill px-1.5 text-[11px] leading-4">99</span><Icon name="chevronDown" size={13} /></span>
	</div>
</nav>
