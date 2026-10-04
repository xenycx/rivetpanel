<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { can, Perm, type Bot, type RuntimeInfo } from '$lib/api/types';
	import { power } from '$lib/api/bots';
	import { resourceHref, type Blueprint, type GameStatus } from '$lib/api/games';
	import { connectInfo, loadGameHosts } from '$lib/connect.svelte';
	import { describe } from '$lib/status';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import StatusBadge from '$lib/components/ui/StatusBadge.svelte';
	import GameIcon from '$lib/components/ui/GameIcon.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import type { Snippet } from 'svelte';

	// One table for bots and game servers: a header row, one row per item,
	// and a single quiet row when there is nothing to list. Columns collapse
	// to name, state and actions on narrow screens.
	let {
		items,
		kind,
		now,
		label,
		blueprints = {},
		runtimes = [],
		players = {},
		selected = $bindable(),
		note = () => '',
		onChanged = () => {},
		onTag,
		empty
	}: {
		items: Bot[];
		kind: 'bot' | 'game';
		now: number;
		label: string;
		blueprints?: Record<string, Blueprint>;
		runtimes?: RuntimeInfo[];
		players?: Record<string, GameStatus | undefined>;
		/** When bound, rows get a checkbox for batch actions. */
		selected?: Record<string, boolean>;
		/** Extra text after the type/source line (workspace, sharing). */
		note?: (b: Bot) => string;
		onChanged?: () => void;
		onTag?: (t: string) => void;
		empty: Snippet;
	} = $props();

	let busy = $state<Record<string, boolean>>({});
	$effect(() => {
		if (kind === 'game') loadGameHosts();
	});
	const runner = $derived(session.features.runner);
	const runtimeName = (id: string) => runtimes.find((r) => r.id === id)?.display_name ?? id;
	const runtimeTag: Record<string, string> = { nodejs: 'JS', python: 'PY', rust: 'RS', go: 'GO', java: 'JV', ruby: 'RB' };
	const typeLine = (b: Bot) =>
		kind === 'game'
			? (blueprints[b.blueprint_id ?? '']?.name ?? 'Game server')
			: b.source_type === 'github'
				? `GitHub, ${runtimeName(b.runtime)}`
				: b.source_type === 'template'
					? `${runtimeName(b.runtime)} template`
					: runtimeName(b.runtime);

	async function act(b: Bot, a: 'start' | 'stop' | 'restart' | 'kill') {
		busy[b.id] = true;
		if (await power(b, a)) onChanged();
		busy[b.id] = false;
	}
	async function star(b: Bot) {
		const on = !b.favorite;
		b.favorite = on;
		try {
			await api('PUT', `/bots/${b.id}/favorite`, { favorite: on });
		} catch (e) {
			b.favorite = !on;
			toast(e instanceof ApiError ? e.message : 'The favorite could not be saved.', 'fail');
		}
	}
	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast('Address copied', 'success');
		} catch {
			toast(text, 'info');
		}
	}
	function menuFor(b: Bot): MenuItem[] {
		const href = resourceHref(b);
		const items: MenuItem[] = [{ label: 'Open', onselect: () => goto(href) }];
		if (can(b, Perm.console)) items.push({ label: 'Console', onselect: () => goto(`${href}?tab=manage`) });
		if (can(b, Perm.files)) items.push({ label: 'Files', onselect: () => goto(`${href}?tab=files`) });
		items.push({ label: b.favorite ? 'Remove from favorites' : 'Add to favorites', onselect: () => star(b) });
		if (can(b, Perm.power) && runner) {
			items.push('separator');
			if (b.phase === 'failed' || b.phase === 'exited') items.push({ label: 'Start again', onselect: () => act(b, 'start') }, { label: 'Stop', onselect: () => act(b, 'stop') });
			else if (b.desired_state === 'running') items.push({ label: 'Restart', onselect: () => act(b, 'restart') }, { label: 'Stop', onselect: () => act(b, 'stop') });
			else items.push({ label: 'Start', onselect: () => act(b, 'start') });
			items.push({ label: 'Kill', danger: true, onselect: () => act(b, 'kill'), hint: 'Ends the process immediately' });
		}
		return items;
	}
	const grid = $derived(
		kind === 'game'
			? selected
				? 'grid-cols-[1.25rem_minmax(0,1fr)_auto_auto] md:grid-cols-[1.25rem_minmax(0,1.5fr)_minmax(0,1.2fr)_minmax(0,1fr)_5rem_5rem_4.5rem]'
				: 'grid-cols-[minmax(0,1fr)_auto_auto] md:grid-cols-[minmax(0,1.5fr)_minmax(0,1.2fr)_minmax(0,1fr)_5rem_5rem_4.5rem]'
			: selected
				? 'grid-cols-[1.25rem_minmax(0,1fr)_auto_auto] md:grid-cols-[1.25rem_minmax(0,1.5fr)_minmax(0,1.4fr)_9rem_4.5rem_4.5rem]'
				: 'grid-cols-[minmax(0,1fr)_auto_auto] md:grid-cols-[minmax(0,1.5fr)_minmax(0,1.4fr)_9rem_4.5rem_4.5rem]'
	);
	const selectable = $derived(selected ? items.filter((b) => can(b, Perm.power) && runner) : []);
	const allSelected = $derived(selectable.length > 0 && selectable.every((b) => selected?.[b.id]));
</script>

<div class="rows" role="table" aria-label={label}>
	<div class="rows-head hidden items-center gap-x-4 px-4 py-2 {items.length ? 'md:grid' : ''} {grid}" role="row">
		{#if selected}
			<span role="columnheader"><input type="checkbox" aria-label="Select all {selectable.length} shown" checked={allSelected} disabled={!selectable.length} onchange={(e) => { const on = e.currentTarget.checked; for (const b of selectable) selected![b.id] = on; }} /></span>
		{/if}
		<span role="columnheader">Name</span>
		<span role="columnheader">Status</span>
		{#if kind === 'game'}<span role="columnheader">Address</span><span role="columnheader">Players</span>{/if}
		<span role="columnheader">{kind === 'game' ? 'Memory' : 'Memory, CPU'}</span>
		{#if kind === 'bot'}<span role="columnheader">Restarts</span>{/if}
		<span role="columnheader" class="sr-only">Actions</span>
	</div>
	{#each items as b (b.id)}
		{@const d = describe(b, now)}
		{@const href = resourceHref(b)}
		{@const ci = kind === 'game' ? connectInfo(b, blueprints[b.blueprint_id ?? '']?.spec) : null}
		{@const addr = ci?.short ?? ''}
		{@const st = players[b.id]}
		<div class="row-link grid items-center gap-x-4 px-4 py-2.5 {grid}" role="row">
			{#if selected}
				<span role="cell">{#if can(b, Perm.power) && runner}<input type="checkbox" aria-label="Select {b.name}" bind:checked={selected[b.id]} />{/if}</span>
			{/if}
			<div class="flex min-w-0 items-center gap-3" role="cell">
				{#if b.logo_url}
					<img src={b.logo_url} alt="" class="size-8 shrink-0 rounded-control object-cover" referrerpolicy="no-referrer" />
				{:else if kind === 'game'}
					<GameIcon type={blueprints[b.blueprint_id ?? '']} size={32} />
				{:else}
					<span class="grid size-8 shrink-0 place-items-center rounded-control bg-paper-2 font-mono text-[11px] font-medium text-muted" aria-hidden="true">{b.template_id === 'discordts' ? 'TS' : (runtimeTag[b.runtime] ?? b.runtime.slice(0, 2).toUpperCase())}</span>
				{/if}
				<div class="min-w-0">
					<p class="flex min-w-0 items-center gap-1.5">
						<a {href} class="truncate font-medium hover:underline">{b.name}</a>
						{#if b.favorite}<span class="shrink-0 text-small text-muted" title="Favorite" aria-label="Favorite">★</span>{/if}
					</p>
					<p class="truncate text-small text-muted">{typeLine(b)}{note(b) ? `, ${note(b)}` : ''}</p>
					{#if b.tags.length && onTag}
						<p class="mt-0.5 flex flex-wrap gap-1">
							{#each b.tags as t (t)}<button class="rounded-inner border border-rule-soft px-1.5 text-[11px] text-muted hover:border-rule hover:text-ink" onclick={() => onTag(t)} aria-label="Show items tagged {t}">{t}</button>{/each}
						</p>
					{/if}
				</div>
			</div>
			<div class="min-w-0" role="cell">
				<StatusBadge bot={b} {now} size="sm" />
				{#if d.detail && d.tone !== 'run' && d.tone !== 'idle'}<p class="hidden truncate text-small text-muted md:block" title={d.detail}>{d.detail}</p>{/if}
			</div>
			{#if kind === 'game'}
				<div class="hidden min-w-0 md:block" role="cell">
					{#if addr}<button class="flex max-w-full items-center gap-1.5 font-mono text-small hover:underline" onclick={() => copy(ci?.full ?? addr)} title="Copy {ci?.full}{ci && ci.reach !== 'public' ? (ci.reach === 'local' ? ' (only reachable from this machine)' : ' (only reachable on your network)') : ''}"><span class="truncate">{addr}</span><Icon name="copy" size={12} class="shrink-0 text-muted" /></button>{:else}<span class="text-small text-muted">–</span>{/if}
				</div>
				<div class="hidden font-mono text-small md:block" role="cell">{st?.online ? `${st.players} / ${st.max_players}` : '–'}</div>
				<div class="hidden font-mono text-small md:block" role="cell">{fmtBytes(b.memory_bytes)}</div>
			{:else}
				<div class="hidden font-mono text-small md:block" role="cell">{fmtBytes(b.memory_bytes)}<span class="text-muted">, {fmtCpu(b.nano_cpus)}</span></div>
				<div class="hidden font-mono text-small md:block" role="cell">{b.restart_count ?? 0}</div>
			{/if}
			<div class="flex items-center justify-end gap-0.5" role="cell">
				{#if can(b, Perm.power) && runner}
					{#if b.phase === 'failed' || b.phase === 'exited' || b.desired_state !== 'running'}
						<button class="btn btn-quiet btn-icon btn-sm" disabled={busy[b.id]} onclick={() => act(b, 'start')} aria-label="Start {b.name}" title="Start"><Icon name="play" size={12} /></button>
					{:else}
						<button class="btn btn-quiet btn-icon btn-sm" disabled={busy[b.id]} onclick={() => act(b, 'stop')} aria-label="Stop {b.name}" title="Stop"><Icon name="stop" size={12} /></button>
					{/if}
				{/if}
				<Menu label="Actions for {b.name}" items={menuFor(b)} triggerClass="btn btn-quiet btn-icon btn-sm" />
			</div>
		</div>
	{:else}
		<div class="rows-empty" role="row"><span role="cell" class="contents">{@render empty()}</span></div>
	{/each}
</div>
