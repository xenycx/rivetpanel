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
	import Menu from '$lib/components/ui/Menu.svelte';
	import OperationShelf from '$lib/components/OperationShelf.svelte';
	import CommandPalette from '$lib/components/CommandPalette.svelte';
	import AIChat from '$lib/components/AIChat.svelte';
	import { chat } from '$lib/ai/chat.svelte';
	import { theme, cycleTheme } from '$lib/ui/theme.svelte';
	import { describe } from '$lib/status';
	import WorkspaceSwitcher from '$lib/components/WorkspaceSwitcher.svelte';
	import NotificationBell from '$lib/components/NotificationBell.svelte';
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

	const path = $derived(page.url.pathname);
	const adminHome = $derived(session.user && canAdminister() ? (adminLinks()[0]?.href ?? '') : '');
	const withheld = $derived(session.verification.withheld ?? []);
	type NavItem = { href: string; label: string; icon: IconName; active: boolean };
	const nav = $derived<NavItem[]>([
		{ href: '/dashboard', label: 'Bots', icon: 'home', active: path === '/dashboard' || (path.startsWith('/bots') && path !== '/bots/new') },
		...(session.features.games ? [{ href: '/servers', label: 'Game servers', icon: 'gamepad' as IconName, active: path.startsWith('/servers') }] : []),
		{ href: '/templates', label: 'Templates', icon: 'layers', active: path.startsWith('/templates') || path === '/bots/new' },
		...(session.features.sites ? [{ href: '/sites', label: 'Sites', icon: 'globe' as IconName, active: path.startsWith('/sites') }] : []),
		{ href: '/activity', label: 'Activity', icon: 'activity', active: path.startsWith('/activity') },
		...(can('tickets.create') || can('tickets.view_all') || can('tickets.manage')
			? [{ href: '/support', label: 'Support', icon: 'lifebuoy' as IconName, active: path.startsWith('/support') }]
			: []),
		{ href: '/settings/connected-accounts', label: 'Settings', icon: 'gear', active: path.startsWith('/settings') },
		...(adminHome ? [{ href: adminHome, label: 'Administration', icon: 'shield' as IconName, active: path.startsWith('/admin') }] : [])
	]);
	const resources = $derived([
		{ href: '/docs', label: 'Documentation', icon: 'book' as IconName, external: false },
		{ href: '/help', label: 'Help center', icon: 'lifebuoy' as IconName, external: false },
		{ href: '/api/v1/automation/openapi.yaml', label: 'Automation API', icon: 'code' as IconName, external: true },
		{ href: '/', label: 'What RivetPanel does', icon: 'info' as IconName, external: false },
		...(can('system.view') ? [{ href: '/admin/diagnostics', label: 'Status', icon: 'chart' as IconName, external: false }] : [])
	]);
	const bare = $derived((PUBLIC.includes(path) && path !== '/docs') || (!session.user && session.loaded));
	const who = $derived(session.user?.display_name || session.user?.email.split('@')[0] || '');
	const initials = $derived(who.slice(0, 2).toUpperCase());
	const themeLabel = $derived(theme.pref === 'system' ? `System theme (${theme.dark ? 'dark' : 'light'})` : theme.pref === 'dark' ? 'Dark theme' : 'Light theme');
	const accountItems = $derived([
		{ label: session.user?.email ?? '', disabled: true, onselect: () => {} },
		'separator' as const,
		{ label: 'Profile', onselect: () => goto('/settings/profile') },
		{ label: 'Settings', onselect: () => goto('/settings/appearance') },
		'separator' as const,
		{ label: 'Sign out', onselect: logout }
	]);
</script>

<svelte:window onbeforeunload={beforeUnload} />
<svelte:head><title>RivetPanel</title></svelte:head>

{#snippet sidebar(compact = false, collapsible = false)}
	<div class="flex items-center {compact ? 'flex-col justify-center gap-2' : 'gap-2'} px-1">
		<a href="/dashboard" class="flex min-w-0 items-center gap-2.5 {compact ? 'justify-center' : 'px-2'} py-1" aria-label="RivetPanel dashboard" title={compact ? 'RivetPanel dashboard' : undefined}>
			<img src="/favicon.svg" alt="" width="30" height="30" class="shrink-0 rounded-tile" />
			{#if !compact}<span class="truncate font-semibold tracking-[0.08em] uppercase">RivetPanel</span>{/if}
		</a>
		{#if collapsible}
			<button class="tb-btn {compact ? '' : 'ml-auto'} shrink-0" onclick={toggleSidebar} aria-label={compact ? 'Expand sidebar' : 'Collapse sidebar'} title={compact ? 'Expand sidebar' : 'Collapse sidebar'}>
				<Icon name={compact ? 'chevronRight' : 'chevronLeft'} size={16} />
			</button>
		{/if}
	</div>
	<div class="mt-5 {compact ? 'flex justify-center' : 'px-1'}"><WorkspaceSwitcher {compact} /></div>
	<nav class="mt-4" aria-label="Main">
		<ul class="grid gap-0.5">
			{#each nav as n (n.href)}
				<li>
					<a href={n.href} class="side-link {compact ? 'justify-center px-0' : ''}" data-active={n.active} aria-current={n.active ? 'page' : undefined} aria-label={compact ? n.label : undefined} title={compact ? n.label : undefined}>
						<Icon name={n.icon} class="side-icon" />{#if !compact}<span>{n.label}</span>{/if}
					</a>
				</li>
			{/each}
		</ul>
	</nav>
	<div class="my-5 border-t border-rule-soft"></div>
	<div>
		{#if !compact}<p class="eyebrow flex items-center justify-between px-3">Favorites <a href="/dashboard" class="text-muted hover:text-ink" aria-label="Star bots on the overview"><Icon name="plus" size={14} /></a></p>{/if}
		<ul class="mt-2 grid gap-0.5">
			{#each favorites as b (b.id)}
				{@const d = describe(b)}
				<li>
					<a href={resourceHref(b)} class="side-link {compact ? 'justify-center px-0' : ''}" data-active={path === resourceHref(b)} aria-label={compact ? b.name : undefined} title={compact ? b.name : undefined}>
						{#if b.logo_url}<img src={b.logo_url} alt="" class="size-6 shrink-0 rounded-pill object-cover" referrerpolicy="no-referrer" />{:else}<span class="side-dot" data-tone={d.tone}></span>{/if}{#if !compact}<span class="truncate">{b.name}</span>{/if}
					</a>
				</li>
			{:else}
				{#if !compact}<li class="px-3 text-small text-muted">Star a bot to pin it here.</li>{/if}
			{/each}
		</ul>
	</div>
	<div class="my-5 border-t border-rule-soft"></div>
	<div>
		{#if !compact}<p class="eyebrow px-3">Resources</p>{/if}
		<ul class="mt-2 grid gap-0.5">
			{#each resources as r (r.href)}
				<li>
					<a href={r.href} class="side-link group {compact ? 'justify-center px-0' : ''}" target={r.external ? '_blank' : undefined} rel={r.external ? 'noopener' : undefined} aria-label={compact ? r.label : undefined} title={compact ? r.label : undefined}>
						<Icon name={r.icon} class="side-icon" />{#if !compact}<span class="flex-1">{r.label}</span>{/if}
						{#if r.external && !compact}<Icon name="external" size={13} class="text-muted opacity-60 group-hover:opacity-100" />{/if}
					</a>
				</li>
			{/each}
		</ul>
	</div>
{/snippet}

{#if session.user && !bare}
	<a href="#main" class="sr-only z-50 bg-raised px-3 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:rounded-control focus:shadow-overlay">Skip to content</a>
	<div class="lg:grid {sidebarCollapsed ? 'lg:grid-cols-[72px_minmax(0,1fr)]' : 'lg:grid-cols-[16.5rem_minmax(0,1fr)]'} lg:transition-[grid-template-columns] lg:duration-200">
		<aside class="sidebar sticky top-0 z-40 hidden h-dvh flex-col overflow-y-auto border-r border-rule-soft px-3 py-5 lg:flex" aria-label="Sidebar">
			{@render sidebar(sidebarCollapsed, true)}
		</aside>
		<div class="min-w-0">
			<header class="topbar sticky top-0 z-30 border-b border-rule-soft backdrop-blur-md">
				<div class="flex h-16 items-center gap-2 px-4 sm:px-6 lg:px-10">
					<button class="btn btn-quiet btn-icon lg:hidden" aria-label="Open menu" aria-expanded={drawer} onclick={() => (drawer = true)}><Icon name="menu" size={18} /></button>
					<a href="/dashboard" class="flex items-center gap-2 lg:hidden" aria-label="RivetPanel dashboard"><img src="/favicon.svg" alt="" width="26" height="26" class="rounded-tile" /></a>
					<p class="hidden truncate text-title text-muted sm:block">Welcome back, <span class="font-semibold text-ink">{who}</span></p>
					<div class="ml-auto flex items-center gap-1.5">
						{#if session.features.ai}<button class="tb-btn {chat.open ? 'text-action' : ''}" onclick={() => chat.toggle()} aria-label="Ask AI (Ctrl+.)" aria-pressed={chat.open} title="Ask AI (Ctrl+.)"><Icon name="sparkle" size={17} /></button>{/if}
						<button class="tb-btn" onclick={() => (palette = true)} aria-label="Go to a bot or page (Ctrl+K)" title="Go to (Ctrl+K)"><Icon name="search" size={17} /></button>
						<a class="tb-btn hidden sm:grid" href="/activity" aria-label="Activity" title="Activity"><Icon name="history" size={17} /></a>
						<NotificationBell />
						<button class="tb-btn" onclick={cycleTheme} aria-label="{themeLabel}. Switch theme" title="{themeLabel} · click to switch">
							<Icon name={theme.pref === 'system' ? 'monitor' : theme.dark ? 'moon' : 'sun'} size={17} />
						</button>
						{#if can('bots.create')}<a href="/bots/new" class="btn btn-primary ml-1"><Icon name="plus" />New</a>{/if}
						<Menu label="Account" items={accountItems}>
							{#snippet trigger()}{#if session.user?.avatar_url}<img class="avatar object-cover" src={session.user.avatar_url} alt="" />{:else}<span class="avatar" aria-hidden="true">{initials}</span>{/if}<Icon name="chevronDown" size={14} class="text-muted" />{/snippet}
						</Menu>
					</div>
				</div>
			</header>
			<main id="main" tabindex="-1" class="mx-auto max-w-[1400px] px-4 pt-6 pb-24 outline-none sm:px-6 lg:px-10 lg:pt-8">
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
		<aside class="sidebar pb-safe fixed inset-y-0 left-0 z-50 flex w-72 max-w-[85vw] animate-enter flex-col overflow-y-auto border-r border-rule-soft px-3 py-5 shadow-overlay lg:hidden" aria-label="Menu">
			<button class="btn btn-quiet btn-icon absolute top-4 right-3" aria-label="Close menu" onclick={() => (drawer = false)}><Icon name="x" size={18} /></button>
			{@render sidebar()}
			<div class="mt-auto pt-6">
				<button class="side-link w-full" onclick={logout}><Icon name="logout" class="side-icon" />Sign out</button>
			</div>
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
