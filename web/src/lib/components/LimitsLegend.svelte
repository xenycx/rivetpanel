<script lang="ts">
	// The four kinds of limit, what each bounds and what enforces it. Shown
	// once on the Default allowance section and in the documentation; other
	// limit surfaces link to it instead of repeating it.
	let { class: cls = '', note = true }: { class?: string; note?: boolean } = $props();
	const rows = [
		{ term: 'App size', what: 'Memory, CPU and processes of one app, chosen when it is created or resized.', who: 'Docker (cgroups) while the app runs' },
		{ term: 'Workspace allowance', what: 'What one workspace may hold in total: apps, sites, memory and CPU. Each workspace inherits the default allowance unless it has its own.', who: 'The panel, when an app or site is created, resized or moved' },
		{ term: 'Node budget', what: 'The sum of app sizes wanted running on one node (RIVET_NODE_MEMORY_BYTES).', who: 'The panel, when an app starts' },
		{ term: 'Node maximum', what: 'The largest app size one node accepts: its memory and cores, or less if set.', who: 'The panel, when an app is created or resized' }
	];
</script>

<dl class="grid divide-y divide-rule-soft border-y border-rule-soft text-small {cls}">
	{#each rows as r (r.term)}
		<div class="grid gap-x-6 gap-y-0.5 py-2.5 sm:grid-cols-[11rem_minmax(0,1fr)_minmax(0,16rem)]">
			<dt class="font-medium text-ink">{r.term}</dt>
			<dd class="text-muted">{r.what}</dd>
			<dd class="text-muted"><span class="sr-only">Enforced by: </span>{r.who}</dd>
		</div>
	{/each}
</dl>
{#if note}<p class="mt-2 text-small text-muted">Built-in administrators are never refused by a workspace allowance; every other member is, even in a workspace an administrator owns. Disk is not counted. <a class="link" href="/docs#workspace-limits">How limits work</a></p>{/if}
