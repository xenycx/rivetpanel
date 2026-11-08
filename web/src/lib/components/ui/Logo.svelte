<script lang="ts">
	import { avatarUri, type AvatarKind } from '$lib/avatar';

	// A logo or avatar: the uploaded (or Discord, or favicon) image when there
	// is one and it loads, else a DiceBear picture generated from `seed`.
	// Decorative: the name is always written next to it. Pass the seed through
	// logoSeed(id, variant) so a chosen "New picture" shows everywhere.
	let {
		src = '',
		kind,
		seed,
		class: cls = '',
		generated = $bindable(false),
		...rest
	}: {
		src?: string | null;
		kind: AvatarKind;
		seed: string;
		class?: string;
		/** Out: true while the generated picture is shown (no image, or it failed to load). */
		generated?: boolean;
		[attr: string]: unknown;
	} = $props();

	// Remembers which URL failed, so a new URL is tried again.
	let failed = $state('');
	const own = $derived(!!src && src !== failed);
	const shown = $derived(own ? (src as string) : avatarUri(kind, seed));
	$effect(() => {
		generated = !own;
	});
</script>

<img src={shown} alt="" class="shrink-0 object-cover {cls}" referrerpolicy="no-referrer" decoding="async" draggable="false" onerror={() => (failed = src ?? '')} {...rest} />
