<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { OpPage, Operation } from '$lib/api/types';
	import OperationRow from '$lib/components/OperationRow.svelte';

	let { botId, canOutput = true, isGame = false }: { botId: string; canOutput?: boolean; isGame?: boolean } = $props();

	let ops = $state<Operation[] | null>(null);
	let now = $state(Date.now());
	// Which rows start expanded: the requested one, else the newest failed or running build.
	let openFirst = $state('');

	async function load() {
		try {
			ops = (await api<OpPage>('GET', `/bots/${botId}/operations?limit=6`)).operations;
			if (!openFirst) {
				const first = ops.find((o) => o.kind === 'build');
				openFirst = page.url.searchParams.get('op') ?? (first && (first.status === 'failed' || first.status === 'running') ? first.id : '-');
			}
		} catch {
			ops = [];
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') load();
		}, 3000);
		return () => clearInterval(t);
	});
</script>

<section aria-labelledby="ov-recent">
	<div class="flex items-baseline justify-between gap-2">
		<h3 id="ov-recent" class="text-title font-semibold">Recent activity</h3>
		<a class="link text-small" href="/activity?bot={botId}">All activity</a>
	</div>
	{#if ops === null}
		<p class="mt-2 text-muted">Loading…</p>
	{:else if ops.length === 0}
		<p class="mt-2 text-muted">{isGame ? 'Installations, backups and restores' : 'Builds, deployments, backups and restores'} appear here once they run.</p>
	{:else}
		<ul class="mt-2 list-card">
			{#each ops as op (op.id)}
				<OperationRow {op} {now} {canOutput} initiallyOpen={op.id === openFirst} />
			{/each}
		</ul>
	{/if}
</section>
