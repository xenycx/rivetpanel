<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	// The list lives in Apps now; filters and ?new=1 carry over.
	onMount(() => {
		const q = new URLSearchParams(location.search);
		q.set('kind', 'bot');
		goto(`/apps?${q}`, { replaceState: true });
	});
</script>
