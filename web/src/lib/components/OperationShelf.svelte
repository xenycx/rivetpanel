<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { OpPage, Operation } from '$lib/api/types';
	import { elapsed, opNoun, opVerb, opHref } from '$lib/ops';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// Active long-running work across the user's bots, visible on every page.
	// It reads the durable operation records, so it survives navigation and
	// reloads; finished work produces a toast and stays in Activity.
	let ops = $state<Operation[]>([]);
	let now = $state(Date.now());
	let collapsed = $state(false);
	let seen = new Map<string, Operation>();
	let first = true;

	async function load() {
		let list: Operation[];
		try {
			list = (await api<OpPage>('GET', '/operations?active=1&limit=20')).operations;
		} catch {
			return; // unavailable (e.g. signed out); try again later
		}
		const ids = new Set(list.map((o) => o.id));
		for (const [id, o] of seen) {
			if (ids.has(id)) continue;
			seen.delete(id);
			if (first) continue;
			// Finished since the last look: report the outcome.
			api<Operation>('GET', `/bots/${o.bot_id}/operations/${id}`)
				.then((f) => {
					const what = `${opNoun(f)} of ${f.bot_name}`;
					if (f.status === 'succeeded') toast(`${what} finished`, 'success', { label: 'View', href: opHref(f) });
					else if (f.status === 'failed') toast(`${what} failed${f.message ? `: ${f.message}` : ''}`, 'fail', { label: 'Details', href: opHref(f) });
					else if (f.status === 'cancelled') toast(`${what} was cancelled`, 'info');
				})
				.catch(() => {});
		}
		for (const o of list) seen.set(o.id, o);
		first = false;
		ops = list;
	}

	onMount(() => {
		let stop = false;
		(async () => {
			while (!stop) {
				if (document.visibilityState === 'visible') await load();
				now = Date.now();
				await new Promise((r) => setTimeout(r, ops.length ? 2500 : 8000));
			}
		})();
		return () => (stop = true);
	});

	// The bot page already shows its own work prominently; the shelf is for elsewhere.
	const here = $derived(/^\/(bots|servers)\//.test(page.url.pathname) ? page.url.pathname.split('/')[2] : '');
	const shown = $derived(ops.filter((o) => o.bot_id !== here));
</script>

{#if shown.length}
	<aside class="pb-safe fixed inset-x-0 bottom-0 z-30 border-t border-rule-soft bg-raised/95 shadow-overlay backdrop-blur-sm sm:right-3 sm:bottom-3 sm:left-auto sm:w-96 sm:border sm:pb-0" aria-label="Work in progress">
		<button class="flex w-full items-center gap-2 px-3 py-2 text-left" aria-expanded={!collapsed} onclick={() => (collapsed = !collapsed)}>
			<span class="size-2 bg-warn" aria-hidden="true"></span>
			<span class="flex-1 font-medium">{shown.length} operation{shown.length === 1 ? '' : 's'} in progress</span>
			<Icon name={collapsed ? 'chevronDown' : 'chevronDown'} class={collapsed ? 'rotate-180' : ''} />
		</button>
		{#if !collapsed}
			<ul class="max-h-56 overflow-y-auto border-t border-rule-soft">
				{#each shown.slice(0, 6) as o (o.id)}
					<li class="spine border-b border-rule-soft last:border-b-0" data-tone="warn" data-busy="true">
						<a href={opHref(o)} class="block py-2 pr-3 pl-5 hover:bg-paper">
							<span class="block truncate">{opVerb(o)} <span class="font-medium">{o.bot_name}</span></span>
							<span class="block truncate text-small text-muted">{o.stage || 'Waiting to start'}, {elapsed(o, now)}</span>
						</a>
					</li>
				{/each}
			</ul>
		{/if}
	</aside>
{/if}
