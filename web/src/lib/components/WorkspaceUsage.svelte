<script lang="ts">
	// A workspace's usage against each limit that applies to it. A limit of
	// null is unlimited; 0 allows none.
	import { fmtBytes } from '$lib/api/client';
	import type { QuotaLimits, QuotaUsage } from '$lib/api/types';
	import CapacityMeter from '$lib/components/CapacityMeter.svelte';

	let { usage, limits, exempt = false }: { usage: QuotaUsage; limits: QuotaLimits; exempt?: boolean } = $props();

	const cores = (n: number) => `${Math.round((n / 1e9) * 100) / 100} CPU`;
	const rows: { key: keyof QuotaLimits; label: string; format?: (n: number) => string }[] = [
		{ key: 'apps', label: 'Apps in total' },
		{ key: 'bots', label: 'Bots' },
		{ key: 'games', label: 'Game servers' },
		{ key: 'web_apps', label: 'Web apps' },
		{ key: 'sites', label: 'Sites' },
		{ key: 'memory_bytes', label: 'Memory', format: fmtBytes },
		{ key: 'nano_cpus', label: 'CPU', format: cores }
	];
	// Per-kind counts only matter where a limit is set.
	const shown = $derived(rows.filter((r) => ['apps', 'sites', 'memory_bytes', 'nano_cpus'].includes(r.key) || limits[r.key] != null));
</script>

{#if exempt}
	<p class="text-small text-muted">An administrator owns this workspace, so no limits apply to it.</p>
{/if}
<div class="grid gap-x-6 gap-y-4 sm:grid-cols-2">
	{#each shown as r (r.key)}
		{@const lim = exempt ? null : limits[r.key]}
		{@const used = usage[r.key === 'apps' ? 'apps' : r.key]}
		{#if lim === 0}
			<div class="min-w-0">
				<div class="flex items-baseline justify-between gap-2">
					<span class="eyebrow">{r.label}</span>
					<span class="font-mono text-small"><span class="font-medium text-ink">{(r.format ?? String)(used)}</span><span class="text-muted">&nbsp;/ none allowed</span></span>
				</div>
			</div>
		{:else}
			<CapacityMeter label={r.label} value={used} max={lim ?? 0} format={r.format} />
		{/if}
	{/each}
</div>
