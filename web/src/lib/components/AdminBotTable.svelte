<script lang="ts">
	import { resourceHref } from '$lib/api/games';
	import { fmtBytes } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { describe } from '$lib/status';

	// Bots in administrator overviews: state, owner and footprint at a glance.
	let { bots, showOwner = true, empty = 'No bots.' }: { bots: (Bot & { owner_email?: string })[]; showOwner?: boolean; empty?: string } = $props();
</script>

{#if bots.length}
	<div class="card overflow-x-auto">
		<table class="w-full min-w-[36rem] text-left">
			<thead class="border-b border-rule-soft">
				<tr class="[&>th]:px-4 [&>th]:py-2.5 [&>th]:font-normal">
					<th class="eyebrow">Bot</th>
					<th class="eyebrow">State</th>
					{#if showOwner}<th class="eyebrow">Owner</th>{/if}
					<th class="eyebrow text-right">Memory</th>
					<th class="eyebrow text-right">Last start</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each bots as b (b.id)}
					{@const d = describe(b)}
					<tr class="[&>td]:px-4 [&>td]:py-2.5 hover:bg-paper/40">
						<td class="max-w-64">
							<a href={resourceHref(b)} class="flex min-w-0 items-center gap-2 font-medium hover:underline">
								{#if b.logo_url}<img src={b.logo_url} alt="" class="size-6 shrink-0 rounded-pill object-cover" referrerpolicy="no-referrer" />{/if}
								<span class="truncate">{b.name}</span>
							</a>
							<span class="block truncate font-mono text-[11px] text-muted">{b.runtime} · {b.source_type}</span>
						</td>
						<td><span class="pill" data-tone={d.tone} title={d.detail || undefined}>{d.label}</span></td>
						{#if showOwner}<td class="max-w-56 truncate text-small">{b.owner_email ?? b.owner_id}</td>{/if}
						<td class="text-right font-mono text-small">{fmtBytes(b.memory_bytes)}</td>
						<td class="text-right text-small text-muted">{b.last_started_at_ms ? fmtAgo(b.last_started_at_ms) : 'never'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{:else}
	<p class="card px-4 py-4 text-small text-muted">{empty}</p>
{/if}
