<script lang="ts">
	import { cubicOut } from 'svelte/easing';

	// A live reading that rolls like a mechanical counter: when the value
	// changes, only the characters that changed slide out and their successors
	// slide in, upward when the number rose and downward when it fell. Places
	// are matched from the right, so 9.9% → 10.0% turns the units and adds a
	// digit instead of shifting every character. Assistive technology reads
	// the plain value; the rolling copy is hidden from it.
	let { value }: { value: string } = $props();

	const chars = $derived([...value]);
	const num = (s: string) => parseFloat(s.replace(/[^\d.-]/g, ''));
	let dir = $state<1 | -1>(1);
	let last = '';
	$effect.pre(() => {
		const v = value;
		const a = num(last);
		const b = num(v);
		dir = Number.isNaN(a) || Number.isNaN(b) || b >= a ? 1 : -1;
		last = v;
	});

	const still = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
	function roll(_: Element, { from }: { from: 1 | -1 }) {
		if (still) return { duration: 0 };
		return {
			duration: 300,
			easing: cubicOut,
			css: (t: number, u: number) => `transform: translateY(${(from * u * 0.8).toFixed(3)}em); opacity: ${t}`
		};
	}
</script>

<span class="roll"><span class="sr-only">{value}</span><span class="roll-track" aria-hidden="true"
		>{#each chars as c, i (chars.length - i)}<span class="roll-slot"
				>{#key c}<span in:roll={{ from: dir }} out:roll={{ from: (dir * -1) as 1 | -1 }}>{c}</span>{/key}</span
			>{/each}</span
	></span>

<style>
	.roll-track {
		white-space: pre;
	}
	.roll-slot {
		display: inline-grid;
		overflow-y: clip;
		vertical-align: bottom;
	}
	.roll-slot > :global(span) {
		grid-area: 1 / 1;
	}
</style>
