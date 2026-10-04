<script lang="ts" module>
	/**
	 * A menu row. A short hint (a shortcut such as "Ctrl+K") sits on the same
	 * line; a longer one becomes the tooltip, or a second line when the item is
	 * disabled (the reason) or `detail` asks for it. `heading` is a non-interactive
	 * caption such as the signed-in e-mail.
	 */
	export type MenuItem =
		| { label: string; onselect: () => void; danger?: boolean; disabled?: boolean; hint?: string; detail?: boolean }
		| { heading: string }
		| 'separator';
	const SHORT_HINT = 16;
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon, { type IconName } from './Icon.svelte';

	// Compact action menu for rows and narrow layouts. Keyboard: Enter/Space or
	// ArrowDown opens, arrows move, Escape closes and returns focus.
	let {
		items,
		label,
		icon = 'more',
		text = '',
		align = 'end',
		class: cls = '',
		triggerClass = '',
		menuClass = '',
		fixed = false,
		side = false,
		block = false,
		trigger
	}: {
		items: MenuItem[];
		label: string;
		icon?: IconName;
		text?: string;
		align?: 'start' | 'end';
		class?: string;
		triggerClass?: string;
		menuClass?: string;
		/** Position against the viewport, for triggers inside narrow scrolling containers that would clip the list. */
		fixed?: boolean;
		/** With fixed: open beside the trigger (a flyout from a narrow rail) instead of below it. */
		side?: boolean;
		/** Take the full width of the parent (a sidebar row) instead of sitting inline. */
		block?: boolean;
		trigger?: Snippet;
	} = $props();

	let open = $state(false);
	let btn: HTMLButtonElement | undefined = $state();
	let list: HTMLDivElement | undefined = $state();
	const uid = Math.random().toString(36).slice(2, 8);

	function focusItem(delta: number | 'first' | 'last') {
		const els = [...(list?.querySelectorAll<HTMLButtonElement>('[role=menuitem]:not(:disabled)') ?? [])];
		if (!els.length) return;
		const i = els.indexOf(document.activeElement as HTMLButtonElement);
		const n = delta === 'first' ? 0 : delta === 'last' ? els.length - 1 : (i + delta + els.length) % els.length;
		els[n].focus();
	}
	let at = $state('');
	// Viewport positioning, chosen on open: always when asked for, and whenever
	// an ancestor clips its overflow (rounded lists, scroll panes), where an
	// absolutely placed list would be cut off.
	let floating = $state(false);
	function clipped(el: HTMLElement) {
		for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
			const cs = getComputedStyle(p);
			if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') return true;
		}
		return false;
	}
	function place() {
		if (!btn) return;
		const r = btn.getBoundingClientRect();
		// A trigger in the lower part of the screen (a sidebar footer) opens upwards.
		const vh = window.innerHeight;
		const up = r.top > vh * 0.55;
		if (side) at = `position: fixed; ${up ? `bottom: ${Math.round(vh - r.bottom)}px` : `top: ${Math.round(r.top)}px`}; left: ${Math.round(r.right + 10)}px;`;
		else at = `position: fixed; ${up ? `bottom: ${Math.round(vh - r.top + 4)}px` : `top: ${Math.round(r.bottom + 4)}px`}; ${align === 'end' ? `right: ${Math.round(document.documentElement.clientWidth - r.right)}px` : `left: ${Math.round(r.left)}px`}; min-width: ${Math.max(160, Math.round(r.width))}px;`;
	}
	async function show() {
		floating = fixed || (!!btn && clipped(btn));
		if (floating) place();
		open = true;
		await Promise.resolve();
		focusItem('first');
	}
	function hide(refocus = true) {
		open = false;
		if (refocus) btn?.focus();
	}
	function onkey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			e.preventDefault();
			hide();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			focusItem(1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			focusItem(-1);
		} else if (e.key === 'Home') {
			e.preventDefault();
			focusItem('first');
		} else if (e.key === 'End') {
			e.preventDefault();
			focusItem('last');
		} else if (e.key === 'Tab') hide(false);
	}
	function outside(e: PointerEvent) {
		if (open && !btn?.contains(e.target as Node) && !list?.contains(e.target as Node)) hide(false);
	}
</script>

<svelte:window onpointerdown={outside} onresize={() => floating && open && place()} />
<svelte:document onscrollcapture={() => floating && open && place()} />

<div class="relative {block ? 'block' : 'inline-block'} {cls}">
	<button
		bind:this={btn}
		class={triggerClass || (trigger ? 'flex items-center gap-1.5 rounded-pill p-0.5 pr-1.5 hover:bg-paper-2/60' : `btn ${text ? '' : 'btn-icon'} btn-quiet`)}
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls="menu-{uid}"
		aria-label={text ? undefined : label}
		onclick={() => (open ? hide() : show())}
		onkeydown={(e) => {
			if (e.key === 'ArrowDown' && !open) {
				e.preventDefault();
				show();
			}
		}}
	>
		{#if trigger}{@render trigger()}{:else}<Icon name={icon} />{#if text}<span>{text}</span>{/if}{/if}
	</button>
	{#if open}
		<div
			bind:this={list}
			id="menu-{uid}"
			role="menu"
			tabindex="-1"
			aria-label={label}
			class="menu-list {floating ? 'z-50' : 'absolute top-full z-40 mt-1'} max-h-[70vh] max-w-[min(22rem,calc(100vw-1rem))] min-w-40 animate-enter overflow-y-auto {floating ? '' : align === 'end' ? 'right-0' : 'left-0'} {menuClass}"
			style={floating ? at : undefined}
			onkeydown={onkey}
		>
			{#each items as it, i (i)}
				{#if it === 'separator'}
					<div class="menu-sep" role="separator"></div>
				{:else if 'heading' in it}
					<div class="menu-head truncate" role="presentation" title={it.heading}>{it.heading}</div>
				{:else}
					{@const short = !!it.hint && it.hint.length <= SHORT_HINT}
					{@const below = !!it.hint && !short && (it.disabled || it.detail)}
					<button
						role="menuitem"
						class="menu-item {it.danger ? 'danger' : ''} {below ? 'flex-col items-start! gap-0!' : ''}"
						disabled={it.disabled}
						title={it.hint && !short && !below ? it.hint : undefined}
						onclick={() => {
							hide();
							it.onselect();
						}}
					>
						<span class="truncate">{it.label}</span>
						{#if short}<span class="menu-hint">{it.hint}</span>
						{:else if below}<span class="text-[11px] leading-4 whitespace-normal text-muted">{it.hint}</span>
						{:else if it.hint}<span class="sr-only">: {it.hint}</span>{/if}
					</button>
				{/if}
			{/each}
		</div>
	{/if}
</div>
