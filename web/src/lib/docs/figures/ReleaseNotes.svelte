<script lang="ts">
	import releases from 'virtual:docs/changelog';
	import Blocks from '../Blocks.svelte';
	import Inlines from '../Inlines.svelte';
	// Built from CHANGELOG.md, so this page never needs editing for a release.
</script>

<div class="grid gap-8">
	{#each releases as r (r.version)}
		<section aria-labelledby={'rel-' + r.version} class="doc-release">
			<h3 id={'rel-' + r.version} class="doc-h3 !mt-0 flex flex-wrap items-baseline gap-x-2">
				{r.version === 'Unreleased' ? 'In progress' : r.version}
				{#if r.date}<time class="text-small font-normal text-muted" datetime={r.date}>{new Date(r.date + 'T00:00:00').toLocaleDateString(undefined, { dateStyle: 'long' })}</time>{/if}
			</h3>
			{#each r.blocks as b, i (i)}
				{#if b.t === 'h'}<h4 class="doc-release-kind"><Inlines c={b.c} /></h4>{:else}<Blocks blocks={[b]} />{/if}
			{/each}
		</section>
	{/each}
</div>
