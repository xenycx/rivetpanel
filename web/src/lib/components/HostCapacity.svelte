<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fmtBytes, fmtCpu } from '$lib/api/client';
	import type { EnvVar, EnvView } from '$lib/api/admin';
	import type { Capacity, LimitDefaults } from '$lib/api/types';
	import CapacityMeter from '$lib/components/CapacityMeter.svelte';
	import { fmtAllowance, LIMITS_HELP, NO_LIMIT, sourceText, type LimitSource } from '$lib/limits';

	type Defaults = { limits: LimitDefaults; source: Partial<Record<keyof LimitDefaults, Exclude<LimitSource, 'workspace'>>> };
	let cap = $state<Capacity | null>(null);
	let vars = $state<Record<string, EnvVar>>({});
	let defaults = $state<Defaults | null>(null);
	onMount(async () => {
		try {
			cap = await api<Capacity>('GET', '/me/capacity');
		} catch {
			/* the table below still shows */
		}
		try {
			vars = Object.fromEntries((await api<EnvView>('GET', '/admin/environment')).vars.map((v) => [v.name, v]));
		} catch {
			/* optional */
		}
		try {
			defaults = await api<Defaults>('GET', '/admin/limits');
		} catch {
			/* needs Panel settings; the environment value shows instead */
		}
	});

	// The value in effect now (the running one while a change waits for a restart).
	const cur = (name: string) => {
		const v = vars[name];
		return v ? (v.pending ? v.running : v.value).trim() : '';
	};
	// Where an environment value comes from: the environment file, the
	// Environment page, or nothing set (the built-in value).
	const envSource = (name: string): string => {
		const v = vars[name];
		if (!v || !cur(name)) return sourceText.builtin;
		return v.source === 'panel' ? 'Environment page' : sourceText.env;
	};

	type Row = { term: string; name: string; scope: string; now: string; source: string; note?: string };
	const rows = $derived.by((): Row[] => {
		const node = cap?.node;
		const out: Row[] = [];
		const budget = node?.budget_bytes ?? Number(cur('RIVET_NODE_MEMORY_BYTES') || 0);
		out.push({ term: 'Node budget', name: 'RIVET_NODE_MEMORY_BYTES', scope: 'Each node, at start', now: budget > 0 ? fmtBytes(budget) : NO_LIMIT, source: envSource('RIVET_NODE_MEMORY_BYTES') });
		const memSet = Number(cur('RIVET_MAX_BOT_MEMORY_BYTES') || 0) > 0;
		const maxMem = node?.max_app_memory_bytes;
		out.push({
			term: 'Node maximum, memory',
			name: 'RIVET_MAX_BOT_MEMORY_BYTES',
			scope: 'One app on this node',
			now: maxMem ? fmtBytes(maxMem) : memSet ? fmtBytes(Number(cur('RIVET_MAX_BOT_MEMORY_BYTES'))) : "This node's memory",
			source: memSet ? envSource('RIVET_MAX_BOT_MEMORY_BYTES') : sourceText.builtin,
			note: memSet ? (maxMem && maxMem < Number(cur('RIVET_MAX_BOT_MEMORY_BYTES')) ? "Capped by this node's memory" : undefined) : "This node's memory, in whole 128 MiB steps"
		});
		const cpuSet = Number(cur('RIVET_MAX_BOT_NANO_CPUS') || 0) > 0;
		const maxCpu = node?.max_app_nano_cpus;
		out.push({
			term: 'Node maximum, CPU',
			name: 'RIVET_MAX_BOT_NANO_CPUS',
			scope: 'One app on this node',
			now: maxCpu ? fmtCpu(maxCpu) : cpuSet ? fmtCpu(Number(cur('RIVET_MAX_BOT_NANO_CPUS'))) : "This node's cores",
			source: cpuSet ? envSource('RIVET_MAX_BOT_NANO_CPUS') : sourceText.builtin,
			note: cpuSet ? undefined : "This node's core count"
		});
		// The default allowance: Panel settings first, this variable as the fallback.
		const allowance = (term: string, name: string, key: keyof LimitDefaults, kind: 'count' | 'memory', builtin: string) => {
			const src = defaults?.source[key];
			if (defaults && src) {
				const v = defaults.limits[key] as number | null;
				return { term, name, scope: 'Each workspace', now: fmtAllowance(kind, v), source: sourceText[src], note: src === 'panel' ? `Set under Panel settings; this variable is only the fallback` : undefined };
			}
			const raw = cur(name);
			const n = Number(raw);
			const now = raw === '' ? builtin : n === 0 ? NO_LIMIT : kind === 'memory' ? fmtBytes(n) : raw;
			return { term, name, scope: 'Each workspace', now, source: envSource(name) };
		};
		out.push(allowance('Default allowance, apps in total', 'RIVET_MAX_BOTS_PER_USER', 'max_apps', 'count', NO_LIMIT));
		out.push(allowance('Default allowance, memory', 'RIVET_USER_MEMORY_BYTES', 'memory_bytes', 'memory', NO_LIMIT));
		out.push(allowance('Default allowance, sites', 'RIVET_MAX_SITES_PER_USER', 'max_sites', 'count', '10'));
		const builds = cur('RIVET_MAX_BUILDS');
		out.push({ term: 'Concurrent builds', name: 'RIVET_MAX_BUILDS', scope: 'This host', now: builds || (vars.RIVET_MAX_BUILDS?.default ?? '1'), source: envSource('RIVET_MAX_BUILDS'), note: 'Further builds wait their turn' });
		const disk = cur('RIVET_MIN_FREE_DISK_BYTES');
		out.push({
			term: 'Minimum free disk',
			name: 'RIVET_MIN_FREE_DISK_BYTES',
			scope: 'This host',
			now: disk === '' ? '1 GiB' : Number(disk) === 0 ? 'Off' : fmtBytes(Number(disk)),
			source: envSource('RIVET_MIN_FREE_DISK_BYTES'),
			note: 'Backups, restores and deployments wait for it'
		});
		return out;
	});
</script>

{#if cap?.node}
	{@const node = cap.node}
	<h3 class="text-title font-semibold">Node budget</h3>
	<p class="mt-1 max-w-3xl text-small text-muted">The memory sizes of apps wanted running on this node, add-ons included, against the node budget. The panel refuses a start that would exceed it and says why; creating an app is not refused. This is bookkeeping for admission, not a kernel limit.</p>
	<div class="mt-3 grid gap-3 md:grid-cols-3">
		<div class="stat md:col-span-2">
			<CapacityMeter label="Reserved by running apps" value={node.reserved_bytes} max={node.budget_bytes} format={fmtBytes} />
			<p class="mt-2 text-small text-muted">
				{#if node.budget_bytes > 0}{fmtBytes(Math.max(0, node.budget_bytes - node.reserved_bytes))} left for new starts.{:else}No node budget is set, so starts are never refused for memory. To protect a small host from overcommitting, set <a class="link font-mono text-[12px]" href="/admin/environment?find=RIVET_NODE_MEMORY_BYTES">RIVET_NODE_MEMORY_BYTES</a> on the Environment page and restart the panel.{/if}
			</p>
		</div>
		<div class="stat">
			<p class="eyebrow">Apps wanted running</p>
			<p class="mt-1 font-mono text-section font-semibold">{node.running}</p>
			<p class="text-small text-muted">Each build may use up to {fmtBytes(cap.build_memory_bytes)} on top.</p>
		</div>
	</div>
{/if}

<h3 class="mt-8 text-title font-semibold">Limits in effect</h3>
<p class="mt-1 max-w-3xl text-small text-muted">Each value as it applies now, and where it comes from. Host and node values change on the Environment page and apply after a restart; the default allowance changes under <a class="link" href="/admin/settings#limits">Panel settings → Default allowance</a> at once. <a class="link" href={LIMITS_HELP}>How limits work</a></p>
<div class="card mt-3 overflow-x-auto">
	<table class="w-full min-w-[44rem] text-left">
		<thead class="border-b border-rule-soft"><tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal"><th class="eyebrow">Limit</th><th class="eyebrow">Applies to</th><th class="eyebrow text-right">Now</th><th class="eyebrow text-right">Source</th></tr></thead>
		<tbody class="divide-y divide-rule-soft text-small">
			{#each rows as r (r.name)}
				<tr class="align-top [&>td]:px-4 [&>td]:py-2.5">
					<td>
						<span class="font-medium">{r.term}</span>
						<a class="block font-mono text-[12px] text-muted hover:underline" href="/admin/environment?find={r.name}">{r.name}</a>
					</td>
					<td class="text-muted">{r.scope}</td>
					<td class="text-right">
						<span class="font-mono">{r.now}</span>{#if vars[r.name]?.pending}<span class="pill ml-2" data-tone="warn" title="Takes effect after a restart">pending</span>{/if}
						{#if r.note}<span class="block text-[12px] text-muted">{r.note}</span>{/if}
					</td>
					<td class="text-right text-muted">{r.source}</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>
<p class="mt-3"><a class="link text-small" href="/admin/environment">Open the Environment page</a></p>
