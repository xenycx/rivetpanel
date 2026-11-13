<script lang="ts">
	import { priorityLabel } from '$lib/api/support';

	// Priority as a drawn mark: urgent a double chevron up, high a chevron
	// up, normal two bars, low a chevron down. With `label` the word follows.
	let { priority, label = false, class: cls = '' }: { priority: string; label?: boolean; class?: string } = $props();
</script>

<span class="prio-mark {cls}" data-p={priority} title={label ? undefined : `${priorityLabel(priority)} priority`}>
	<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
		{#if priority === 'urgent'}
			<path d="M4 8.5 8 4.5l4 4" /><path d="M4 12.5 8 8.5l4 4" />
		{:else if priority === 'high'}
			<path d="M4 10.5 8 6.5l4 4" />
		{:else if priority === 'low'}
			<path d="M4 6.5 8 10.5l4-4" />
		{:else}
			<path d="M4.5 6.5h7" /><path d="M4.5 9.5h7" />
		{/if}
	</svg>
	{#if label}<span>{priorityLabel(priority)}</span>{:else}<span class="sr-only">{priorityLabel(priority)} priority</span>{/if}
</span>
