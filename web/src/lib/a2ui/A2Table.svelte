<script lang="ts">
	import { cell, NUMERIC } from './format';

	// An A2UI Table: rows of plain values under named columns, numbers
	// formatted and right-aligned, with an optional share bar (0-100).
	type Column = { key: string; label: string; align?: string; format?: string };
	let { title = '', columns, rows, bar }: { title?: string; columns: unknown; rows: unknown; bar?: unknown } = $props();

	const cols = $derived<Column[]>(
		Array.isArray(columns) ? columns.slice(0, 8).filter((c) => c && typeof c.key === 'string').map((c) => ({ key: c.key, label: String(c.label ?? c.key), align: c.align, format: c.format })) : []
	);
	const list = $derived<Record<string, unknown>[]>(Array.isArray(rows) ? rows.slice(0, 50).filter((r) => r && typeof r === 'object' && !Array.isArray(r)) : []);
	const barKey = $derived(typeof bar === 'string' && cols.some((c) => c.key === bar) ? bar : '');
	const right = (c: Column) => c.align === 'end' || (!c.align && NUMERIC.has(c.format ?? ''));
	const share = (v: unknown) => {
		const n = typeof v === 'number' ? v : Number(v);
		return Number.isFinite(n) ? Math.max(0, Math.min(100, n)) : 0;
	};
</script>

<div class="min-w-0">
	{#if title}<p class="mb-1 text-small font-medium text-ink">{title}</p>{/if}
	<div class="overflow-x-auto rounded-tile border border-rule-soft bg-panel">
		<table class="w-full text-left text-small" aria-label={title || 'Table'}>
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-2 [&>th]:py-1.5 [&>th]:font-normal">
					{#each cols as c (c.key)}
						<th scope="col" class="eyebrow whitespace-nowrap" class:text-right={right(c)} class:text-center={c.align === 'center'}>{c.label}</th>
					{/each}
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each list as r, i (`${i}:${String(r[cols[0]?.key] ?? '')}`)}
					<tr class="[&>td]:px-2 [&>td]:py-1.5">
						{#each cols as c (c.key)}
							<td class="max-w-64 truncate" class:text-right={right(c)} class:text-center={c.align === 'center'} class:font-mono={right(c)} class:tabular-nums={true} title={String(r[c.key] ?? '')}>
								{cell(r[c.key], c.format)}
								{#if c.key === barKey}
									<span class="mt-0.5 block h-0.5 w-16 overflow-hidden rounded-pill bg-rule-soft {right(c) ? 'ml-auto' : ''}" aria-hidden="true"><span class="block h-full bg-action" style="width: {share(r[c.key])}%"></span></span>
								{/if}
							</td>
						{/each}
					</tr>
				{:else}
					<tr><td colspan={Math.max(cols.length, 1)} class="px-2 py-3 text-muted">No rows.</td></tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>
