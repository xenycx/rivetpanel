<script lang="ts">
	// A drag handle between two panes. It sets the width of the pane on its
	// `grows` side: drag it, use the arrow keys (Shift for bigger steps),
	// Home/End for the limits, or double-click (Enter) to go back to the
	// default width. The width is reported in pixels; null is the default.
	let {
		width,
		min,
		max,
		grows = 'left',
		label,
		onresize,
		class: cls = ''
	}: {
		/** The pane's current width in pixels. */
		width: number;
		min: number;
		max: number;
		/** Which side of the handle the resized pane is on. */
		grows?: 'left' | 'right';
		label: string;
		onresize: (px: number | null) => void;
		class?: string;
	} = $props();

	let dragging = $state(false);
	const clamp = (n: number) => Math.min(max, Math.max(min, n));

	function down(e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		const el = e.currentTarget as HTMLElement;
		el.setPointerCapture(e.pointerId);
		const x0 = e.clientX;
		const w0 = width;
		dragging = true;
		document.documentElement.classList.add('col-resizing');
		const move = (ev: PointerEvent) => {
			const dx = ev.clientX - x0;
			onresize(clamp(grows === 'left' ? w0 + dx : w0 - dx));
		};
		const up = () => {
			dragging = false;
			document.documentElement.classList.remove('col-resizing');
			el.removeEventListener('pointermove', move);
			el.removeEventListener('pointerup', up);
			el.removeEventListener('pointercancel', up);
		};
		el.addEventListener('pointermove', move);
		el.addEventListener('pointerup', up);
		el.addEventListener('pointercancel', up);
	}
	function key(e: KeyboardEvent) {
		const step = e.shiftKey ? 64 : 16;
		const toward = grows === 'left' ? 1 : -1;
		let next: number | null | undefined;
		if (e.key === 'ArrowRight') next = width + step * toward;
		else if (e.key === 'ArrowLeft') next = width - step * toward;
		else if (e.key === 'Home') next = min;
		else if (e.key === 'End') next = max;
		else if (e.key === 'Enter') next = null;
		if (next === undefined) return;
		e.preventDefault();
		onresize(next === null ? null : clamp(next));
	}
</script>

<!-- A focusable separator is an interactive widget (WAI-ARIA window splitter). -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	class="resizer {cls}"
	class:dragging
	role="separator"
	aria-orientation="vertical"
	aria-label={label}
	aria-valuenow={Math.round(width)}
	aria-valuemin={Math.round(min)}
	aria-valuemax={Math.round(max)}
	tabindex="0"
	title="Drag to resize · double-click to reset"
	onpointerdown={down}
	onkeydown={key}
	ondblclick={() => onresize(null)}
>
	<span class="grip" aria-hidden="true"></span>
</div>

<style>
	.resizer {
		position: absolute;
		top: 0;
		bottom: 0;
		z-index: 5;
		width: 1rem;
		display: grid;
		place-items: center;
		cursor: col-resize;
		touch-action: none;
		outline: none;
	}
	/* A hairline that thickens and takes the accent while hovered or dragged. */
	.resizer::before {
		content: '';
		position: absolute;
		top: 0.75rem;
		bottom: 0.75rem;
		left: 50%;
		width: 2px;
		margin-left: -1px;
		border-radius: var(--radius-pill);
		background: transparent;
		transition:
			background-color 140ms,
			top 200ms cubic-bezier(0.16, 1, 0.3, 1),
			bottom 200ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.resizer:hover::before,
	.resizer:focus-visible::before,
	.resizer.dragging::before {
		top: 0;
		bottom: 0;
		background: color-mix(in srgb, var(--color-action) 55%, transparent);
	}
	.grip {
		position: relative;
		width: 6px;
		height: 2.25rem;
		border: 1px solid var(--color-rule);
		border-radius: var(--radius-pill);
		background: var(--color-panel);
		box-shadow: 0 1px 3px rgb(20 18 16 / 0.12);
		opacity: 0;
		transition: opacity 140ms;
	}
	.resizer:hover .grip,
	.resizer:focus-visible .grip,
	.resizer.dragging .grip {
		opacity: 1;
		border-color: color-mix(in srgb, var(--color-action) 60%, var(--color-rule));
	}
	:global(html.col-resizing),
	:global(html.col-resizing *) {
		cursor: col-resize !important;
		user-select: none !important;
	}
	@media (pointer: coarse) {
		.grip {
			opacity: 1;
		}
	}
</style>
