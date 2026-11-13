<script lang="ts">
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import Figure from './Figure.svelte';
	const steps: { icon: IconName; name: string; does: string; k: string }[] = [
		{ icon: 'check', name: 'Verify', does: 'Checksum and that the environment decrypts. Nothing has changed yet', k: 'panel' },
		{ icon: 'copy', name: 'Safety copy', does: 'A pre_restore backup of the current files', k: 'action' },
		{ icon: 'folder', name: 'Stage', does: 'Unpack, refusing .., absolute paths, devices, hard links and escaping links', k: 'panel' },
		{ icon: 'commit', name: 'Commit', does: 'The crash-safe swap deployments use, with verification recorded', k: 'panel' }
	];
</script>

<Figure caption="An app restore checks everything before it touches the app, and the previous files stay until the database records the restore, so a failure at any step leaves the app as it was.">
	<ol class="dg dg-flow">
		{#each steps as s, i (s.name)}
			{#if i}<li class="dg-arrow" aria-hidden="true"></li>{/if}
			<li class="dg-box" data-k={s.k}>
				<p class="dg-name"><Icon name={s.icon} size={14} />{i + 1}. {s.name}</p>
				<p class="dg-sub">{s.does}</p>
			</li>
		{/each}
	</ol>
</Figure>
