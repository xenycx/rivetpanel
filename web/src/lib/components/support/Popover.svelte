<script lang="ts">
	import type { Snippet } from 'svelte';

	// A small floating panel anchored to its trigger: positioned against the
	// viewport (so scrolling panes never clip it), closed by Escape, a click
	// outside, or close(). Focus returns to the trigger.
	let {
		label,
		align = 'start',
		width = '16rem',
		disabled = false,
		triggerClass = '',
		trigger,
		children,
		open = $bindable(false)
	}: {
		label: string;
		align?: 'start' | 'end';
		width?: string;
		disabled?: boolean;
		triggerClass?: string;
		trigger: Snippet;
		children: Snippet<[() => void]>;
		open?: boolean;
	} = $props();

	let btn = $state<HTMLButtonElement>();
	let panel = $state<HTMLDivElement>();
	let at = $state('');
	const uid = Math.random().toString(36).slice(2, 8);

	function place() {
		if (!btn) return;
		const r = btn.getBoundingClientRect();
		const vh = innerHeight;
		const up = r.bottom > vh * 0.62;
		const x = align === 'end' ? `right: ${Math.max(8, Math.round(document.documentElement.clientWidth - r.right))}px` : `left: ${Math.max(8, Math.round(r.left))}px`;
		at = `${up ? `bottom: ${Math.round(vh - r.top + 4)}px` : `top: ${Math.round(r.bottom + 4)}px`}; ${x}; width: ${width}; max-height: ${Math.round((up ? r.top : vh - r.bottom) - 16)}px;`;
	}
	export function show() {
		if (disabled) return;
		place();
		open = true;
		queueMicrotask(() => panel?.querySelector<HTMLElement>('[data-autofocus], input, [role=option], button')?.focus());
	}
	export function close(refocus = true) {
		if (!open) return;
		open = false;
		if (refocus) btn?.focus();
	}
	function outside(e: PointerEvent) {
		if (!open) return;
		const t = e.target as Node;
		if (!panel?.contains(t) && !btn?.contains(t)) close(false);
	}
	function key(e: KeyboardEvent) {
		if (open && e.key === 'Escape') {
			e.preventDefault();
			e.stopPropagation();
			close();
		}
	}
</script>

<svelte:window onpointerdown={outside} onresize={() => open && place()} onkeydown={key} />

<button
	bind:this={btn}
	type="button"
	class={triggerClass}
	aria-haspopup="dialog"
	aria-expanded={open}
	aria-controls={open ? `pop-${uid}` : undefined}
	aria-label={label}
	{disabled}
	onclick={() => (open ? close() : show())}
>
	{@render trigger()}
</button>
{#if open}
	<div bind:this={panel} id="pop-{uid}" role="dialog" aria-label={label} class="pop-panel" style={at}>
		{@render children(() => close())}
	</div>
{/if}

<style>
	.pop-panel {
		position: fixed;
		z-index: 60;
		display: flex;
		flex-direction: column;
		overflow: auto;
		border: 1px solid var(--color-rule-soft);
		border-radius: var(--radius-overlay);
		background: var(--color-raised);
		box-shadow: var(--shadow-overlay);
		padding: 0.25rem;
		animation: var(--animate-enter);
		overscroll-behavior: contain;
	}
</style>
