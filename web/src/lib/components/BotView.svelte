<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { can, Perm, type Bot, type NodeInfo } from '$lib/api/types';
	import { power, type PowerAction } from '$lib/api/bots';
	import { isStopped } from '$lib/status';
	import { session } from '$lib/session.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import StatsBar from '$lib/components/StatsBar.svelte';
	import Overview from '$lib/components/Overview.svelte';
	import Console from '$lib/components/Console.svelte';
	import Files from '$lib/components/Files.svelte';
	import Env from '$lib/components/Env.svelte';
	import Startup from '$lib/components/Startup.svelte';
	import Packages from '$lib/components/Packages.svelte';
	import PageStudio from '$lib/components/PageStudio.svelte';
	import Network from '$lib/components/Network.svelte';
	import Addons from '$lib/components/Addons.svelte';
	import Backups from '$lib/components/Backups.svelte';
	import Deploy from '$lib/components/Deploy.svelte';
	import Users from '$lib/components/Users.svelte';
	import Settings from '$lib/components/Settings.svelte';
	import Schedules from '$lib/components/Schedules.svelte';
	import Alerts from '$lib/components/Alerts.svelte';
	import GameStartup from '$lib/components/GameStartup.svelte';
	import GameNetwork from '$lib/components/GameNetwork.svelte';
	import GameStatusStrip from '$lib/components/GameStatusStrip.svelte';
	import GameProperties from '$lib/components/GameProperties.svelte';
	import GamePlayers from '$lib/components/GamePlayers.svelte';
	import GameAddons from '$lib/components/GameAddons.svelte';
	import type { GameDetail } from '$lib/api/games';
	import { goto } from '$app/navigation';
	import { publishTarget } from '$lib/ai/context.svelte';
	import UsageAnalytics from '$lib/components/UsageAnalytics.svelte';

	let { id }: { id: string } = $props();

	let bot = $state<Bot | null>(null);
	let missing = $state(false);
	let loadError = $state('');
	let now = $state(Date.now());
	let acting = $state(false);
	let tabs = $state<HTMLElement | null>(null);
	let nodeNames = $state<Record<string, string>>({});
	let nodesOffline = $state<Record<string, boolean>>({});

	type Section = { id: string; label: string; icon: IconName; perm: number };
	// One row of tabs, in the order people reach for them. perm -1 = owner or
	// administrator only; 0 = anyone with access. The server enforces the same
	// rules.
	const sections: Section[] = [
		{ id: 'manage', label: 'Manage', icon: 'terminal', perm: 0 },
		{ id: 'overview', label: 'Overview', icon: 'overview', perm: 0 },
		{ id: 'files', label: 'Files', icon: 'file', perm: Perm.files },
		{ id: 'players', label: 'Players', icon: 'users', perm: 0 },
		{ id: 'properties', label: 'Properties', icon: 'sliders', perm: Perm.files },
		{ id: 'mods', label: 'Mods', icon: 'package', perm: 0 },
		{ id: 'deploy', label: 'Deploy', icon: 'rocket', perm: Perm.files },
		{ id: 'startup', label: 'Startup', icon: 'sliders', perm: Perm.admin },
		{ id: 'packages', label: 'Packages', icon: 'package', perm: Perm.files },
		{ id: 'env', label: 'Env', icon: 'key', perm: Perm.env },
		{ id: 'addons', label: 'Add-ons', icon: 'layers', perm: 0 },
		{ id: 'network', label: 'Network', icon: 'network', perm: Perm.admin },
		{ id: 'page', label: 'Page', icon: 'globe', perm: Perm.console },
		{ id: 'alerts', label: 'Health', icon: 'activity', perm: Perm.console },
		{ id: 'usage', label: 'Analytics', icon: 'chart', perm: Perm.console },
		{ id: 'backups', label: 'Backups', icon: 'archive', perm: Perm.files },
		{ id: 'schedules', label: 'Schedules', icon: 'clock', perm: 0 },
		{ id: 'users', label: 'Access', icon: 'users', perm: -1 },
		{ id: 'settings', label: 'Settings', icon: 'gear', perm: Perm.admin }
	];
	// Sections for features this panel does not run are left out entirely.
	const available: Record<string, keyof typeof session.features> = { files: 'files', packages: 'files', backups: 'backups', schedules: 'schedules', alerts: 'health', usage: 'usage' };
	// Game servers take their startup from the server type: no deployments,
	// packages, raw variables or Discord page.
	const notForGames = new Set(['deploy', 'packages', 'env', 'page']);
	const onlyForGames = new Set(['players', 'properties', 'mods']);
	const isGame = $derived(bot?.kind === 'game');
	// What the server type offers (players, properties, a mod browser).
	let game = $state<GameDetail | null>(null);
	$effect(() => {
		if (bot?.kind === 'game' && !game) api<GameDetail>('GET', `/bots/${id}/game`).then((g) => (game = g)).catch(() => {});
	});
	const gameHas = (sid: string) =>
		sid === 'players' ? !!game?.spec.features?.includes('minecraft-players') : sid === 'properties' ? !!game?.spec.features?.includes('minecraft-properties') : sid === 'mods' ? !!game?.spec.addons : true;
	const noun = $derived(isGame ? 'server' : 'bot');
	const allowed = (s: Section) =>
		!!bot && !(bot.kind === 'game' && notForGames.has(s.id)) && !(onlyForGames.has(s.id) && (bot.kind !== 'game' || !gameHas(s.id))) && (!available[s.id] || session.features[available[s.id]]) && (s.perm === 0 || (s.perm === -1 ? !bot.shared : can(bot, s.perm)));
	const flat = $derived(sections.filter(allowed));
	// Links from before the tabs were reorganised keep working.
	const legacy: Record<string, string> = { analytics: 'page', console: 'manage', ai: 'manage' };
	const asked = $derived(page.url.searchParams.get('tab') ?? 'manage');
	const requested = $derived(legacy[asked] ?? asked);
	const tab = $derived(flat.some((s) => s.id === requested) ? requested : 'manage');

	function sectionHref(sid: string) {
		return `${isGame ? '/servers' : '/bots'}/${id}?tab=${sid}`;
	}
	// Old links to a game server under /bots land on its /servers page.
	$effect(() => {
		if (bot?.kind === 'game' && page.url.pathname.startsWith('/bots/')) goto(`/servers/${id}${page.url.search}`, { replaceState: true });
	});

	async function refresh() {
		try {
			bot = await api<Bot>('GET', `/bots/${id}`);
			loadError = '';
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) missing = true;
			else loadError = e instanceof ApiError ? e.message : 'Connection to the panel was lost. Retrying…';
		}
	}
	onMount(() => {
		refresh();
		if (session.user?.role === 'admin') api<{ nodes: NodeInfo[] }>('GET', '/nodes')
				.then((r) => {
					nodeNames = Object.fromEntries(r.nodes.map((n) => [n.id, n.name]));
					// Status and stats shown below are the last ones the node reported.
					nodesOffline = Object.fromEntries(r.nodes.map((n) => [n.id, n.transport === 'agent' && !n.agent?.connected]));
				})
				.catch(() => {});
		const poll = setInterval(() => document.visibilityState === 'visible' && refresh(), 2000);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(tick);
		};
	});

	async function act(a: PowerAction) {
		if (!bot) return;
		acting = true;
		if (await power(bot, a)) await refresh();
		acting = false;
	}

	const stopped = $derived(bot ? isStopped(bot) : false);
	const isOwner = $derived(bot ? !bot.shared : false);
	const admin = $derived(bot ? can(bot, Perm.admin) : false);
	// Without a runner, lifecycle requests can only fail: offer none.
	const canPower = $derived(bot ? can(bot, Perm.power) && session.features.runner : false);
	const configSection = $derived(['env', 'addons', 'startup', 'network', 'settings'].includes(tab));

	// Tell the assistant which bot and tab are in view.
	// A narrow window scrolls the tab row; keep the current tab visible.
	$effect(() => {
		void tab;
		tabs?.querySelector('[aria-current=page]')?.scrollIntoView({ inline: 'nearest', block: 'nearest' });
	});
	const botName = $derived(bot?.name ?? '');
	$effect(() => {
		if (!botName) return;
		return publishTarget({ kind: 'bot', id, label: botName, section: tab === 'manage' ? 'Console' : (sections.find((x) => x.id === tab)?.label ?? tab) });
	});
</script>

<svelte:head><title>{bot?.name ?? 'Server'} · RivetPanel</title></svelte:head>

{#if missing}
	<h1 class="text-page">Not found</h1>
	<p class="mt-1 text-muted">It was deleted, or it belongs to another account. <a class="link" href="/dashboard">Back to the overview</a></p>
{:else if !bot}
	{#if loadError}<Notice tone="fail">{loadError}</Notice>{:else}<Skeleton rows={4} label="Loading bot" />{/if}
{:else}
	<!-- Identity, the shortcuts people use most, and one row of tabs. -->
	<header class="rounded-tile border border-rule-soft bg-panel">
		<div class="flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-3.5">
			<a href={isGame ? '/servers' : '/dashboard'} class="btn btn-quiet btn-icon btn-sm shrink-0 text-muted" aria-label="Back to {noun}s" title="Back to {noun}s"><Icon name="chevronLeft" size={16} /></a>
			{#if bot.logo_url}
				<img src={bot.logo_url} alt="" class="size-11 shrink-0 rounded-tile object-cover" referrerpolicy="no-referrer" />
			{:else}
				<span class="grid size-11 shrink-0 place-items-center rounded-tile bg-paper-2/70 text-action"><Icon name={isGame ? 'cube' : 'box'} size={20} /></span>
			{/if}
			<div class="min-w-0 flex-1 basis-56">
				<h1 class="truncate text-section font-semibold">{bot.name}</h1>
				<p class="truncate text-small text-muted" title="{bot.runtime}, {fmtBytes(bot.memory_bytes)} memory, {fmtCpu(bot.nano_cpus)}">
					{isGame ? 'Game server' : bot.runtime} · {fmtBytes(bot.memory_bytes)} · {fmtCpu(bot.nano_cpus)}{#if nodeNames[bot.node_id]} · {nodeNames[bot.node_id]}{#if nodesOffline[bot.node_id]}<span class="ml-1 text-warn" title="The node's agent is not connected. Status and output are the last ones it reported.">(node offline)</span>{/if}{/if}{#if bot.source_type === 'github'} · deployed from GitHub{/if}{#if bot.shared} · shared with you{/if}
				</p>
			</div>
			<div class="flex flex-wrap items-center gap-2">
				{#if flat.some((s) => s.id === 'page')}<a class="btn gap-1.5 border-action/50 text-action hover:bg-action/8" href={sectionHref('page')} data-sveltekit-noscroll><Icon name="globe" size={14} />Open Studio</a>{/if}
				{#if flat.some((s) => s.id === 'settings')}<a class="btn btn-primary gap-1.5" href={sectionHref('settings')} data-sveltekit-noscroll><Icon name="sliders" size={14} />Adjust resources</a>{/if}
			</div>
		</div>
		<nav class="flex gap-0.5 overflow-x-auto border-t border-rule-soft px-2 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" aria-label="{isGame ? 'Server' : 'Bot'} sections" bind:this={tabs}>
			{#each flat as s (s.id)}
				<a
					href={sectionHref(s.id)}
					data-sveltekit-noscroll
					data-sveltekit-keepfocus
					class="relative flex shrink-0 items-center gap-2 px-2.5 py-3 text-small font-medium whitespace-nowrap {tab === s.id ? 'text-action after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:bg-action' : 'text-muted hover:text-ink'}"
					aria-current={tab === s.id ? 'page' : undefined}
				>
					<Icon name={s.icon} />{s.id === 'mods' && game?.spec.addons?.kind === 'plugin' ? 'Plugins' : s.label}
				</a>
			{/each}
		</nav>
	</header>

	<div class="mt-4"><StatsBar {bot} {now} showStats={can(bot, Perm.console) && session.features.stats} {canPower} {acting} onAct={act} /></div>
	{#if loadError}<Notice tone="warn" class="mt-3">{loadError}</Notice>{/if}

	<section class="mt-4 min-w-0" aria-label={flat.find((s) => s.id === tab)?.label}>
		{#if configSection && !stopped}
			<Notice tone="warn" class="mb-4" title="Stop the {noun} to change these settings">
				Changes need a stopped {noun} and apply the next time it starts.
				{#snippet action()}
					{#if canPower}<button class="btn btn-sm" disabled={acting} onclick={() => act('stop')}>Stop {noun}</button>{/if}
				{/snippet}
			</Notice>
		{/if}

		{#if tab === 'manage'}
			{#if isGame}<div class="mb-4"><GameStatusStrip {bot} /></div>{/if}
			{#if can(bot, Perm.console)}<Console {bot} />{:else}<Overview {bot} {now} onSaved={(b) => (bot = b)} onAct={act} {acting} />{/if}
		{:else if tab === 'overview'}
			<Overview {bot} {now} onSaved={(b) => (bot = b)} onAct={act} {acting} />
		{:else if tab === 'page'}
			<PageStudio botId={id} botName={bot.name} canEdit={can(bot, Perm.files)} canAdmin={admin} {stopped} />
		{:else if tab === 'players'}
			<GamePlayers {bot} />
		{:else if tab === 'properties'}
			<GameProperties {bot} onOpenFiles={() => goto(sectionHref('files'))} />
		{:else if tab === 'mods'}
			<GameAddons {bot} />
		{:else if tab === 'files'}
			<Files botId={id} running={!stopped} />
		{:else if tab === 'packages'}
			<Packages botId={id} running={!stopped} />
		{:else if tab === 'deploy'}
			<Deploy {bot} {admin} />
		{:else if tab === 'env'}
			<Env {bot} running={!stopped} />
		{:else if tab === 'startup' && isGame}
			<GameStartup {bot} {stopped} onSaved={(b) => (bot = b)} />
		{:else if tab === 'startup'}
			<Startup {bot} {stopped} onSaved={(b) => (bot = b)} />
		{:else if tab === 'addons'}
			<Addons {bot} {stopped} />
		{:else if tab === 'network' && isGame}
			<GameNetwork {bot} {stopped} onSaved={(b) => (bot = b)} />
		{:else if tab === 'network'}
			<Network {bot} {stopped} owner={isOwner} onSaved={(b) => (bot = b)} />
		{:else if tab === 'backups'}
			<Backups {bot} {stopped} {admin} onSaved={(b) => (bot = b)} />
		{:else if tab === 'alerts'}
			<Alerts {bot} {admin} />
		{:else if tab === 'usage'}
			<UsageAnalytics {bot} />
		{:else if tab === 'schedules'}
			<Schedules {bot} />
		{:else if tab === 'users'}
			<Users botId={id} botName={bot.name} />
		{:else if tab === 'settings'}
			<Settings {bot} {stopped} onSaved={(b) => (bot = b)} />
		{/if}
	</section>
{/if}
