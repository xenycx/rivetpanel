<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { afterNavigate, beforeNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { Bot } from '$lib/api/types';
	import { session, loadSession, logout, rememberNext, canAdminister, can } from '$lib/session.svelte';
	import { adminLinks } from '$lib/adminNav';
	import { describePermissions } from '$lib/permissions';
	import { confirmLeave, dirtyEntries } from '$lib/ui/guard.svelte';
	import DialogHost from '$lib/components/ui/DialogHost.svelte';
	import ToastRegion from '$lib/components/ui/ToastRegion.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import OperationShelf from '$lib/components/OperationShelf.svelte';
	import CommandPalette from '$lib/components/CommandPalette.svelte';
	import AIChat from '$lib/components/AIChat.svelte';
	import { chat } from '$lib/ai/chat.svelte';
	import { theme, cycleTheme } from '$lib/ui/theme.svelte';
	import { describe } from '$lib/status';
	import WorkspaceSwitcher from '$lib/components/WorkspaceSwitcher.svelte';
	import NotificationBell from '$lib/components/NotificationBell.svelte';
	import NewMenu from '$lib/components/NewMenu.svelte';
	import { loadWorkspaces } from '$lib/workspaces.svelte';
	import { resourceHref } from '$lib/api/games';

	let { children } = $props();
	let drawer = $state(false);
	let palette = $state(false);
	let setupNeeded = $state(false);
	function readSidebarPreference() {
		try {
			return localStorage.getItem('rivetpanel.sidebarCollapsed') === '1';
		} catch {
			return false;
		}
	}
	let sidebarCollapsed = $state(readSidebarPreference());

	// Pages that work without an account.
	const PUBLIC = ['/', '/login', '/register', '/reset', '/verify-email', '/welcome', '/setup', '/docs', '/status'];
	// The help center answers anonymous visitors itself (only while the
	// public help center is on; otherwise it asks them to sign in).
	const isPublic = (p: string) => PUBLIC.includes(p) || p === '/help' || p.startsWith('/help/');

	onMount(async () => {
		await loadSession();
		if (!session.user) {
			try {
				setupNeeded = (await api<{ needed: boolean }>('GET', '/setup/status')).needed;
			} catch {
				/* older server or offline: fall through to sign-in */
			}
		}
	});

	$effect(() => {
		if (!session.loaded || session.user) return;
		const p = page.url.pathname;
		if (setupNeeded && p !== '/setup') goto('/setup');
		else if (!setupNeeded && !isPublic(p)) {
			rememberNext(location.pathname + location.search + location.hash);
			goto('/login');
		}
	});

	function toggleSidebar() {
		sidebarCollapsed = !sidebarCollapsed;
		try {
			localStorage.setItem('rivetpanel.sidebarCollapsed', sidebarCollapsed ? '1' : '0');
		} catch {
			/* the preference is optional */
		}
	}

	// Leaving with unsaved edits asks first (in-app navigation and page exit).
	let bypass = false;
	beforeNavigate((nav) => {
		drawer = false;
		if (bypass || nav.type === 'leave' || !nav.to || !dirtyEntries().length) return;
		nav.cancel();
		const to = nav.to.url;
		confirmLeave().then((ok) => {
			if (!ok) return;
			bypass = true;
			goto(to).finally(() => (bypass = false));
		});
	});
	function beforeUnload(e: BeforeUnloadEvent) {
		if (dirtyEntries().length) e.preventDefault();
	}

	// Favorites for the sidebar, refreshed on navigation (cheap: one list call).
	let favorites = $state<Bot[]>([]);
	let lastFetch = 0;
	async function loadFavorites() {
		if (!session.user || Date.now() - lastFetch < 5000) return;
		lastFetch = Date.now();
		try {
			favorites = (await api<{ bots: Bot[] }>('GET', '/bots')).bots.filter((b) => b.favorite).slice(0, 8);
		} catch {
			/* the sidebar is a convenience */
		}
	}
	afterNavigate(() => loadFavorites());
	$effect(() => {
		if (session.user) {
			loadFavorites();
			loadWorkspaces();
		}
	});
	// The public status page is linked only when this panel publishes one.
	let statusPage = $state(false);
	$effect(() => {
		if (session.user && !statusPage)
			api('GET', '/status')
				.then(() => (statusPage = true))
				.catch(() => {});
	});

	const path = $derived(page.url.pathname);
	const adminHome = $derived(session.user && canAdminister() ? (adminLinks()[0]?.href ?? '') : '');
	const withheld = $derived(session.verification.withheld ?? []);
	type NavItem = { href: string; label: string; icon: IconName; active: boolean };
	const nav = $derived<NavItem[]>([
		{ href: '/dashboard', label: 'Overview', icon: 'home', active: path === '/dashboard' },
		...(session.features.games ? [{ href: '/servers', label: 'Game servers', icon: 'gamepad' as IconName, active: path.startsWith('/servers') }] : []),
		{ href: '/bots', label: 'Bots', icon: 'box', active: path.startsWith('/bots') },
		...(session.features.sites ? [{ href: '/sites', label: 'Sites', icon: 'globe' as IconName, active: path.startsWith('/sites') }] : []),
		{ href: '/templates', label: 'Templates', icon: 'layers', active: path.startsWith('/templates') },
		{ href: '/activity', label: 'Activity', icon: 'activity', active: path.startsWith('/activity') },
		...(can('tickets.create') || can('tickets.view_all') || can('tickets.manage')
			? [{ href: '/support', label: 'Support', icon: 'lifebuoy' as IconName, active: path.startsWith('/support') }]
			: [])
	]);
	// Account and installation settings sit apart, below the work areas.
	const navEnd = $derived<NavItem[]>([
		{ href: '/settings/connected-accounts', label: 'Settings', icon: 'gear', active: path.startsWith('/settings') },
		...(adminHome ? [{ href: adminHome, label: 'Administration', icon: 'shield' as IconName, active: path.startsWith('/admin') }] : [])
	]);
	// Secondary links share one "Help & resources" menu instead of a list.
	const open = (href: string, external = false) => (external ? window.open(href, '_blank', 'noopener') : goto(href));
	const resources = $derived<MenuItem[]>([
		{ label: 'Documentation', onselect: () => open('/docs') },
		{ label: 'Help center', onselect: () => open('/help') },
		...(statusPage ? [{ label: 'Status page', onselect: () => open('/status') }] : []),
		'separator',
		{ label: 'Automation API', hint: 'OpenAPI description, opens in a new tab', onselect: () => open('/api/v1/automation/openapi.yaml', true) },
		...(can('system.view') ? [{ label: 'Diagnostics', onselect: () => open('/admin/diagnostics') }] : []),
		{ label: 'What RivetPanel does', onselect: () => open('/') },
		{ label: 'Keyboard: Go to', hint: 'Ctrl+K', onselect: () => (palette = true) },
		...(session.features.ai ? [{ label: 'Keyboard: Ask AI', hint: 'Ctrl+.', onselect: () => chat.show() }] : [])
	]);
	const helpActive = $derived(path.startsWith('/docs') || path.startsWith('/help') || path === '/admin/diagnostics');
	const bare = $derived((PUBLIC.includes(path) && path !== '/docs') || (!session.user && session.loaded));
	const who = $derived(session.user?.display_name || session.user?.email.split('@')[0] || '');
	const initials = $derived(who.slice(0, 2).toUpperCase());
	const themeLabel = $derived(theme.pref === 'system' ? `System theme (${theme.dark ? 'dark' : 'light'})` : theme.pref === 'dark' ? 'Dark theme' : 'Light theme');
	const accountItems = $derived([
		{ heading: session.user?.email ?? '' },
		'separator' as const,
		{ label: 'Profile', onselect: () => goto('/settings/profile') },
		{ label: 'Settings', onselect: () => goto('/settings/appearance') },
		'separator' as const,
		{ label: 'Sign out', onselect: logout }
	]);
</script>

<svelte:window onbeforeunload={beforeUnload} />
<svelte:head><title>RivetPanel</title></svelte:head>

{#snippet navList(items: NavItem[], compact: boolean)}
	<ul class="grid gap-0.5">
		{#each items as n (n.href)}
			<li>
				<a href={n.href} class="side-link {compact ? 'justify-center px-0' : ''}" data-active={n.active} aria-current={n.active ? 'page' : undefined} aria-label={compact ? n.label : undefined} title={compact ? n.label : undefined}>
					<Icon name={n.icon} class="side-icon" />{#if !compact}<span>{n.label}</span>{/if}
				</a>
			</li>
		{/each}
	</ul>
{/snippet}

{#snippet sidebar(compact = false, collapsible = false)}
	<div class="flex items-center {compact ? 'flex-col justify-center gap-2' : 'gap-2'} px-1">
		<a href="/dashboard" class="flex min-w-0 items-center gap-2.5 {compact ? 'justify-center' : 'px-2'} py-1" aria-label="RivetPanel overview" title={compact ? 'RivetPanel overview' : undefined}>
			<img src="/favicon.svg" alt="" width="26" height="26" class="shrink-0 rounded-control" />
			{#if !compact}<span class="truncate font-semibold">RivetPanel</span>{/if}
		</a>
		{#if collapsible}
			<button class="tb-btn {compact ? '' : 'ml-auto'} shrink-0" onclick={toggleSidebar} aria-label={compact ? 'Expand sidebar' : 'Collapse sidebar'} title={compact ? 'Expand sidebar' : 'Collapse sidebar'}>
				<Icon name={compact ? 'chevronRight' : 'chevronLeft'} size={16} />
			</button>
		{/if}
	</div>
	<div class="mt-4 {compact ? 'flex justify-center' : 'px-1'}"><WorkspaceSwitcher {compact} /></div>
	<nav class="mt-4" aria-label="Main">
		{@render navList(nav, compact)}
	</nav>
	{#if favorites.length}
		<div class="mt-5">
			{#if !compact}<p class="eyebrow px-3">Favorites</p>{/if}
			<ul class="mt-1.5 grid gap-0.5">
				{#each favorites as b (b.id)}
					{@const d = describe(b)}
					<li>
						<a href={resourceHref(b)} class="side-link {compact ? 'justify-center px-0' : ''}" data-active={path === resourceHref(b)} aria-label={compact ? b.name : undefined} title={compact ? b.name : undefined}>
							{#if b.logo_url}<img src={b.logo_url} alt="" class="size-5 shrink-0 rounded-pill object-cover" referrerpolicy="no-referrer" />{:else}<span class="side-dot" data-tone={d.tone}></span>{/if}{#if !compact}<span class="truncate">{b.name}</span>{/if}
						</a>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
	<div class="mt-auto grid gap-0.5 border-t border-rule-soft pt-3">
		{@render navList(navEnd, compact)}
		<Menu
			label="Help and resources"
			items={resources}
			fixed
			side={compact}
			align="start"
			block
			triggerClass="side-link {compact ? 'justify-center px-0' : ''} {helpActive ? 'text-ink' : ''}"
		>
			{#snippet trigger()}<Icon name="book" class="side-icon" />{#if !compact}<span class="flex-1">Help &amp; resources</span><Icon name="chevronDown" size={13} class="text-muted" />{/if}{/snippet}
		</Menu>
	</div>
{/snippet}

{#if session.user && !bare}
	<a href="#main" class="sr-only z-50 bg-raised px-3 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:rounded-control focus:shadow-overlay">Skip to content</a>
	<div class="lg:grid {sidebarCollapsed ? 'lg:grid-cols-[68px_minmax(0,1fr)]' : 'lg:grid-cols-[15rem_minmax(0,1fr)]'} lg:transition-[grid-template-columns] lg:duration-200">
		<aside class="sidebar sticky top-0 z-40 hidden h-dvh flex-col overflow-y-auto border-r border-rule-soft px-3 py-4 lg:flex" aria-label="Sidebar">
			{@render sidebar(sidebarCollapsed, true)}
		</aside>
		<div class="min-w-0">
			<header class="topbar sticky top-0 z-30 border-b border-rule-soft backdrop-blur-md">
				<div class="flex h-14 items-center gap-2 px-4 sm:px-6 lg:px-8">
					<button class="btn btn-quiet btn-icon lg:hidden" aria-label="Open menu" aria-expanded={drawer} onclick={() => (drawer = true)}><Icon name="menu" size={18} /></button>
					<a href="/dashboard" class="flex items-center gap-2 lg:hidden" aria-label="RivetPanel overview"><img src="/favicon.svg" alt="" width="24" height="24" class="rounded-control" /></a>
					<button class="hidden h-9 w-full max-w-xs items-center gap-2 rounded-control border border-rule-soft bg-panel px-2.5 text-left text-muted transition-colors hover:border-rule hover:text-ink sm:flex" onclick={() => (palette = true)} aria-label="Go to a bot, server or page (Ctrl+K)">
						<Icon name="search" size={15} /><span class="flex-1 truncate text-small">Go to…</span><kbd class="rounded-inner border border-rule-soft px-1.5 text-[11px]">Ctrl K</kbd>
					</button>
					<div class="ml-auto flex items-center gap-1">
						<button class="tb-btn sm:hidden" onclick={() => (palette = true)} aria-label="Go to a bot, server or page (Ctrl+K)" title="Go to (Ctrl+K)"><Icon name="search" size={17} /></button>
						{#if session.features.ai}<button class="tb-btn" onclick={() => chat.toggle()} aria-label="Ask AI (Ctrl+.)" aria-pressed={chat.open} title="Ask AI (Ctrl+.)"><Icon name="sparkle" size={17} /></button>{/if}
						<NotificationBell />
						<button class="tb-btn" onclick={cycleTheme} aria-label="{themeLabel}. Switch theme" title="{themeLabel}, click to switch">
							<Icon name={theme.pref === 'system' ? 'monitor' : theme.dark ? 'moon' : 'sun'} size={17} />
						</button>
						{#if path !== '/dashboard'}<NewMenu />{/if}
						<Menu label="Account" items={accountItems}>
							{#snippet trigger()}{#if session.user?.avatar_url}<img class="avatar object-cover" src={session.user.avatar_url} alt="" />{:else}<span class="avatar" aria-hidden="true">{initials}</span>{/if}<Icon name="chevronDown" size={14} class="text-muted" />{/snippet}
						</Menu>
					</div>
				</div>
			</header>
			<main id="main" tabindex="-1" class="mx-auto max-w-[1360px] px-4 pt-5 pb-16 outline-none sm:px-6 lg:px-8 lg:pt-6">
				{#if withheld.length && !path.startsWith('/settings/profile')}
					<p class="mb-5 rounded-tile border border-warn/30 bg-warn/7 px-3.5 py-2.5 text-small" role="status">
						Verify your email address to unlock {describePermissions(withheld)}. <a class="link" href="/settings/profile#email">Verify now</a>
					</p>
				{/if}
				{@render children()}
			</main>
		</div>
	</div>

	{#if drawer}
		<div class="fixed inset-0 z-40 bg-black/50 lg:hidden" role="presentation" onclick={() => (drawer = false)}></div>
		<aside class="sidebar pb-safe fixed inset-y-0 left-0 z-50 flex w-72 max-w-[85vw] animate-enter flex-col overflow-y-auto border-r border-rule-soft px-3 py-4 shadow-overlay lg:hidden" aria-label="Menu">
			<button class="btn btn-quiet btn-icon absolute top-3 right-3" aria-label="Close menu" onclick={() => (drawer = false)}><Icon name="x" size={18} /></button>
			{@render sidebar()}
			<button class="side-link mt-0.5" onclick={logout}><Icon name="logout" class="side-icon" />Sign out</button>
		</aside>
	{/if}

	<OperationShelf />
	<CommandPalette bind:open={palette} />
	<AIChat />
{:else if session.loaded && isPublic(path)}
	{@render children()}
{:else if !session.loaded}
<div class="grid min-h-dvh place-items-center" role="status"><span class="text-muted">Loading RivetPanel…</span></div>
{/if}

<DialogHost />
<ToastRegion />
