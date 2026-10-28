<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { pct, type BotUsage, type BotsReport } from '$lib/api/admin';
	import { resourceHref } from '$lib/api/games';
	import { fmtAgo } from '$lib/args';
	import { poll } from '$lib/poll';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let rep = $state<BotsReport | null>(null);
	let error = $state('');
	type Key = 'name' | 'state' | 'cpu' | 'mem' | 'disk' | 'net' | 'pids';
	let sort = $state<Key>('mem');
	let desc = $state(true);
	let onlyRunning = $state(false);
	let filter = $state('');

	onMount(() =>
		poll(async () => {
			try {
				rep = await api<BotsReport>('GET', '/admin/host/bots');
				error = '';
			} catch (e) {
				error = e instanceof ApiError ? e.message : 'Per-app usage could not be read.';
			}
		}, 10_000)
	);

	const value: Record<Key, (b: BotUsage) => number | string> = {
		name: (b) => b.name.toLowerCase(),
		state: (b) => b.state,
		cpu: (b) => b.cpu_cores,
		mem: (b) => b.mem_used_bytes,
		disk: (b) => b.disk_bytes,
		net: (b) => b.net_rx_bytes + b.net_tx_bytes,
		pids: (b) => b.pids
	};
	const rows = $derived.by(() => {
		let r = rep?.bots ?? [];
		if (onlyRunning) r = r.filter((b) => b.running);
		const f = filter.trim().toLowerCase();
		if (f) r = r.filter((b) => `${b.name} ${b.owner} ${kindLabel(b)}${b.runtime}`.toLowerCase().includes(f));
		const get = value[sort];
		return [...r].sort((a, b) => {
			const x = get(a), y = get(b);
			const c = typeof x === 'string' ? x.localeCompare(y as string) : (x as number) - (y as number);
			return (desc ? -c : c) || a.name.localeCompare(b.name);
		});
	});
	function by(k: Key) {
		if (sort === k) desc = !desc;
		else {
			sort = k;
			desc = k !== 'name' && k !== 'state';
		}
	}
	const arrow = (k: Key) => (sort === k ? (desc ? ' ↓' : ' ↑') : '');
	const bar = (p: number) => (p >= 90 ? 'bg-fail' : p >= 75 ? 'bg-warn' : 'bg-run');
	const kindLabel = (b: BotUsage) => (b.kind === 'game' ? 'game server · ' : b.kind === 'web' ? 'web app · ' : '');
	const tone = (b: BotUsage) => (b.running ? 'run' : b.state === 'stopped' || b.state === 'exited' ? undefined : 'warn');
	const diskTotal = $derived(rep?.bots.reduce((a, b) => a + Math.max(b.disk_bytes, 0), 0) ?? 0);
</script>

{#if error}<Notice tone="fail" class="mb-4" live>{error}</Notice>{/if}
{#if !rep && !error}
	<Skeleton rows={5} label="Reading app usage" />
{:else if rep}
	<dl class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
		<div class="stat"><dt class="eyebrow">Running</dt><dd class="mt-1 font-mono text-section font-semibold">{rep.running}<span class="ml-2 text-small font-normal text-muted">of {rep.total} apps</span></dd></div>
		<div class="stat"><dt class="eyebrow">CPU in use</dt><dd class="mt-1 font-mono text-section font-semibold">{rep.cpu_cores.toFixed(2)}<span class="ml-2 text-small font-normal text-muted">cores, all apps</span></dd></div>
		<div class="stat"><dt class="eyebrow">Memory in use</dt><dd class="mt-1 font-mono text-section font-semibold">{fmtBytes(rep.mem_used_bytes)}<span class="ml-2 text-small font-normal text-muted">of {fmtBytes(rep.mem_reserved_bytes)} reserved</span></dd></div>
		<div class="stat"><dt class="eyebrow">Workspace files</dt><dd class="mt-1 font-mono text-section font-semibold">{fmtBytes(diskTotal)}<span class="ml-2 text-small font-normal text-muted">all apps</span></dd></div>
	</dl>
	{#if rep.truncated}<Notice tone="info" class="mt-3">At most 60 running apps are measured per refresh; the others are listed without live numbers.</Notice>{/if}

	<div class="mt-4 flex flex-wrap items-center gap-3">
		<label class="min-w-48 flex-1 sm:max-w-xs"><span class="sr-only">Filter apps</span><input class="field" placeholder="Filter by app, owner or runtime" bind:value={filter} /></label>
		<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={onlyRunning} />Only running</label>
		<span class="ml-auto text-small text-muted">Updated {fmtAgo(rep.generated_at_ms)} · refreshes every 10 seconds</span>
	</div>

	<div class="card mt-3 overflow-x-auto">
		<table class="w-full min-w-[52rem] text-left">
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal">
					{#each [['name', 'App'], ['state', 'State'], ['cpu', 'CPU'], ['mem', 'Memory'], ['net', 'Network total'], ['pids', 'Processes'], ['disk', 'Files']] as [k, l] (k)}
						<th class="eyebrow" aria-sort={sort === k ? (desc ? 'descending' : 'ascending') : 'none'}><button class="uppercase hover:text-ink" onclick={() => by(k as Key)}>{l}{arrow(k as Key)}</button></th>
					{/each}
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft text-small">
				{#each rows as b (b.id)}
					{@const mp = b.mem_limit_bytes ? pct(b.mem_used_bytes, b.mem_limit_bytes) : 0}
					{@const cp = b.cpu_limit_cores ? pct(b.cpu_cores, b.cpu_limit_cores) : 0}
					<tr class="[&>td]:px-4 [&>td]:py-2.5 hover:bg-paper/40">
						<td class="max-w-60">
							<a href={resourceHref(b)} class="block truncate font-medium hover:underline">{b.name}</a>
							<span class="block truncate font-mono text-[11px] text-muted">{kindLabel(b)}{b.runtime}{b.owner ? ` · ${b.owner}` : ''}</span>
						</td>
						<td><span class="pill" data-tone={tone(b)}>{b.running ? 'Running' : b.state}</span>{#if b.running && b.started_at_ms}<span class="block text-[11px] text-muted">since {fmtAgo(b.started_at_ms)}</span>{/if}</td>
						<td class="min-w-28">
							{#if b.measured}
								<span class="font-mono">{b.cpu_cores.toFixed(2)}</span><span class="text-muted"> / {b.cpu_limit_cores ? b.cpu_limit_cores.toFixed(2) : '∞'}</span>
								{#if b.cpu_limit_cores}<div class="mt-1 h-1 overflow-hidden rounded-pill bg-rule-soft"><div class="h-full {bar(cp)}" style="width: {cp}%"></div></div>{/if}
							{:else}<span class="text-muted">—</span>{/if}
						</td>
						<td class="min-w-36">
							{#if b.measured}
								<span class="font-mono">{fmtBytes(b.mem_used_bytes)}</span><span class="text-muted"> / {fmtBytes(b.mem_limit_bytes)}</span>
								<div class="mt-1 h-1 overflow-hidden rounded-pill bg-rule-soft"><div class="h-full {bar(mp)}" style="width: {mp}%"></div></div>
							{:else}<span class="font-mono text-muted">{fmtBytes(b.mem_limit_bytes)} limit</span>{/if}
						</td>
						<td class="font-mono">{#if b.measured}<span title="Received">↓ {fmtBytes(b.net_rx_bytes)}</span><br /><span title="Sent">↑ {fmtBytes(b.net_tx_bytes)}</span>{:else}<span class="text-muted">—</span>{/if}</td>
						<td class="font-mono">{b.measured ? b.pids : '—'}</td>
						<td class="font-mono">{b.disk_bytes < 0 ? '…' : fmtBytes(b.disk_bytes)}</td>
					</tr>
				{:else}
					<tr><td colspan="7" class="px-4 py-6 text-center text-muted">No apps match.</td></tr>
				{/each}
			</tbody>
		</table>
	</div>
	<p class="mt-3 max-w-3xl text-small text-muted">CPU is cores in use against the app's limit. Memory is what the container uses against its limit. Network totals count since the container last started. Workspace files are measured in the background about every two minutes.</p>
{/if}
