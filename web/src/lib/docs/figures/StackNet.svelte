<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
</script>

<Figure caption="A stack's services share one private network and reach each other by service name on any port. Only ports you publish leave it, on 127.0.0.1 unless an administrator allows public binding.">
	<div class="dg dg-flow">
		<div class="dg-box" data-k="panel" style="flex: 0 1 8rem">
			<p class="dg-name"><Icon name="server" size={14} />Host</p>
			<p class="dg-sub"><span class="dg-mono">127.0.0.1:8080</span></p>
		</div>
		<div class="dg-arrow">published<br />port</div>
		<div class="dg-zone" data-k="docker" style="flex: 3 1 0">
			<p class="dg-zone-label"><Icon name="network" size={12} />Stack network · private</p>
			<div class="dg-flow">
				<div class="dg-box" data-k="docker"><p class="dg-name"><Icon name="globe" size={14} /><span class="dg-mono">web</span></p><p class="dg-sub">Listens on <span class="dg-mono">:80</span></p></div>
				<div class="dg-arrow"><span class="dg-mono">api:3000</span></div>
				<div class="dg-box" data-k="docker"><p class="dg-name"><Icon name="code" size={14} /><span class="dg-mono">api</span></p><p class="dg-sub">Starts after <span class="dg-mono">db</span> runs</p></div>
				<div class="dg-arrow"><span class="dg-mono">db:5432</span></div>
				<div class="dg-box" data-k="docker"><p class="dg-name"><Icon name="layers" size={14} /><span class="dg-mono">db</span></p><p class="dg-sub">Volume for its data</p></div>
			</div>
		</div>
	</div>
</Figure>
