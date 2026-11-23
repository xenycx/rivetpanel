<script lang="ts">
	import { fmtBytes } from '$lib/api/client';
	import type { ProcessEntry } from '$lib/api/metrics';

	// Top processes: pid, name, command line, CPU and memory, and the app a
	// process belongs to. With `stable` (a live list) rows keep their place
	// unless a process clearly overtakes the one above it, so a list that
	// refreshes every few seconds does not jitter.
	let {
		items,
		showApp = true,
		stable = false,
		limit,
		label = 'Processes'
	}: { items: ProcessEntry[]; showApp?: boolean; stable?: boolean; limit?: number; label?: string } = $props();

	let prevOrder: number[] = [];
	// Overtaking needs a clear margin: 15 % more CPU plus one percentage point.
	const clearlyAbove = (a: ProcessEntry, b: ProcessEntry) => a.cpu_pct > b.cpu_pct * 1.15 + 1;

	const rows = $derived.by(() => {
		const byCpu = [...items].sort((a, b) => b.cpu_pct - a.cpu_pct || b.rss_bytes - a.rss_bytes);
		if (!stable) return limit ? byCpu.slice(0, limit) : byCpu;
		const byPid = new Map(items.map((p) => [p.pid, p]));
		const kept = prevOrder.filter((pid) => byPid.has(pid)).map((pid) => byPid.get(pid)!);
		const seen = new Set(kept.map((p) => p.pid));
		const list = [...kept, ...byCpu.filter((p) => !seen.has(p.pid))];
		// Insertion sort that only moves a row past one it clearly beats.
		for (let i = 1; i < list.length; i++) {
			let j = i;
			while (j > 0 && clearlyAbove(list[j], list[j - 1])) {
				[list[j - 1], list[j]] = [list[j], list[j - 1]];
				j--;
			}
		}
		const out = limit ? list.slice(0, limit) : list;
		prevOrder = out.map((p) => p.pid);
		return out;
	});
	const maxCpu = $derived(Math.max(100, ...items.map((p) => p.cpu_pct)));
</script>

<div class="overflow-x-auto">
	<table class="w-full text-left text-small" aria-label={label}>
		<thead class="border-b border-rule-soft">
			<tr class="[&>th]:px-2 [&>th]:py-2 [&>th]:font-normal">
				<th class="eyebrow w-16">PID</th>
				<th class="eyebrow">Process</th>
				<th class="eyebrow hidden md:table-cell">Command</th>
				<th class="eyebrow w-28 text-right">CPU</th>
				<th class="eyebrow w-24 text-right">Memory</th>
				{#if showApp}<th class="eyebrow w-36">App</th>{/if}
			</tr>
		</thead>
		<tbody class="divide-y divide-rule-soft">
			{#each rows as p (p.pid)}
				<tr class="[&>td]:px-2 [&>td]:py-1.5">
					<td class="font-mono text-muted tabular-nums">{p.pid}</td>
					<td class="max-w-40 truncate font-mono font-medium" title={p.comm}>{p.comm}</td>
					<td class="hidden max-w-0 truncate font-mono text-[12px] text-muted md:table-cell" title={p.cmd}>{p.cmd || '—'}</td>
					<td class="text-right">
						<span class="font-mono tabular-nums">{p.cpu_pct.toFixed(p.cpu_pct < 10 ? 1 : 0)}%</span>
						<span class="mt-0.5 ml-auto block h-0.5 w-16 overflow-hidden rounded-pill bg-rule-soft" aria-hidden="true"><span class="block h-full bg-action" style="width: {Math.min(100, (p.cpu_pct / maxCpu) * 100)}%"></span></span>
					</td>
					<td class="text-right font-mono tabular-nums">{fmtBytes(p.rss_bytes)}</td>
					{#if showApp}
						<td class="max-w-36 truncate">{#if p.bot_id}<a class="link" href="/bots/{p.bot_id}">{p.bot_name || p.bot_id}</a>{:else}<span class="text-muted">host</span>{/if}</td>
					{/if}
				</tr>
			{:else}
				<tr><td colspan={showApp ? 6 : 5} class="px-2 py-3 text-muted">No processes reported.</td></tr>
			{/each}
		</tbody>
	</table>
</div>
