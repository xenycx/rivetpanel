<script lang="ts">
	import Icon from './Icon.svelte';
	import { gameIcon } from '$lib/gameIcons';

	// The icon of a server type in a rounded tile; a drawn gamepad when the
	// type has none. Decorative: the name is always written next to it.
	let { type, size = 40, class: cls = '' }: { type: { slug?: string; category?: string } | null | undefined; size?: number; class?: string } = $props();
	const src = $derived(gameIcon(type));
</script>

<span class="grid shrink-0 place-items-center overflow-hidden rounded-tile bg-paper-2/70 {cls}" style="width: {size}px; height: {size}px" aria-hidden="true">
	{#if src}
		<img {src} alt="" width={Math.round(size * 0.78)} height={Math.round(size * 0.78)} class="object-contain" loading="lazy" decoding="async" draggable="false" />
	{:else}
		<Icon name="gamepad" size={Math.round(size * 0.45)} class="text-action" />
	{/if}
</span>
