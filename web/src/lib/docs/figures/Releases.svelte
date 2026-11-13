<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
	const older = [6, 5, 4, 3];
</script>

<Figure caption="Every publish makes a new immutable release and the site serves exactly one. Five are kept: the serving one, never pruned, and up to four earlier ones to roll back to.">
	<div class="dg dg-flow">
		<div class="dg-box" data-k="muted" style="flex: 0 1 11rem">
			<p class="dg-name"><Icon name="pencil" size={14} />Sources</p>
			<div class="dg-chips">
				<span class="dg-chip">Site files draft</span>
				<span class="dg-chip">ZIP upload</span>
				<span class="dg-chip">GitHub branch</span>
			</div>
		</div>
		<div class="dg-arrow">Publish</div>
		<div class="dg-zone" data-k="panel" style="flex: 3 1 0">
			<p class="dg-zone-label"><Icon name="archive" size={12} />Releases of one site</p>
			<ol class="dg-releases">
				<li class="serving"><span class="dg-mono">#7</span><strong>Serving</strong><span>Visitors get these files</span></li>
				{#each older as n (n)}<li><span class="dg-mono">#{n}</span><strong>Kept</strong><span>Serve this</span></li>{/each}
			</ol>
		</div>
	</div>
</Figure>
