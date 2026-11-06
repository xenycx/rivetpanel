<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import { extensions, loadExtensions } from '$lib/extensions/store.svelte';
	import type { ExtContribution } from '$lib/extensions/types';
	import { toast } from '$lib/ui/toast.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// Extensions that act for people (Discord ChatOps) do so only with the
	// scopes each person allows here.
	const acting = $derived(extensions.list.filter((x) => x.consent));
	let chosen = $state<Record<string, Record<string, boolean>>>({});
	let saving = $state('');
	$effect(() => {
		for (const x of acting) if (!chosen[x.id]) chosen[x.id] = Object.fromEntries(x.consent!.scopes.map((s) => [s.id, x.consented.includes(s.id)]));
	});

	async function save(x: ExtContribution, scopes?: string[]) {
		const list = scopes ?? Object.entries(chosen[x.id] ?? {}).filter(([, on]) => on).map(([k]) => k);
		saving = x.id;
		try {
			await api('PUT', `/extensions/${encodeURIComponent(x.id)}/consent`, { scopes: list });
			if (scopes) chosen[x.id] = Object.fromEntries(x.consent!.scopes.map((s) => [s.id, false]));
			await loadExtensions(true);
			toast(list.length ? 'Saved' : `${x.name} can no longer act for you`, 'success');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The change could not be saved.', 'fail');
		} finally {
			saving = '';
		}
	}
</script>

<svelte:head><title>Extensions · Settings · RivetPanel</title></svelte:head>

{#if !extensions.loaded}
	<Skeleton rows={3} label="Loading" />
{:else if !acting.length}
	<EmptyState title="No extension acts for you">No enabled extension asks to act on your behalf.</EmptyState>
{:else}
	{#each acting as x (x.id)}
		<SettingsSection title={x.name} description={x.consent!.explanation}>
			<div class="grid max-w-xl gap-3">
				{#each x.consent!.scopes as s (s.id)}
					<label class="flex items-start gap-3">
						<input type="checkbox" class="mt-1" bind:checked={chosen[x.id][s.id]} />
						<span><span class="font-medium">{s.label}</span><span class="block text-small text-muted">{s.description}</span></span>
					</label>
				{/each}
				<p class="text-small text-muted">It always acts with your current panel permissions; allowing something here never gives you more.{#if x.consented.length}{' '}Allowed now: {x.consented.join(', ')}.{/if}</p>
				<div class="flex flex-wrap gap-2">
					<button class="btn btn-primary" disabled={saving === x.id} onclick={() => save(x)}>Save</button>
					{#if x.consented.length}<button class="btn btn-quiet" disabled={saving === x.id} onclick={() => save(x, [])}>Withdraw all</button>{/if}
				</div>
			</div>
		</SettingsSection>
	{/each}
{/if}
