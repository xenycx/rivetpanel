<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fmtBytes } from '$lib/api/client';
	import type { EnvVar, EnvView } from '$lib/api/admin';
	import type { Capacity } from '$lib/api/types';
	import CapacityMeter from '$lib/components/CapacityMeter.svelte';

	let cap = $state<Capacity | null>(null);
	let vars = $state<EnvVar[]>([]);
	onMount(async () => {
		try {
			cap = await api<Capacity>('GET', '/me/capacity');
		} catch {
			/* the budgets below still show */
		}
		try {
			const names = new Set(['RIVET_NODE_MEMORY_BYTES', 'RIVET_USER_MEMORY_BYTES', 'RIVET_MAX_BOTS_PER_USER', 'RIVET_MAX_BUILDS', 'RIVET_MIN_FREE_DISK_BYTES', 'RIVET_MAX_BOT_MEMORY_BYTES', 'RIVET_MAX_SITES_PER_USER']);
			vars = (await api<EnvView>('GET', '/admin/environment')).vars.filter((v) => names.has(v.name));
		} catch {
			/* optional */
		}
	});
	const show = (v: EnvVar) => {
		const n = Number(v.value);
		if (v.value === '' || v.value === '0') return 'unlimited';
		if (v.kind === 'bytes' && Number.isFinite(n)) return fmtBytes(n);
		return v.value;
	};
	const scope: Record<string, string> = { RIVET_NODE_MEMORY_BYTES: 'Host', RIVET_USER_MEMORY_BYTES: 'Account', RIVET_MAX_BOTS_PER_USER: 'Account', RIVET_MAX_BUILDS: 'Host', RIVET_MIN_FREE_DISK_BYTES: 'Host', RIVET_MAX_BOT_MEMORY_BYTES: 'Bot', RIVET_MAX_SITES_PER_USER: 'Account' };
	const srcText = { default: 'Default', environment: 'Environment file', panel: 'Set here' } as const;
</script>

{#if cap?.node}
	{@const node = cap.node}
	<h3 class="text-title font-semibold">Capacity</h3>
	<p class="mt-1 max-w-3xl text-small text-muted">Memory limits of bots that are wanted running, counted against the admission budget. A start that would exceed it is refused with an explanation. This is bookkeeping for admission, not a kernel limit.</p>
	<div class="mt-3 grid gap-3 md:grid-cols-3">
		<div class="stat md:col-span-2">
			<CapacityMeter label="Reserved memory" value={node.reserved_bytes} max={node.budget_bytes} format={fmtBytes} />
			<p class="mt-2 text-small text-muted">
				{#if node.budget_bytes > 0}{fmtBytes(Math.max(0, node.budget_bytes - node.reserved_bytes))} left for new starts.{:else}No node budget is set, so starts are never refused for memory. Set a node memory budget below to protect a small host from overcommitting.{/if}
			</p>
		</div>
		<div class="stat">
			<p class="eyebrow">Bots wanted running</p>
			<p class="mt-1 font-mono text-section font-semibold">{node.running}</p>
			<p class="text-small text-muted">Each build may use up to {fmtBytes(cap.build_memory_bytes)} on top.</p>
		</div>
	</div>
{/if}

<h3 class="mt-8 text-title font-semibold">Budgets and limits</h3>
<p class="mt-1 max-w-3xl text-small text-muted">0 means unlimited. Administrators are exempt from per-account limits. Changes are made on the Environment page and apply after a restart.</p>
<div class="card mt-3 overflow-x-auto">
	<table class="w-full min-w-[40rem] text-left">
		<thead class="border-b border-rule-soft"><tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal"><th class="eyebrow">Setting</th><th class="eyebrow">Limits</th><th class="eyebrow">Scope</th><th class="eyebrow text-right">Now</th><th class="eyebrow text-right">From</th></tr></thead>
		<tbody class="divide-y divide-rule-soft text-small">
			{#each vars as v (v.name)}
				<tr class="[&>td]:px-4 [&>td]:py-2.5">
					<td><a class="font-mono text-[12px] hover:underline" href="/admin/environment?find={v.name}">{v.name}</a></td>
					<td>{v.description}</td>
					<td class="text-muted">{scope[v.name]}</td>
					<td class="text-right font-mono">{show(v)}{#if v.pending}<span class="pill ml-2" data-tone="warn" title="Takes effect after a restart">pending</span>{/if}</td>
					<td class="text-right text-muted">{srcText[v.source]}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
<p class="mt-3"><a class="link text-small" href="/admin/environment">Open the Environment page</a></p>
