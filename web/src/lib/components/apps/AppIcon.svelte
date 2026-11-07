<script lang="ts">
	import { appIcon } from '$lib/appIcons';

	// The logo of a one-click app on a soft tile; its initial when there is
	// none. Decorative: the name is always written next to it.
	let { id, name = '', size = 40, class: cls = '' }: { id: string | undefined | null; name?: string; size?: number; class?: string } = $props();
	const src = $derived(appIcon(id));
	const dark = $derived(appIcon(id, true));
	// Black logos without a light variant are inverted on dark tiles.
	const invert = $derived(id === 'herdr');
</script>

<span class="grid shrink-0 place-items-center overflow-hidden rounded-tile bg-paper-2/70 {cls}" style="width: {size}px; height: {size}px" aria-hidden="true">
	{#if src}
		{@const px = Math.round(size * 0.68)}
		<img src={src} alt="" width={px} height={px} class="object-contain {dark !== src ? 'app-icon-light' : ''} {invert ? 'app-icon-invert' : ''}" loading="lazy" decoding="async" draggable="false" />
		{#if dark !== src}<img src={dark} alt="" width={px} height={px} class="app-icon-dark object-contain" loading="lazy" decoding="async" draggable="false" />{/if}
	{:else}
		<span class="font-semibold text-action" style="font-size: {Math.round(size * 0.42)}px">{(name || id || '?').slice(0, 1).toUpperCase()}</span>
	{/if}
</span>

<style>
	.app-icon-dark {
		display: none;
	}
	:global(:root[data-theme='dark']) .app-icon-light {
		display: none;
	}
	:global(:root[data-theme='dark']) .app-icon-dark {
		display: block;
	}
	:global(:root[data-theme='dark']) .app-icon-invert {
		filter: invert(1);
	}
</style>
