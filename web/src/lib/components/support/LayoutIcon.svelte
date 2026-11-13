<script lang="ts">
	import type { SupportLayout } from '$lib/ui/supportLayout.svelte';

	// A drawn picture of a ticket layout: small for the Support page switcher,
	// large (`thumb`) for the cards in Settings → Appearance.
	let { layout, thumb = false }: { layout: SupportLayout; thumb?: boolean } = $props();
</script>

{#if thumb}
	<svg viewBox="0 0 120 72" class="w-full" aria-hidden="true">
		<rect x="0.5" y="0.5" width="119" height="71" rx="6" class="t-frame" />
		{#if layout === 'split'}
			<rect x="6" y="6" width="40" height="60" rx="3" class="t-pane" />
			{#each [0, 1, 2, 3] as i (i)}<rect x="10" y={11 + i * 13} width={i === 1 ? 32 : 28} height="8" rx="2" class={i === 1 ? 't-on' : 't-line'} />{/each}
			<rect x="51" y="6" width="63" height="60" rx="3" class="t-pane" />
			<rect x="56" y="11" width="40" height="5" rx="2" class="t-ink" />
			<rect x="56" y="22" width="52" height="12" rx="2" class="t-line" />
			<rect x="56" y="38" width="44" height="12" rx="2" class="t-line" />
			<rect x="56" y="55" width="52" height="6" rx="2" class="t-on" />
		{:else if layout === 'table'}
			<rect x="6" y="6" width="108" height="8" rx="2" class="t-ink" />
			{#each [0, 1, 2, 3, 4] as i (i)}
				<rect x="6" y={18 + i * 10} width="10" height="6" rx="1.5" class="t-line" />
				<rect x="20" y={18 + i * 10} width="44" height="6" rx="1.5" class={i === 2 ? 't-on' : 't-line'} />
				<rect x="68" y={18 + i * 10} width="16" height="6" rx="3" class="t-chip" />
				<circle cx="94" cy={21 + i * 10} r="3" class="t-line" />
				<rect x="102" y={18 + i * 10} width="12" height="6" rx="1.5" class="t-line" />
			{/each}
		{:else if layout === 'board'}
			{#each [0, 1, 2, 3] as c (c)}
				<rect x={6 + c * 27.5} y="6" width="24" height="60" rx="3" class="t-pane" />
				<rect x={9 + c * 27.5} y="10" width="12" height="4" rx="2" class="t-ink" />
				{#each Array.from({ length: [3, 2, 2, 1][c] }) as _, i (i)}<rect x={9 + c * 27.5} y={18 + i * 15} width="18" height="12" rx="2" class={c === 0 && i === 0 ? 't-on' : 't-line'} />{/each}
			{/each}
		{:else}
			{#each [0, 1, 2, 3, 4] as i (i)}
				<rect x="6" y={7 + i * 12.5} width="108" height="10" rx="2" class="t-pane" />
				<rect x="10" y={10 + i * 12.5} width={i === 0 ? 60 : 48 - i * 4} height="4" rx="2" class="t-line" />
				<rect x="98" y={10 + i * 12.5} width="12" height="4" rx="2" class="t-chip" />
			{/each}
		{/if}
	</svg>
{:else}
	<svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" aria-hidden="true">
		{#if layout === 'split'}
			<rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1.5" /><path d="M6.25 2.75v10.5" /><path d="M3.5 5.5h1M3.5 8h1M3.5 10.5h1" stroke-linecap="round" />
		{:else if layout === 'table'}
			<rect x="1.75" y="2.75" width="12.5" height="10.5" rx="1.5" /><path d="M1.75 6h12.5M1.75 9.5h12.5M5.5 2.75v10.5" />
		{:else if layout === 'board'}
			<rect x="1.75" y="2.75" width="3.5" height="10.5" rx="1" /><rect x="6.25" y="2.75" width="3.5" height="7" rx="1" /><rect x="10.75" y="2.75" width="3.5" height="8.75" rx="1" />
		{:else}
			<path d="M2 4h12M2 8h12M2 12h12" stroke-linecap="round" />
		{/if}
	</svg>
{/if}

<style>
	.t-frame {
		fill: var(--color-paper);
		stroke: var(--color-rule);
	}
	.t-pane {
		fill: var(--color-panel);
		stroke: var(--color-rule-soft);
	}
	.t-line {
		fill: color-mix(in srgb, var(--color-ink) 14%, transparent);
	}
	.t-ink {
		fill: color-mix(in srgb, var(--color-ink) 30%, transparent);
	}
	.t-on {
		fill: color-mix(in srgb, var(--color-action) 45%, transparent);
	}
	.t-chip {
		fill: color-mix(in srgb, var(--color-run) 45%, transparent);
	}
</style>
