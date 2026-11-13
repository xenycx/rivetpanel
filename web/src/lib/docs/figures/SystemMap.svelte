<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
</script>

<Figure caption="One rivetpanel process holds the desired state and drives Docker on its own host. Websites are served by a separate listener on their own host names. Remote nodes dial out to the panel, so they need no open management port.">
	<div class="dg dg-map">
		<div class="dg-box dg-ops">
			<p class="dg-name"><Icon name="users" size={14} />People</p>
			<p class="dg-sub">Browser and API clients</p>
		</div>
		<div class="dg-arrow a-ops">HTTPS</div>

		<div class="dg-box dg-visit">
			<p class="dg-name"><Icon name="globe" size={14} />Visitors</p>
			<p class="dg-sub">Public pages and sites</p>
		</div>
		<div class="dg-arrow a-visit">HTTPS</div>

		<div class="dg-zone dg-host" data-k="panel">
			<p class="dg-zone-label"><Icon name="server" size={12} />Panel host</p>
			<div class="dg-box" data-k="panel">
				<p class="dg-name"><Icon name="panel" size={14} />rivetpanel</p>
				<div class="dg-chips">
					<span class="dg-chip">Interface and API</span>
					<span class="dg-chip">Sign-in and permissions</span>
					<span class="dg-chip">SQLite: desired state</span>
					<span class="dg-chip">Keys</span>
					<span class="dg-chip">Local runner</span>
				</div>
			</div>
			<div class="dg-box" data-k="panel">
				<p class="dg-name"><Icon name="globe" size={14} />Sites listener</p>
				<p class="dg-sub">Own host names, away from panel cookies</p>
			</div>
			<div class="dg-box" data-k="docker">
				<p class="dg-name"><Icon name="box" size={14} />Docker</p>
				<p class="dg-sub">One container per app; services on private networks</p>
			</div>
		</div>

		<div class="dg-arrow a-agent" data-dir="back">dials out<br />TLS 1.3</div>

		<div class="dg-zone dg-node" data-k="docker">
			<p class="dg-zone-label"><Icon name="network" size={12} />Remote node</p>
			<div class="dg-box" data-k="docker">
				<p class="dg-name"><Icon name="terminal" size={14} /><span class="dg-mono">rivet-agent</span></p>
				<p class="dg-sub">Runner, files, logs, console</p>
			</div>
			<div class="dg-box" data-k="docker">
				<p class="dg-name"><Icon name="box" size={14} />Docker</p>
				<p class="dg-sub">Apps placed on this node</p>
			</div>
		</div>
	</div>
	<p class="dg-legend" aria-hidden="true">
		<span style="--k: var(--color-data)"><i></i>The panel (panel badge)</span>
		<span style="--k: var(--color-run)"><i></i>Docker (Docker badge)</span>
	</p>
</Figure>
