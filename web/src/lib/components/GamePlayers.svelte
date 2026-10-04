<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fetchText } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import type { GameStatus } from '$lib/api/games';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';

	let { bot }: { bot: Bot } = $props();

	type Named = { name: string; uuid?: string; level?: number; reason?: string };
	let status = $state<GameStatus | null>(null);
	let ops = $state<Named[]>([]);
	let whitelist = $state<Named[]>([]);
	let banned = $state<Named[]>([]);
	let busy = $state(false);
	const running = $derived(bot.observed_state === 'running');
	const canAct = $derived(can(bot, Perm.power));
	const canRead = $derived(can(bot, Perm.files));
	const nameRe = /^[A-Za-z0-9_]{1,16}$/;

	async function readList(file: string): Promise<Named[]> {
		try {
			const r = JSON.parse(await fetchText(`/bots/${bot.id}/files/content?path=${encodeURIComponent(file)}`));
			return Array.isArray(r) ? r.filter((x) => typeof x?.name === 'string').slice(0, 500) : [];
		} catch {
			return [];
		}
	}
	async function load() {
		if (running) {
			try {
				status = await api<GameStatus>('GET', `/bots/${bot.id}/game/query`);
			} catch {
				status = null;
			}
		} else status = null;
		if (canRead) [ops, whitelist, banned] = await Promise.all([readList('ops.json'), readList('whitelist.json'), readList('banned-players.json')]);
	}
	onMount(() => {
		load();
		const t = setInterval(() => document.visibilityState === 'visible' && load(), 15000);
		return () => clearInterval(t);
	});

	async function send(command: string, done: string) {
		busy = true;
		try {
			await api('POST', `/bots/${bot.id}/command`, { command });
			toast(done, 'success');
			setTimeout(load, 1500); // the server writes its JSON files shortly after
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The command could not be sent.', 'fail');
		} finally {
			busy = false;
		}
	}
	async function ask(title: string, body: string): Promise<string | null> {
		const v = await promptDialog({ title, body, label: 'Player name', placeholder: 'Steve', confirmLabel: 'Continue', validate: (s) => (nameRe.test(s.trim()) ? null : 'Player names are 1-16 letters, digits or underscores.') });
		return v === null ? null : v.trim();
	}
	async function addTo(kind: 'op' | 'whitelist' | 'ban') {
		const n = await ask(kind === 'op' ? 'Make a player an operator' : kind === 'whitelist' ? 'Add to the whitelist' : 'Ban a player', kind === 'op' ? 'Operators can use every command, including stopping the server.' : kind === 'ban' ? 'The player is disconnected and cannot join again until pardoned.' : 'The player can join while the whitelist is on.');
		if (!n) return;
		if (kind === 'op') await send(`op ${n}`, `${n} is now an operator`);
		else if (kind === 'whitelist') await send(`whitelist add ${n}`, `${n} added to the whitelist`);
		else await send(`ban ${n}`, `${n} was banned`);
	}
	async function kick(n: string) {
		if (await confirmDialog({ title: `Kick ${n}?`, body: 'The player is disconnected and can join again.', confirmLabel: 'Kick' })) await send(`kick ${n}`, `${n} was kicked`);
	}
	async function ban(n: string) {
		if (await confirmDialog({ title: `Ban ${n}?`, body: 'The player is disconnected and cannot join again until pardoned.', confirmLabel: 'Ban', tone: 'danger' })) await send(`ban ${n}`, `${n} was banned`);
	}
</script>

{#if !running}
	<Notice tone="info" class="mb-4">The server is not running. Player actions are sent through its console, so start it to make changes; the lists below come from its files.</Notice>
{/if}

<div class="grid gap-4 lg:grid-cols-2">
	<section class="card p-5">
		<h2 class="text-section font-semibold">Online now {#if status?.online}<span class="text-small font-normal text-muted">· {status.players} of {status.max_players}</span>{/if}</h2>
		{#if status?.online && status.sample.length}
			<ul class="mt-3 divide-y divide-rule-soft">
				{#each status.sample as n (n)}
					<li class="flex items-center gap-2 py-2">
						<span class="grid size-6 place-items-center rounded bg-paper-2 text-[11px] font-semibold text-muted" aria-hidden="true">{n.slice(0, 1).toUpperCase()}</span>
						<span class="flex-1 font-medium">{n}</span>
						{#if canAct}
							<button class="btn btn-sm" disabled={busy} onclick={() => kick(n)}>Kick</button>
							<button class="btn btn-sm btn-quiet text-fail" disabled={busy} onclick={() => ban(n)}>Ban</button>
						{/if}
					</li>
				{/each}
			</ul>
			{#if status.players > status.sample.length}<p class="mt-2 text-small text-muted">The server shows a sample of {status.sample.length} names.</p>{/if}
		{:else}
			<p class="mt-2 text-muted">{running ? 'Nobody is online.' : 'Offline.'}</p>
		{/if}
	</section>

	{#each [{ title: 'Operators', list: ops, kind: 'op' as const, remove: (n: string) => send(`deop ${n}`, `${n} is no longer an operator`), removeLabel: 'Remove' }, { title: 'Whitelist', list: whitelist, kind: 'whitelist' as const, remove: (n: string) => send(`whitelist remove ${n}`, `${n} removed from the whitelist`), removeLabel: 'Remove' }, { title: 'Banned', list: banned, kind: 'ban' as const, remove: (n: string) => send(`pardon ${n}`, `${n} was pardoned`), removeLabel: 'Pardon' }] as box (box.title)}
		<section class="card p-5">
			<div class="flex items-center justify-between gap-2">
				<h2 class="text-section font-semibold">{box.title} <span class="text-small font-normal text-muted">· {box.list.length}</span></h2>
				{#if canAct}<button class="btn btn-sm" disabled={busy || !running} onclick={() => addTo(box.kind)}><Icon name="plus" size={14} />Add</button>{/if}
			</div>
			{#if box.kind === 'whitelist' && canAct}
				<div class="mt-2 flex gap-2">
					<button class="btn btn-sm" disabled={busy || !running} onclick={() => send('whitelist on', 'Whitelist turned on')}>Turn on</button>
					<button class="btn btn-sm" disabled={busy || !running} onclick={() => send('whitelist off', 'Whitelist turned off')}>Turn off</button>
				</div>
			{/if}
			<ul class="mt-3 divide-y divide-rule-soft">
				{#each box.list as p (p.name)}
					<li class="flex items-center gap-2 py-2">
						<span class="flex-1"><span class="font-medium">{p.name}</span>{#if p.reason}<span class="text-small text-muted"> · {p.reason}</span>{/if}{#if p.level}<span class="text-small text-muted"> · level {p.level}</span>{/if}</span>
						{#if canAct}<button class="btn btn-sm btn-quiet" disabled={busy || !running} onclick={() => box.remove(p.name)}>{box.removeLabel}</button>{/if}
					</li>
				{:else}
					<li class="py-2 text-small text-muted">{canRead ? 'Nobody yet.' : 'You need file access to see this list.'}</li>
				{/each}
			</ul>
		</section>
	{/each}
</div>
