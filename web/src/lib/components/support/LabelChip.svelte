<script lang="ts">
	import type { Label } from '$lib/api/support';

	// A staff label, tinted with its palette color (see app.css, .tag-chip).
	let { label, class: cls = '' }: { label: Pick<Label, 'name' | 'color'>; class?: string } = $props();
</script>

<span class="tag-chip {cls}" data-c={label.color}>{label.name}</span>
