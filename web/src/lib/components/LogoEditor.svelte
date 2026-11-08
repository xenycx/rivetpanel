<script lang="ts">
	import { squarePng } from '$lib/image';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import type { AvatarKind } from '$lib/avatar';

	let {
		src,
		custom,
		kind,
		seed,
		fallback,
		disabled = false,
		busy = false,
		compact = false,
		name = '',
		onUpload,
		onRemove,
		onShuffle,
		children
	}: {
		/** What is shown now (custom, Discord avatar, favicon...); "" = generated. */
		src: string;
		custom: boolean;
		/** The generated DiceBear logo shown when there is no image (seed: logoSeed(id, variant)). */
		kind: AvatarKind;
		seed: string;
		/** Shown instead of the generated logo when there is no image (game servers). */
		fallback?: import('svelte').Snippet;
		disabled?: boolean;
		busy?: boolean;
		/** A 40px logo in a page header whose controls open from it as a menu. */
		compact?: boolean;
		/** What the logo belongs to, for the compact menu's accessible name. */
		name?: string;
		onUpload: (base64: string) => Promise<void>;
		onRemove: () => Promise<void>;
		/** Picks another generated picture; offered only while it is the one shown. */
		onShuffle?: () => Promise<void>;
		children?: import('svelte').Snippet;
	} = $props();

	let error = $state('');
	let input: HTMLInputElement | undefined = $state();
	// True while the generated picture is shown (no image, or it failed to load).
	let generated = $state(false);
	const canShuffle = $derived(!!onShuffle && generated && !(fallback && !src));

	async function choose(e: Event) {
		const file = (e.currentTarget as HTMLInputElement).files?.[0];
		(e.currentTarget as HTMLInputElement).value = '';
		if (!file) return;
		error = '';
		try {
			const img = await squarePng(file);
			await onUpload(img.base64);
		} catch (err) {
			error = err instanceof Error && err.message ? err.message : 'That image could not be read. Try a PNG, JPEG or WebP file.';
			if (compact) toast(error, 'fail');
		}
	}

	const menuItems = $derived<MenuItem[]>([
		{ label: custom ? 'Replace logo…' : 'Upload logo…', icon: 'upload', disabled: disabled || busy, onselect: () => input?.click() },
		...(canShuffle ? [{ label: 'New picture', icon: 'shuffle' as const, hint: 'Draw a different generated logo', disabled: disabled || busy, onselect: () => void onShuffle?.() }] : []),
		...(custom ? [{ label: 'Remove custom logo', icon: 'trash' as const, disabled: disabled || busy, onselect: () => void onRemove() }] : [])
	]);
</script>

<input bind:this={input} type="file" accept="image/png,image/jpeg,image/webp,image/gif" class="sr-only" tabindex="-1" aria-hidden="true" onchange={choose} {disabled} />
{#if compact}
	<Menu items={menuItems} label="Logo" triggerLabel={name ? `Change the logo of ${name}` : 'Change logo'} align="start"
		triggerClass="group relative block shrink-0 rounded-tile focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-action">
		{#snippet trigger()}
			<Logo {src} {kind} {seed} bind:generated class="size-10 rounded-tile border border-rule-soft" />
			<span class="absolute -right-1 -bottom-1 grid size-4.5 place-items-center rounded-pill border border-rule-soft bg-raised text-muted transition-colors group-hover:text-ink" aria-hidden="true"><Icon name="pencil" size={10} /></span>
		{/snippet}
	</Menu>
{:else}
	<div class="flex flex-wrap items-center gap-4">
		{#if fallback && !src}
			{@render fallback()}
		{:else}
			<Logo {src} {kind} {seed} bind:generated class="size-16 rounded-tile border border-rule-soft" />
		{/if}
		<div class="flex flex-wrap gap-2">
			<button type="button" class="btn btn-sm" disabled={disabled || busy} onclick={() => input?.click()}><Icon name="upload" size={14} />{custom ? 'Replace logo' : 'Upload logo'}</button>
			{#if canShuffle}<button type="button" class="btn btn-sm" disabled={disabled || busy} onclick={onShuffle} title="Draw a different generated logo"><Icon name="shuffle" size={14} />New picture</button>{/if}
			{#if custom}<button type="button" class="btn btn-sm btn-quiet" disabled={disabled || busy} onclick={onRemove}><Icon name="trash" size={14} />Remove custom logo</button>{/if}
			{@render children?.()}
		</div>
	</div>
	{#if error}<p class="mt-2 text-small text-fail" role="alert">{error}</p>{/if}
{/if}
