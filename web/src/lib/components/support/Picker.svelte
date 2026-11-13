<script lang="ts" generics="T">
	import type { Snippet } from 'svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Popover from './Popover.svelte';

	// A listbox in a popover: one choice, or several (`multi`) that apply as
	// they are toggled. Rows are drawn by the `row` snippet; a search box
	// appears for longer lists. Arrow keys move, Enter or Space picks.
	type Option = { value: string; text: string; data?: T };
	let {
		label,
		options,
		value = [],
		multi = false,
		align = 'start',
		width = '16rem',
		disabled = false,
		triggerClass = 'picker-trigger',
		empty = 'Nothing to choose',
		trigger,
		row,
		footer,
		onpick
	}: {
		label: string;
		options: Option[];
		value?: string[];
		multi?: boolean;
		align?: 'start' | 'end';
		width?: string;
		disabled?: boolean;
		triggerClass?: string;
		empty?: string;
		trigger: Snippet;
		row: Snippet<[Option, boolean]>;
		footer?: Snippet<[() => void]>;
		onpick: (value: string[]) => void;
	} = $props();

	let open = $state(false);
	let pop = $state<{ show: () => void }>();
	/** Open the list (the composer's / key). */
	export function show() {
		pop?.show();
	}
	let q = $state('');
	let active = $state(0);
	const shown = $derived(q.trim() ? options.filter((o) => o.text.toLowerCase().includes(q.trim().toLowerCase())) : options);
	$effect(() => {
		if (!open) q = '';
	});
	function pick(o: Option, close: () => void) {
		if (multi) {
			onpick(value.includes(o.value) ? value.filter((v) => v !== o.value) : [...value, o.value]);
		} else {
			onpick([o.value]);
			close();
		}
	}
	function keys(e: KeyboardEvent, close: () => void) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			active = (active + (e.key === 'ArrowDown' ? 1 : -1) + shown.length) % Math.max(1, shown.length);
			(e.currentTarget as HTMLElement).querySelectorAll<HTMLElement>('[role=option]')[active]?.focus();
		} else if ((e.key === 'Enter' || e.key === ' ') && (e.target as HTMLElement).getAttribute('role') === 'option') {
			e.preventDefault();
			const o = shown[active];
			if (o) pick(o, close);
		}
	}
</script>

<Popover bind:this={pop} {label} {align} {width} {disabled} {triggerClass} {trigger} bind:open>
	{#snippet children(close)}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="flex min-h-0 flex-col" onkeydown={(e) => keys(e, close)}>
			{#if options.length > 6}
				<label class="relative m-1 flex items-center">
					<span class="sr-only">Filter</span>
					<Icon name="search" size={13} class="pointer-events-none absolute left-2 text-muted" />
					<input class="field h-8 py-0 pl-7 text-small" bind:value={q} oninput={() => (active = 0)} placeholder="Filter…" data-autofocus autocomplete="off" />
				</label>
			{/if}
			<div role="listbox" aria-label={label} aria-multiselectable={multi} class="grid min-h-0 gap-px overflow-y-auto">
				{#each shown as o, i (o.value)}
					{@const on = value.includes(o.value)}
					<div
						role="option"
						tabindex={i === active ? 0 : -1}
						aria-selected={on}
						class="pick-row"
						onclick={() => pick(o, close)}
						onfocus={() => (active = i)}
						onkeydown={() => {}}
					>
						<span class="pick-check" aria-hidden="true">{#if on}<Icon name="check" size={13} />{/if}</span>
						{@render row(o, on)}
					</div>
				{:else}
					<p class="px-3 py-2 text-small text-muted">{q ? `Nothing matches “${q}”.` : empty}</p>
				{/each}
			</div>
			{#if footer}<div class="mt-1 border-t border-rule-soft pt-1">{@render footer(close)}</div>{/if}
		</div>
	{/snippet}
</Popover>

<style>
	.pick-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2rem;
		border-radius: var(--radius-control);
		padding: 0.25rem 0.5rem 0.25rem 0.25rem;
		font-size: 13px;
		cursor: default;
		outline: none;
	}
	.pick-row:hover,
	.pick-row:focus-visible {
		background: color-mix(in srgb, var(--color-ink) 6%, transparent);
	}
	.pick-check {
		display: grid;
		place-items: center;
		width: 1rem;
		flex: none;
		color: var(--color-action);
	}
	:global(.picker-trigger) {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		min-height: 2rem;
		max-width: 100%;
		border: 1px solid transparent;
		border-radius: var(--radius-control);
		padding: 0.125rem 0.5rem;
		font-size: var(--text-small);
		text-align: left;
		transition:
			background-color 120ms,
			border-color 120ms;
	}
	:global(.picker-trigger:hover:not(:disabled)) {
		border-color: var(--color-rule);
		background: color-mix(in srgb, var(--color-ink) 4%, transparent);
	}
	:global(.picker-trigger:disabled) {
		cursor: default;
	}
	:global(.picker-trigger[aria-expanded='true']) {
		border-color: color-mix(in srgb, var(--color-action) 50%, var(--color-rule));
	}
</style>
