<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
</script>

<Figure caption="A bot's public page is composed in the panel but served from the separate sites origin, so author HTML never runs next to a signed-in session.">
	<div class="dg dg-flow">
		<div class="dg-box" data-k="action">
			<p class="dg-name"><Icon name="discord" size={14} />Bot</p>
			<p class="dg-sub">Publishes validated widgets, and opts some in to public</p>
		</div>
		<div class="dg-arrow">telemetry</div>
		<div class="dg-box" data-k="panel">
			<p class="dg-name"><Icon name="pencil" size={14} />Page Studio</p>
			<p class="dg-sub">Composes copy, theme, author HTML and CSS</p>
		</div>
		<div class="dg-arrow">publish</div>
		<div class="dg-box" data-k="panel">
			<p class="dg-name"><Icon name="globe" size={14} />Sites origin</p>
			<p class="dg-sub">Its own host name, away from panel cookies</p>
		</div>
	</div>
	<dl class="dg dg-split">
		<div><dt class="text-run">Public</dt><dd>Page copy, author HTML and CSS, widgets the bot opted in</dd></div>
		<div><dt>Private</dt><dd>Secrets, console, events, command analytics, people and deployments</dd></div>
	</dl>
</Figure>
