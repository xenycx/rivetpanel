<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
	const layers = [
		{ k: 'panel', name: 'Panel', items: 'App authorization, roles, workspace allowances, the separate sites origin, encrypted secrets' },
		{ k: 'docker', name: 'Container runtime', items: 'Non-root containers, read-only runtime, dropped capabilities, memory, CPU and process limits' },
		{ k: 'host', name: 'Host (your job)', items: 'Egress policy, disk quotas, IO and bandwidth shaping, a dedicated host for untrusted tenants' }
	];
</script>

<Figure caption="Each control lives in exactly one layer. The bottom layer is configuration the panel records but cannot enforce; the host has to.">
	<ol class="grid gap-2">
		{#each layers as l, i (l.k)}
			<li class="doc-layer" data-k={l.k} style:margin-inline={`${i * 0.75}rem`}>
				<span class="flex items-center gap-2 font-medium"><Icon name={l.k === 'host' ? 'alert' : 'shield'} size={14} />{l.name}</span>
				<span class="text-small text-muted">{l.items}</span>
			</li>
		{/each}
	</ol>
</Figure>
