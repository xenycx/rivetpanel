<script lang="ts">
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';

	// A sequence chart: participants side by side with lifelines, one row per
	// message. A narrow figure shows the same steps as a numbered list.
	export type Actor = { id: string; name: string; sub?: string; icon: IconName; k?: 'panel' | 'docker' | 'host' | 'action' };
	export type Step = { from: string; to: string; label: string; tall?: boolean; k?: 'panel' | 'docker' | 'host' };

	let { actors, steps }: { actors: Actor[]; steps: Step[] } = $props();

	const n = $derived(actors.length);
	const col = (id: string) => actors.findIndex((a) => a.id === id);
	const center = (i: number) => ((i + 0.5) / n) * 100;
	const name = (id: string) => actors[col(id)]?.name ?? id;
</script>

<div class="dg dg-seq" style:--n={n}>
	<div class="dg-seq-head" aria-hidden="true">
		{#each actors as a (a.id)}
			<div class="dg-box" data-k={a.k}>
				<p class="dg-name"><Icon name={a.icon} size={14} />{a.name}</p>
				{#if a.sub}<p class="dg-sub">{a.sub}</p>{/if}
			</div>
		{/each}
	</div>
	<div class="dg-seq-body" aria-hidden="true">
		{#each actors as _, i (i)}<span class="dg-life" style:left="{center(i)}%"></span>{/each}
		{#each steps as s, i (i)}
			{@const a = col(s.from)}
			{@const b = col(s.to)}
			<div class="dg-msg" class:tall={s.tall || s.label.length > 44}>
				{#if a === b}
					<div class="dg-msg-self" style:left="{center(a)}%" style:--w="{100 / n}%" style:--k={s.k ? `var(--color-${s.k === 'panel' ? 'data' : s.k === 'docker' ? 'run' : 'warn'})` : undefined}>
						<span><span class="dg-msg-no">{i + 1}</span>{s.label}</span>
					</div>
				{:else}
					{@const lo = Math.min(a, b)}
					{@const hi = Math.max(a, b)}
					<span class="dg-msg-arrow {b > a ? 'right' : 'left'}" style:left="{center(lo)}%" style:width="{center(hi) - center(lo)}%"></span>
					<span class="dg-msg-label" style:left="{(center(lo) + center(hi)) / 2}%" style:--w="calc({center(hi) - center(lo)}% - 0.75rem)"><span class="dg-msg-no">{i + 1}</span>{s.label}</span>
				{/if}
			</div>
		{/each}
	</div>
	<ol class="dg-seq-list">
		{#each steps as s, i (i)}
			<li>
				<span class="dg-msg-no">{i + 1}</span>
				<span>
					<span class="dg-seq-route">{name(s.from)}{#if s.from !== s.to}<Icon name="chevronRight" size={11} />{name(s.to)}{/if}</span>
					{s.label}
				</span>
			</li>
		{/each}
	</ol>
</div>
