<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes, fmtCpu } from '$lib/api/client';
	import { can, Perm, type Bot, type Packages } from '$lib/api/types';
	import { power, type PowerAction } from '$lib/api/bots';
	import { describe, isStopped, mayBeLive } from '$lib/status';
	import { chat } from '$lib/ai/chat.svelte';
	import TabBar from '$lib/components/ui/TabBar.svelte';
	import { resolveTab, tabNav, tabsFor, type AppKind, type Tab } from '$lib/tabs';
	import { session } from '$lib/session.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import GameIcon from '$lib/components/ui/GameIcon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import StatsBar from '$lib/components/StatsBar.svelte';
	import Overview from '$lib/components/Overview.svelte';
	import ExtensionCards from '$lib/components/ExtensionCards.svelte';
	import ExtensionFrame from '$lib/components/ExtensionFrame.svelte';
	import { extensions, loadExtensions, botPermBit } from '$lib/extensions/store.svelte';
	import Console from '$lib/components/Console.svelte';
	import LogDays from '$lib/components/LogDays.svelte';
	import GameStatusStrip from '$lib/components/GameStatusStrip.svelte';
	import type { GameDetail } from '$lib/api/games';
	import { goto } from '$app/navigation';
	import { publishTarget } from '$lib/ai/context.svelte';

	let { id }: { id: string } = $props();

	let bot = $state<Bot | null>(null);
	let missing = $state(false);
	let loadError = $state('');
	let now = $state(Date.now());
	let acting = $state(false);

	// Which sections this app shows comes from the tab registry: one id, label
	// and icon per concept for every kind of app.
	const kind = $derived<AppKind>((bot?.kind ?? 'bot') as AppKind);
	const isGame = $derived(kind === 'game');
	const isContainer = $derived(kind === 'container');
	// Where this app's list and pages live.
	const listHref = $derived(isGame ? '/apps?kind=game' : isContainer ? (bot?.container?.stack_id ? `/stacks/${bot.container.stack_id}` : '/apps?kind=container') : '/apps?kind=bot');
	const base = $derived(isGame ? '/servers' : isContainer ? '/containers' : '/bots');
	// What the server type offers (players, properties, a mod browser).
	let game = $state<GameDetail | null>(null);
	$effect(() => {
		if (bot?.kind === 'game' && !game) api<GameDetail>('GET', `/bots/${id}/game`).then((g) => (game = g)).catch(() => {});
	});
	const noun = $derived(isGame ? 'server' : isContainer ? 'container' : 'bot');
	// Tabs that enabled extensions add, with the data tabs.
	$effect(() => {
		if (session.features.extensions && !extensions.loaded) loadExtensions();
	});
	const extTabs = $derived(
		extensions.list.flatMap((x) =>
			x.bot_tabs.map((t) => ({ id: `x-${x.id}-${t.id}`, label: t.label, icon: 'layers' as IconName, group: 'data' as const, perm: botPermBit(t.bot_perm), ext: x.id, page: t.page, extName: x.name }))
		)
	);
	// The Packages tab is disabled until the project has its dependency
	// manifest (package.json for Node.js, requirements.txt for Python …).
	// Unknown (not loaded, or the check failed) leaves it enabled.
	let pkgs = $state<Pick<Packages, 'supported' | 'file' | 'exists'> | null>(null);
	const pkgHint = $derived(!pkgs ? null : !pkgs.supported ? 'The package manager is not available for this runtime.' : !pkgs.exists ? `Create ${pkgs.file} in Files to manage packages here.` : null);
	const tabs = $derived.by<Tab[]>(() => {
		const b = bot;
		if (!b) return [];
		const allow = (p: number) => p === 0 || (p === -1 ? !b.shared : can(b, p));
		const list = tabsFor({ kind, bot: b, features: session.features, game, allow }).map((t) => (t.id === 'packages' && pkgHint ? { ...t, disabled: true, hint: pkgHint } : t));
		return [...list, ...extTabs.filter((t) => allow(t.perm))];
	});
	const asked = $derived(page.url.searchParams.get('tab'));
	const resolved = $derived(resolveTab(asked, kind, tabs));
	const tab = $derived(resolved.id);
	const current = $derived(tabs.find((s) => s.id === tab));

	// Check the manifest when the bot loads, on every tab switch and when the
	// file manager changes files, so creating package.json enables the tab.
	const hasPackagesTab = $derived(tabs.some((t) => t.id === 'packages'));
	let pkgSeq = 0;
	async function checkPackages() {
		const seq = ++pkgSeq;
		try {
			const r = await api<Packages>('GET', `/bots/${id}/packages`);
			if (seq === pkgSeq) pkgs = { supported: r.supported, file: r.file, exists: r.exists };
		} catch {
			if (seq === pkgSeq) pkgs = null;
		}
	}
	$effect(() => {
		void tab;
		if (hasPackagesTab) untrack(checkPackages);
	});
	onMount(() => {
		const onFiles = (e: Event) => {
			if ((e as CustomEvent<{ botId: string }>).detail?.botId === id && hasPackagesTab) checkPackages();
		};
		window.addEventListener('rivet:files-changed', onFiles);
		return () => window.removeEventListener('rivet:files-changed', onFiles);
	});

	function sectionHref(sid: string) {
		return `${base}/${id}?tab=${sid}`;
	}
	// Retired and alternative tab names (?tab=overview, ?tab=variables …) are rewritten to the tab they open.
	$effect(() => {
		if (bot && resolved.canonical) {
			const u = new URL(page.url);
			u.searchParams.set('tab', resolved.canonical);
			goto(u.pathname + u.search, { replaceState: true, noScroll: true, keepFocus: true });
		}
	});
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
	const configSection = $derived(!!current?.needsStop);

	// Power controls that apply now. A bot that gave up or exited cleanly is
	// wanted running but idle: the useful action is to start it again.
	const d = $derived(bot ? describe(bot, now) : null);
	const wanted = $derived(bot?.desired_state === 'running');
	const idle = $derived(wanted && (bot?.phase === 'failed' || bot?.phase === 'exited'));
	const powerOk = $derived({ start: !wanted || idle, restart: wanted && !idle, stop: wanted, kill: bot ? mayBeLive(bot) : false });
	const dotTone = $derived(d ? { run: 'bg-run', warn: 'bg-warn', fail: 'bg-fail', idle: 'bg-muted' }[d.tone] : '');
	const nav = $derived(tabNav(tabs, tab, kind, sectionHref));

	// Tell the assistant which bot and tab are in view.
	const botName = $derived(bot?.name ?? '');
	$effect(() => {
		if (!botName) return;
		return publishTarget({ kind: 'bot', id, label: botName, section: tab === 'manage' ? 'Console' : (current?.label ?? tab) });
	});
</script>

<svelte:head><title>{bot?.name ?? 'Server'} · RivetPanel</title></svelte:head>

{#if missing}
	<h1 class="text-page">Not found</h1>
	<p class="mt-1 text-muted">It was deleted, or it belongs to another account. <a class="link" href="/dashboard">Back to the overview</a></p>
{:else if !bot}
	{#if loadError}<Notice tone="fail">{loadError}</Notice>{:else}<Skeleton rows={4} label="Loading" />{/if}
{:else}
	<!-- Identity and power on one row, live usage below it, then the tabs. -->
	<header class="rounded-tile border border-rule-soft bg-panel">
		<div class="flex flex-wrap items-center gap-x-4 gap-y-3 px-4 pt-3 pb-3">
			<a href={listHref} class="btn btn-quiet btn-icon btn-sm -ml-1 shrink-0 text-muted" aria-label={isContainer && bot?.container?.stack_id ? 'Back to the stack' : `Back to ${noun}s`} title={isContainer && bot?.container?.stack_id ? 'Back to the stack' : `Back to ${noun}s`}><Icon name="chevronLeft" size={16} /></a>
			{#if bot.logo_url}
				<img src={bot.logo_url} alt="" class="size-10 shrink-0 rounded-tile object-cover" referrerpolicy="no-referrer" data-vt-icon={bot.id} />
			{:else if isGame}
				<span class="grid shrink-0" data-vt-icon={bot.id}><GameIcon type={game?.blueprint} size={40} /></span>
			{:else}
				<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-paper-2 text-muted" data-vt-icon={bot.id}><Icon name="box" size={18} /></span>
			{/if}
			<div class="min-w-0 flex-1 basis-48">
				<div class="flex min-w-0 items-center gap-2.5">
					<h1 class="truncate text-page font-semibold" data-vt-name={bot.id}>{bot.name}</h1>
					{#if d}<span class="pill shrink-0" data-tone={d.tone} title={d.detail || undefined}><span class="size-1.5 rounded-pill {dotTone} {d.busy ? 'animate-pulse' : ''}" aria-hidden="true"></span>{d.label}</span>{/if}
				</div>
				<p class="truncate text-small text-muted" title="{bot.runtime}, {fmtBytes(bot.memory_bytes)} memory, {fmtCpu(bot.nano_cpus)}">
					{isGame ? (game?.blueprint.name ?? 'Game server') : isContainer ? bot.image_ref.replace(/^docker\.io\/library\//, '').replace(/^docker\.io\//, '') : bot.runtime}, {fmtBytes(bot.memory_bytes)}, {fmtCpu(bot.nano_cpus)}{#if bot.node_name}, {bot.node_name}{#if bot.node_online === false}<span class="ml-1 text-warn" title="The node's agent is not connected. Status and output are the last ones it reported.">(node offline)</span>{/if}{/if}{#if bot.source_type === 'github'}, deployed from GitHub{/if}{#if bot.shared}, shared with you{/if}
				</p>
			</div>
			{#if configSection && !stopped}
				<!-- Settings tabs need a stopped app: say so beside the Stop button
				     instead of pushing the section down. -->
				<div class="flex min-w-0 items-center gap-2 rounded-control border border-warn/30 bg-warn/7 px-3 py-1.5 text-small max-sm:w-full" role="status">
					<Icon name="alert" size={14} class="shrink-0 text-warn" />
					<p class="min-w-0"><span class="font-medium text-ink">Stop the {noun} to change these settings.</span> <span class="text-ink/75">Changes apply the next time it starts.</span></p>
				</div>
			{/if}
			{#if canPower}
				<div class="grid w-full grid-cols-4 gap-1.5 sm:flex sm:w-auto" role="group" aria-label="Power controls">
					<button class="btn {powerOk.start ? 'btn-primary' : ''}" disabled={acting || !powerOk.start} onclick={() => act('start')}><Icon name="play" size={11} />Start</button>
					<button class="btn" disabled={acting || !powerOk.restart} onclick={() => act('restart')}><Icon name="restart" size={13} />Restart</button>
					<button class="btn" disabled={acting || !powerOk.stop} onclick={() => act('stop')}><Icon name="stop" size={11} />Stop</button>
					<button class="btn btn-danger" disabled={acting || !powerOk.kill} onclick={() => act('kill')} title="Ends the process immediately"><Icon name="bolt" size={13} />Kill</button>
				</div>
			{/if}
		</div>
		{#if d?.detail}
			<div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-rule-soft px-4 py-2 text-small" aria-live="polite">
				<Icon name={d.tone === 'fail' ? 'alert' : 'info'} size={14} class="shrink-0 {d.tone === 'fail' ? 'text-fail' : d.tone === 'warn' ? 'text-warn' : 'text-muted'}" />
				<p class="min-w-0 flex-1 truncate {d.tone === 'fail' ? 'text-fail' : 'text-muted'}" title={d.detail}><span class="font-medium text-ink">{d.label}.</span> {d.detail}</p>
				{#if d.tone === 'fail' && session.features.ai}
					<button class="btn btn-sm btn-quiet" onclick={() => chat.ask(isGame ? 'My game server is not running. Check its status, console output and installation output, tell me what is wrong and how to fix it.' : 'My bot is not running. Check its status, console output and build output, tell me what is wrong and how to fix it.', true)}><Icon name="sparkle" size={13} />Ask AI why</button>
				{/if}
			</div>
		{/if}
		{#if (can(bot, Perm.console) && session.features.stats) || isGame}
			<div class="@container border-t border-rule-soft px-4 py-2.5">
				{#if can(bot, Perm.console) && session.features.stats}
					<StatsBar {bot}>
						{#snippet extra()}{#if isGame && bot}<GameStatusStrip {bot} />{/if}{/snippet}
					</StatsBar>
				{:else}
					<div class="stat-strip"><GameStatusStrip {bot} /></div>
				{/if}
			</div>
		{/if}
		<div class="border-t border-rule-soft px-2">
			<TabBar tabs={nav.primary} current={nav.active} fill label="{noun[0].toUpperCase() + noun.slice(1)} sections" />
		</div>
	</header>
	{#if nav.secondary.length}
		<div class="mt-3"><TabBar tabs={nav.secondary} current={tab} variant="pill" label="{nav.groupLabel} sections" /></div>
	{/if}
	{#if loadError}<Notice tone="warn" class="mt-3">{loadError}</Notice>{/if}

	<section class="{nav.secondary.length ? 'mt-3' : 'mt-4'} min-w-0" aria-label={current?.label}>
		<!-- Every tab except Manage downloads its code when first opened, so a bot
		     page ships one tab instead of all twenty. A failed download usually
		     means the panel was updated while this page was open. -->
		{#snippet tabLoading()}<Skeleton rows={4} label="Loading {current?.label ?? 'section'}" />{/snippet}
		{#snippet tabFailed()}
			<Notice tone="fail" title="This section could not be loaded">
				The panel may have been updated since this page opened.
				{#snippet action()}<button class="btn btn-sm" onclick={() => location.reload()}>Reload page</button>{/snippet}
			</Notice>
		{/snippet}
		{#if tab === 'manage'}
			<!-- Manage: the console with status and recent activity below it, and
			     the side facts (source, backups, details) in a narrow column. -->
			<Overview {bot} {now} {game} onAct={act} {acting}>
				{#snippet top()}{#if can(bot!, Perm.console)}<Console bot={bot!} />{/if}{/snippet}
				{#snippet side()}{#if session.features.extensions}<ExtensionCards bot={bot!} />{/if}{/snippet}
				{#snippet bottom()}{#if can(bot!, Perm.console)}<LogDays path="/bots/{bot!.id}/logs/days" filename="server-{bot!.id.slice(0, 8)}" label="Log history" empty="No day files yet. Console output is copied every few minutes while the {noun} runs." />{/if}{/snippet}
			</Overview>
		{:else if tab === 'terminal'}
			{#await import('$lib/components/ContainerTerminal.svelte')}{@render tabLoading()}{:then { default: ContainerTerminal }}<ContainerTerminal bot={bot} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'page'}
			{#await import('$lib/components/PageStudio.svelte')}{@render tabLoading()}{:then { default: PageStudio }}<PageStudio botId={id} botName={bot.name} canEdit={can(bot, Perm.files)} canAdmin={admin} {stopped} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'players'}
			{#await import('$lib/components/GamePlayers.svelte')}{@render tabLoading()}{:then { default: GamePlayers }}<GamePlayers {bot} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'properties'}
			{#await import('$lib/components/GameProperties.svelte')}{@render tabLoading()}{:then { default: GameProperties }}<GameProperties {bot} onOpenFiles={() => goto(sectionHref('files'))} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'mods'}
			{#await import('$lib/components/GameAddons.svelte')}{@render tabLoading()}{:then { default: GameAddons }}<GameAddons {bot} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'files'}
			{#await import('$lib/components/Files.svelte')}{@render tabLoading()}{:then { default: Files }}<Files botId={id} running={!stopped} {isGame} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'packages'}
			{#await import('$lib/components/Packages.svelte')}{@render tabLoading()}{:then { default: Packages }}<Packages botId={id} running={!stopped} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'deploy'}
			{#await import('$lib/components/Deploy.svelte')}{@render tabLoading()}{:then { default: Deploy }}<Deploy {bot} {admin} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'env'}
			{#await import('$lib/components/Env.svelte')}{@render tabLoading()}{:then { default: Env }}<Env {bot} running={!stopped} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'startup' && isContainer}
			{#await import('$lib/components/ContainerConfig.svelte')}{@render tabLoading()}{:then { default: ContainerConfig }}<ContainerConfig {bot} {stopped} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'startup' && isGame}
			{#await import('$lib/components/GameStartup.svelte')}{@render tabLoading()}{:then { default: GameStartup }}<GameStartup {bot} {stopped} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'startup'}
			{#await import('$lib/components/Startup.svelte')}{@render tabLoading()}{:then { default: Startup }}<Startup {bot} {stopped} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'addons'}
			{#await import('$lib/components/Addons.svelte')}{@render tabLoading()}{:then { default: Addons }}<Addons {bot} {stopped} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'network' && isGame}
			{#await import('$lib/components/GameNetwork.svelte')}{@render tabLoading()}{:then { default: GameNetwork }}<GameNetwork {bot} {stopped} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'network'}
			{#await import('$lib/components/Network.svelte')}{@render tabLoading()}{:then { default: Network }}<Network {bot} {stopped} owner={isOwner} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'backups'}
			{#await import('$lib/components/Backups.svelte')}{@render tabLoading()}{:then { default: Backups }}<Backups {bot} {stopped} {admin} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'alerts'}
			{#await import('$lib/components/Alerts.svelte')}{@render tabLoading()}{:then { default: Alerts }}<Alerts {bot} {admin} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'usage'}
			{#await import('$lib/components/UsageAnalytics.svelte')}{@render tabLoading()}{:then { default: UsageAnalytics }}<UsageAnalytics {bot} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'schedules'}
			{#await import('$lib/components/Schedules.svelte')}{@render tabLoading()}{:then { default: Schedules }}<Schedules {bot} />{:catch}{@render tabFailed()}{/await}
		{:else if tab === 'users'}
			{#await import('$lib/components/Users.svelte')}{@render tabLoading()}{:then { default: Users }}<Users botId={id} botName={bot.name} isGame={isGame} />{:catch}{@render tabFailed()}{/await}
		{:else if tab.startsWith('x-')}
			{@const xt = extTabs.find((t) => t.id === tab)}
			{#if xt}
				<p class="mb-2 flex items-center gap-1.5 text-small text-muted"><Icon name="layers" size={13} />Extension: {xt.extName}</p>
				<ExtensionFrame ext={xt.ext} page={xt.page} botId={id} title="{xt.label} ({xt.extName})" />
			{/if}
		{:else if tab === 'settings'}
			{#await import('$lib/components/Settings.svelte')}{@render tabLoading()}{:then { default: Settings }}<Settings {bot} {stopped} onSaved={(b) => (bot = b)} />{:catch}{@render tabFailed()}{/await}
		{/if}
	</section>
{/if}
