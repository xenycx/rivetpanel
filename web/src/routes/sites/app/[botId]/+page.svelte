<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, ApiError } from '$lib/api/client';
	import Notice from '$lib/components/ui/Notice.svelte';

	// Notification links name a site's app; open the site it belongs to.
	let error = $state('');
	onMount(async () => {
		try {
			const r = await api<{ site_id: string }>('GET', `/sites/by-app/${page.params.botId}`);
			goto(`/sites/${r.site_id}?tab=deployments`, { replaceState: true });
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The site could not be found.';
		}
	});
</script>

{#if error}<Notice tone="fail" live>{error}</Notice>{:else}<p class="text-muted">Opening the site…</p>{/if}
