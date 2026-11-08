<script lang="ts">
	// A workspace's usage against its allowance. A limit of null is no limit;
	// 0 allows none. `sources` says where each value comes from; `exempt`
	// notes that the account looking is a built-in administrator.
	import { fmtBytes, fmtCpu } from '$lib/api/client';
	import type { QuotaLimits, QuotaUsage } from '$lib/api/types';
	import CapacityMeter from '$lib/components/CapacityMeter.svelte';
	import { countFields, LIMITS_HELP, sourceText, type LimitSource } from '$lib/limits';

	let {
		usage,
		limits,
		sources = {},
		exempt = false
	}: { usage: QuotaUsage; limits: QuotaLimits; sources?: Partial<Record<keyof QuotaLimits, LimitSource>>; exempt?: boolean } = $props();

	const rows: { key: keyof QuotaLimits; label: string; detail?: string; format?: (n: number) => string }[] = [
		...countFields.map((f) => ({ key: f.key as keyof QuotaLimits, label: f.label })),
		{ key: 'memory_bytes', label: 'Memory', detail: 'Assigned to all apps', format: fmtBytes },
		{ key: 'nano_cpus', label: 'CPU', detail: 'Assigned to all apps', format: fmtCpu }
	];
	// Per-kind counts only matter where a limit is set.
	const shown = $derived(rows.filter((r) => ['apps', 'sites', 'memory_bytes', 'nano_cpus'].includes(r.key) || limits[r.key] != null));
</script>

<div class="grid gap-x-6 gap-y-4 sm:grid-cols-2">
	{#each shown as r (r.key)}
		{@const src = sources[r.key]}
		<CapacityMeter label={r.label} value={usage[r.key]} max={limits[r.key] ?? 0} none={limits[r.key] === 0} format={r.format} caption={[r.detail, src ? sourceText[src] : ''].filter(Boolean).join(' · ')} />
	{/each}
</div>
<p class="mt-4 text-small text-muted">
	Apps count until they are deleted, running or stopped. Memory includes add-on services.{#if exempt} You are exempt as an administrator.{/if}
	<a class="link" href={LIMITS_HELP}>How limits work</a>
</p>
