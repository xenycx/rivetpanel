<script lang="ts">
	import { goto } from '$app/navigation';
	import Icon from '$lib/components/ui/Icon.svelte';
	import { groupedCreateOptions } from '$lib/create';

	// The "New" menu: everything this account may create, grouped. It sits in
	// the top bar, and as the primary action of the overview's page header.
	// Keyboard: Enter/Space or ArrowDown opens, arrows move, Escape closes.
	let { primary = false }: { primary?: boolean } = $props();
	const groups = $derived(groupedCreateOptions());
	let open = $state(false);
	let btn: HTMLButtonElement | undefined = $state();
	let list: HTMLDivElement | undefined = $state();
	let at = $state('');

	function items() {
		return [...(list?.querySelectorAll<HTMLAnchorElement>('[role=menuitem]') ?? [])];
	}
	function focusItem(delta: number | 'first' | 'last') {
		const els = items();
		if (!els.length) return;
		const i = els.indexOf(document.activeElement as HTMLAnchorElement);
		els[delta === 'first' ? 0 : delta === 'last' ? els.length - 1 : (i + delta + els.length) % els.length].focus();
	}
	function place() {
		if (!btn) return;
		const r = btn.getBoundingClientRect();
		// Right-aligned under the button, shifted left only as far as needed to stay on screen.
		const vw = document.documentElement.clientWidth;
		const width = Math.min(570, vw - 16);
		const right = Math.max(8, Math.round(Math.min(vw - r.right, vw - width - 8)));
		at = `top: ${Math.round(r.bottom + 6)}px; right: ${right}px;`;
	}
	async function show() {
		place();
		open = true;
		await Promise.resolve();
		focusItem('first');
	}
	function hide(refocus = true) {
		open = false;
		if (refocus) btn?.focus();
	}
	function onkey(e: KeyboardEvent) {
		const k: Record<string, () => void> = {
			Escape: () => hide(),
			ArrowDown: () => focusItem(1),
			ArrowUp: () => focusItem(-1),
			Home: () => focusItem('first'),
			End: () => focusItem('last')
		};
		if (k[e.key]) {
			e.preventDefault();
			k[e.key]();
		} else if (e.key === 'Tab') hide(false);
	}
	function outside(e: PointerEvent) {
		if (open && !btn?.contains(e.target as Node) && !list?.contains(e.target as Node)) hide(false);
	}
	function pick(e: MouseEvent, href: string) {
		if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
		e.preventDefault();
		hide(false);
		goto(href);
	}
</script>

<svelte:window onpointerdown={outside} onresize={() => open && place()} />

{#if groups.length}
	<button
		bind:this={btn}
		class="btn {primary ? 'btn-primary' : ''} px-2.5 sm:px-3"
		aria-haspopup="menu"
		aria-expanded={open}
		aria-controls="new-menu"
		onclick={() => (open ? hide() : show())}
		onkeydown={(e) => {
			if (e.key === 'ArrowDown' && !open) {
				e.preventDefault();
				show();
			}
		}}
	>
		<Icon name="plus" /><span class="hidden sm:inline">New</span><span class="sr-only sm:hidden">New</span>
		<Icon name="chevronDown" size={13} class="-mr-0.5 hidden opacity-80 sm:block" />
	</button>
	{#if open}
		<div
			bind:this={list}
			id="new-menu"
			role="menu"
			tabindex="-1"
			aria-label="Create"
			class="menu-list fixed z-50 max-h-[min(78vh,40rem)] w-[min(38rem,calc(100vw-1rem))] animate-enter overflow-y-auto p-1.5"
			style={at}
			onkeydown={onkey}
		>
			<div class="grid divide-y divide-rule-soft">
				{#each groups as [name, opts] (name)}
					<div class="py-1 first:pt-0 last:pb-0" role="group" aria-label={name}>
						<p class="eyebrow px-2 pt-1 pb-0.5">{name}</p>
						<div class="grid sm:grid-cols-2">
							{#each opts as o (o.key)}
								<a role="menuitem" href={o.href} class="menu-item group items-start! gap-2.5 px-2! py-1.5!" onclick={(e) => pick(e, o.href)}>
									<span class="grid size-7 shrink-0 place-items-center rounded-control bg-paper-2/70 text-muted group-hover:text-ink group-focus:text-ink"><Icon name={o.icon} size={14} /></span>
									<span class="min-w-0">
										<span class="block font-medium">{o.label}</span>
										<span class="block text-[11.5px] leading-4 whitespace-normal text-muted">{o.hint}</span>
									</span>
								</a>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		</div>
	{/if}
{/if}
