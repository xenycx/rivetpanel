<script lang="ts">
	// One labelled usage bar: value of max. A limited meter always has a bar;
	// an unlimited one (max 0, unless `none`) says "No limit" and has none.
	// With `none`, max 0 means nothing is allowed. `caption` says where the
	// limit comes from.
	import { NO_LIMIT } from '$lib/limits';

	let {
		label,
		value,
		max,
		format = (n: number) => String(n),
		none = false,
		caption = ''
	}: { label: string; value: number; max: number; format?: (n: number) => string; none?: boolean; caption?: string } = $props();
	const limited = $derived(max > 0 || none);
	const pct = $derived(max > 0 ? Math.min(100, Math.round((value / max) * 100)) : value > 0 ? 100 : 0);
	const tone = $derived(pct >= 90 ? 'bg-fail' : pct >= 75 ? 'bg-warn' : 'bg-run');
</script>

<div class="min-w-0">
	<div class="flex items-baseline justify-between gap-2">
		<span class="eyebrow">{label}</span>
		<span class="shrink-0 font-mono text-small whitespace-nowrap"><span class="font-medium text-ink">{format(value)}</span><span class="text-muted">&nbsp;/ {max > 0 ? format(max) : limited ? 'none allowed' : NO_LIMIT}</span></span>
	</div>
	{#if limited}
		<div class="mt-1.5 h-1.5 overflow-hidden rounded-pill bg-rule-soft" role="meter" aria-label={label} aria-valuemin="0" aria-valuemax={max} aria-valuenow={value}>
			<div class="h-full rounded-pill {tone} transition-[width]" style="width: {pct}%"></div>
		</div>
	{/if}
	{#if caption}<p class="mt-1 text-[12px] text-muted">{caption}</p>{/if}
</div>
