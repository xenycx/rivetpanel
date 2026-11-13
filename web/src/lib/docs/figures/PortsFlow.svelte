<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
</script>

<Figure caption="The panel reserves the port and publishes it on the host; reaching it from the internet is your router's job.">
	<div class="dg dg-flow">
		<div class="dg-box" data-k="panel">
			<p class="dg-name"><Icon name="list" size={14} />Allocation pool</p>
			<p class="dg-sub">IP:port reservations for each node</p>
		</div>
		<div class="dg-arrow">free port</div>
		<div class="dg-box" data-k="docker" style="flex-grow: 1.4">
			<p class="dg-name"><Icon name="gamepad" size={14} />Server on the host</p>
			<p class="dg-sub">Primary port published for TCP and UDP on <span class="dg-mono">0.0.0.0</span></p>
			<div class="dg-chips">
				<span class="dg-chip mono">SERVER_PORT</span>
				<span class="dg-chip mono">SERVER_PORT_1…10</span>
			</div>
		</div>
		<div class="dg-arrow" data-dir="back">you forward<br />TCP and UDP</div>
		<div class="dg-box" data-k="host">
			<p class="dg-name"><Icon name="network" size={14} />Your router</p>
			<p class="dg-sub">Not configured by RivetPanel</p>
		</div>
	</div>
</Figure>
