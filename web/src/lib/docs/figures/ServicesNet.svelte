<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
</script>

<Figure caption="Each database runs in its own container on a private network that only its app joins. The app keeps its normal network; the services have no route out.">
	<div class="dg dg-flow">
		<div class="dg-box" data-k="muted" style="flex: 0 1 7.5rem">
			<p class="dg-name"><Icon name="globe" size={14} />Internet</p>
			<p class="dg-sub">Discord, APIs</p>
		</div>
		<div class="dg-svc-arrows">
			<div class="dg-arrow" data-dir="back">your app</div>
			<div class="dg-arrow" data-dir="back" data-blocked>services</div>
		</div>
		<div class="dg-zone" data-k="docker" style="flex: 2.4 1 0">
			<p class="dg-zone-label"><Icon name="network" size={12} /><span class="dg-mono">rivetpanel-&lt;app&gt;-net</span> · internal</p>
			<div class="dg-flow">
				<div class="dg-box" data-k="action">
					<p class="dg-name"><Icon name="box" size={14} />Your app</p>
					<div class="dg-chips">
						<span class="dg-chip mono">DATABASE_URL</span>
						<span class="dg-chip mono">REDIS_URL</span>
					</div>
				</div>
				<div class="dg-arrow">by host name</div>
				<div class="grid gap-2" style="flex: 1 1 0">
					<div class="dg-box" data-k="docker"><p class="dg-name"><Icon name="layers" size={14} /><span class="dg-mono">postgres</span></p><p class="dg-sub">Own data folder, password</p></div>
					<div class="dg-box" data-k="docker"><p class="dg-name"><Icon name="layers" size={14} /><span class="dg-mono">redis</span></p><p class="dg-sub">The network is its only guard</p></div>
				</div>
			</div>
		</div>
	</div>
</Figure>
