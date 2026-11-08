<script lang="ts">
	import { avatarUri, type AvatarKind } from '$lib/avatar';

	// A logo or avatar: the uploaded (or Discord, or favicon) image when there
	// is one and it loads, else a DiceBear picture generated from `seed`.
	// Decorative: the name is always written next to it.
	let {
		src = '',
		kind,
		seed,
		class: cls = '',
		...rest
	}: {
		src?: string | null;
		kind: AvatarKind;
		seed: string;
		class?: string;
		[attr: string]: unknown;
	} = $props();

	// Remembers which URL failed, so a new URL is tried again.
	let failed = $state('');
	const shown = $derived(src && src !== failed ? src : avatarUri(kind, seed));
</script>

<img src={shown} alt="" class="shrink-0 object-cover {cls}" referrerpolicy="no-referrer" decoding="async" draggable="false" onerror={() => (failed = src ?? '')} {...rest} />
