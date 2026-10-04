<script lang="ts">
	import { goto } from '$app/navigation';
	import { api } from '$lib/api/client';
	import type { Bot, Site, SiteTemplate } from '$lib/api/types';
	import type { EnvView } from '$lib/api/admin';
	import { describe } from '$lib/status';
	import { session, logout, can } from '$lib/session.svelte';
	import { adminLinks } from '$lib/adminNav';
	import { createOptions } from '$lib/create';
	import { resourceHref } from '$lib/api/games';
	import { chat, type Conversation } from '$lib/ai/chat.svelte';
	import { cycleTheme, theme } from '$lib/ui/theme.svelte';
	import { workspaces } from '$lib/workspaces.svelte';
	import { adminPages, botTabs, notGameTabs, docs, groups, pages, score, scopes, type Entry, type Scope } from '$lib/search';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// Jump to anything: bots and their sections, sites, every page, settings and
	// administration section, environment variables, people, AI chats and the
	// documentation, or run an action. Ctrl+K (Cmd+K) anywhere, or the header
	// button. Several words narrow the search; Tab switches the scope.
	let { open = $bindable(false) }: { open?: boolean } = $props();

	let q = $state('');
	let scope = $state<Scope>('all');
	let active = $state(0);
	let list: HTMLUListElement | undefined = $state();

	let bots = $state<Bot[]>([]);
	let sites = $state<Site[]>([]);
	let siteTemplates = $state<SiteTemplate[]>([]);
	let people = $state<{ id: string; email: string; display_name?: string; role: string }[]>([]);
	let envNames = $state<{ name: string; description: string }[]>([]);
	let chats = $state<Conversation[]>([]);
	// Enter pressed before the lists arrived waits for them, so fast typing
	// still lands on the right thing.
	let loading: Promise<unknown> = Promise.resolve();

	const isAdmin = $derived(session.user?.role === 'admin');
	const aiOn = $derived(session.features.ai);

	$effect(() => {
		if (!open) return;
		q = '';
		scope = 'all';
		active = 0;
		const jobs: Promise<unknown>[] = [api<{ bots: Bot[] }>('GET', '/bots').then((r) => (bots = r.bots))];
		if (session.features.sites) jobs.push(api<{ sites: Site[] }>('GET', '/sites').then((r) => (sites = r.sites)));
		if (session.features.sites && can('sites.create')) jobs.push(api<{ templates: SiteTemplate[] }>('GET', '/site-templates').then((r) => (siteTemplates = r.templates)));
		if (aiOn) jobs.push(api<{ conversations: Conversation[] }>('GET', '/ai/conversations').then((r) => (chats = r.conversations)));
		if (isAdmin) {
			jobs.push(api<{ users: typeof people }>('GET', '/users').then((r) => (people = r.users)));
			jobs.push(api<EnvView>('GET', '/admin/environment').then((r) => (envNames = r.vars.map((v) => ({ name: v.name, description: v.description })))));
		}
		loading = Promise.allSettled(jobs);
	});

	const actions = $derived.by<Entry[]>(() => {
		const a: Entry[] = [
			...(can('bots.create') ? [{ key: 'x-newbot', group: 'action', label: 'New bot', hint: 'Create a Discord bot', icon: 'plus' as const, href: '/bots/new', words: 'create add deploy start', pinned: true }] : []),
			...(can('bots.create') && session.features.games ? [{ key: 'x-newserver', group: 'action', label: 'New game server', hint: 'Minecraft or a Steam game', icon: 'plus' as const, href: '/servers/new', words: 'create add game minecraft steam valheim rust zomboid', pinned: true }] : []),
			// Everything else the "New" menu offers, as "New …" actions.
			...createOptions()
				.filter((o) => !o.key.startsWith('bot-') && !o.key.startsWith('game-'))
				.map((o) => ({ key: `x-new-${o.key}`, group: 'action', label: o.action ?? `New ${o.label.charAt(0).toLowerCase()}${o.label.slice(1)}`, hint: o.hint, icon: 'plus' as const, href: o.href, words: `create add ${o.words ?? ''}` })),
			// One action per site template: opens the new-site form with it chosen.
			...siteTemplates.map((t) => ({ key: `x-site-tpl-${t.id}`, group: 'action', label: `New site from ${t.name}`, hint: `Site template · ${t.description}`, icon: 'globe' as const, href: `/templates?tab=sites&create=${t.id}`, words: `create add site template starter ${t.tags.join(' ')}` })),
			{ key: 'x-theme', group: 'action', label: 'Switch theme', hint: `Now: ${theme.pref === 'system' ? 'follow system' : theme.pref}. Light, dark, system`, icon: 'moon', run: cycleTheme, words: 'dark light mode appearance' },
			{ key: 'x-logout', group: 'action', label: 'Sign out', hint: session.user?.email ?? '', icon: 'logout', run: () => void logout(), words: 'log out exit' }
		];
		if (aiOn) {
			a.unshift(
				{ key: 'x-ai', group: 'action', label: 'Ask AI', hint: 'Open the assistant (Ctrl+.)', icon: 'sparkle', run: () => chat.show(), words: 'assistant chat help gpt llm question', pinned: true },
				{ key: 'x-ai-new', group: 'action', label: 'New AI chat', hint: 'Start a fresh conversation', icon: 'sparkle', run: () => { chat.newChat(); chat.show(); }, words: 'assistant conversation clear' }
			);
		}
		if (isAdmin) a.push({ key: 'x-diag', group: 'action', label: 'Run diagnostics', hint: 'Check Docker, disk, keys and backups', icon: 'shield', href: '/admin/diagnostics', words: 'doctor health check status' });
		return a;
	});

	const catalog = $derived.by<Entry[]>(() => {
		const hidden: Record<string, boolean> = {
			'p-servers': !session.features.games,
			'p-sites': !session.features.sites,
			'p-support': !(can('tickets.create') || can('tickets.view_all') || can('tickets.manage'))
		};
		const out: Entry[] = [...actions, ...pages.filter((p) => !hidden[p.key])];
		for (const b of bots) {
			const d = describe(b);
			const game = b.kind === 'game';
			out.push({ key: `b-${b.id}`, group: 'bot', label: b.name, hint: `${d.label} · ${game ? 'game server' : b.runtime}${b.tags.length ? ` · ${b.tags.join(' ')}` : ''}`, icon: game ? 'gamepad' : 'box', href: resourceHref(b), words: `${b.tags.join(' ')} ${b.runtime} ${game ? 'game server' : 'bot'} ${b.owner_id === session.user?.id ? '' : 'shared'}`, boost: b.favorite ? 8 : 0 });
		}
		for (const s of sites) out.push({ key: `si-${s.id}`, group: 'site', label: s.name, hint: `Site · ${s.slug}${s.workspace_name ? ` · ${s.workspace_name}` : ''}`, icon: 'globe', href: `/sites/${s.id}`, words: s.slug });
		for (const c of chats.slice(0, 30)) out.push({ key: `c-${c.id}`, group: 'chat', label: c.title || 'Untitled chat', hint: 'AI chat', icon: 'sparkle', run: () => { chat.show(); void chat.openConversation(c); } });
		for (const w of workspaces.list) out.push({ key: `w-${w.id}`, group: 'person', label: w.name, hint: 'Workspace', icon: 'building', href: isAdmin ? `/admin/workspaces/${w.id}` : `/settings/workspaces/${w.id}` });
		out.push(...docs.map((d) => ({ ...d, boost: -10 })));
		// Administration sections this role may open (all of them for administrators).
		const known = new Set(isAdmin ? adminPages.map((p) => p.href) : []);
		for (const l of adminLinks()) if (!known.has(l.href)) out.push({ key: `a-${l.href}`, group: 'admin', label: l.label, hint: 'Administration', icon: l.icon, href: l.href });
		if (isAdmin) {
			out.push(...adminPages);
			for (const p of people) out.push({ key: `u-${p.id}`, group: 'person', label: p.display_name || p.email, hint: `${p.email} · ${p.role}`, icon: 'users', href: `/admin/users/${p.id}`, words: p.email });
			for (const v of envNames) out.push({ key: `e-${v.name}`, group: 'env', label: v.name, hint: v.description, icon: 'sliders', href: `/admin/environment?find=${v.name}`, boost: -70 });
		}
		return out;
	});

	const inScope = (e: Entry, sc: Scope) => sc === 'all' || groups.find((g) => g.id === e.group)?.scope === sc;
	const groupOrder = (e: Entry) => groups.findIndex((g) => g.id === e.group);
	const MAX = 40;

	const items = $derived.by<Entry[]>(() => {
		let text = q.trim();
		let sc = scope;
		if (text.startsWith('>')) {
			sc = 'actions';
			text = text.slice(1).trim();
		}
		const base = catalog.filter((e) => inScope(e, sc));
		let found: (Entry & { s: number })[];
		if (!text) {
			// Nothing typed: everything, grouped, with the common actions first.
			found = base.map((e) => ({ ...e, s: 1 }));
			found.sort((a, b) => Number(!!b.pinned) - Number(!!a.pinned) || groupOrder(a) - groupOrder(b));
			const pinned = found.filter((e) => e.pinned);
			const rest = found.filter((e) => !e.pinned);
			return [...pinned, ...rest];
		}
		found = base.map((e) => ({ ...e, s: score(e, text) })).filter((e) => e.s > 0).map((e) => ({ ...e, s: e.s + (e.boost ?? 0) })).filter((e) => e.s > 0);
		// "bluntly files": the best matching bots also offer their own sections.
		const words = text.toLowerCase().split(/\s+/);
		if (sc === 'all' || sc === 'bots') {
			for (const b of bots) {
				const name = b.name.toLowerCase();
				if (!words.some((w) => name.includes(w))) continue;
				const rest = words.filter((w) => !name.includes(w)).join(' ');
				const isGame = b.kind === 'game';
				for (const t of botTabs) {
					if (isGame && notGameTabs.has(t.id)) continue;
					const s = rest ? score({ label: t.label, hint: '', words: t.words }, rest) : 1;
					if (s > 0) found.push({ key: `bt-${b.id}-${t.id}`, group: 'bot-tab', label: `${b.name} › ${t.label}`, hint: isGame ? 'Server section' : 'Bot section', icon: t.icon, href: `${isGame ? '/servers' : '/bots'}/${b.id}?tab=${t.id}`, s: rest ? s - 2 : 2 });
				}
			}
		}
		found.sort((a, b) => b.s - a.s || groupOrder(a) - groupOrder(b));
		const top = found.slice(0, MAX);
		// Keep the groups together, best group (by its best hit) first.
		const best = new Map<string, number>();
		for (const e of top) best.set(e.group, Math.max(best.get(e.group) ?? 0, e.s));
		top.sort((a, b) => (best.get(b.group)! - best.get(a.group)!) || groupOrder(a) - groupOrder(b) || b.s - a.s);
		if (aiOn && (sc === 'all' || sc === 'actions' || sc === 'chats')) {
			top.push({ key: 'x-ask', group: 'action', label: `Ask AI: “${text}”`, hint: 'Send this question to the assistant', icon: 'sparkle', run: () => void chat.ask(text, false), s: 0 });
		}
		return top;
	});
	// Group heading shown above the first item of each run.
	const headings = $derived(items.map((e, i) => (i === 0 || e.group !== items[i - 1].group || (q.trim() === '' && e.pinned !== items[i - 1].pinned) ? (q.trim() === '' && e.pinned ? 'Suggested' : (groups.find((g) => g.id === e.group)?.label ?? '')) : '')));

	function choose(i: number) {
		const it = items[i];
		if (!it) return;
		open = false;
		if (it.run) it.run();
		else if (it.href) {
			if (it.href.startsWith('/api/')) window.open(it.href, '_blank', 'noopener');
			else goto(it.href);
		}
	}
	function cycleScope(dir: 1 | -1) {
		const i = scopes.findIndex((s) => s.id === scope);
		scope = scopes[(i + dir + scopes.length) % scopes.length].id;
		active = 0;
	}
	function onkey(e: KeyboardEvent) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = Math.min(items.length - 1, active + 1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = Math.max(0, active - 1);
		} else if (e.key === 'Tab') {
			e.preventDefault();
			cycleScope(e.shiftKey ? -1 : 1);
		} else if (e.key === 'Enter') {
			e.preventDefault();
			void loading.then(() => choose(active));
		}
		queueMicrotask(() => list?.querySelector(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' }));
	}
	function global(e: KeyboardEvent) {
		if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k' && session.user) {
			e.preventDefault();
			open = !open;
		}
	}
	const counts = $derived.by(() => {
		const c: Record<string, number> = {};
		for (const s of scopes) c[s.id] = catalog.filter((e) => inScope(e, s.id)).length;
		return c;
	});
</script>

<svelte:window onkeydown={global} />

<Dialog bind:open title="Go to" size="lg">
	<label class="relative block">
		<span class="sr-only">Search bots, sites, pages, settings and actions</span>
		<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
		<input
			class="field pl-8"
			placeholder="Search bots, pages, settings, users, variables… or type > for actions"
			bind:value={q}
			oninput={() => (active = 0)}
			onkeydown={onkey}
			role="combobox"
			aria-expanded="true"
			aria-controls="palette-list"
			aria-activedescendant={items[active] ? `pal-${items[active].key}` : undefined}
			autocomplete="off"
			data-autofocus
		/>
	</label>
	<div class="mt-2 flex flex-wrap gap-1" role="group" aria-label="Search in">
		{#each scopes.filter((s) => s.id === 'all' || counts[s.id] > 0) as s (s.id)}
			<button type="button" tabindex="-1" class="rounded-pill border px-2.5 py-0.5 text-small {scope === s.id ? 'border-action bg-action/10 font-medium text-ink' : 'border-rule-soft text-muted hover:text-ink'}" aria-pressed={scope === s.id} onclick={() => { scope = s.id; active = 0; }}>{s.label}</button>
		{/each}
	</div>
	<ul bind:this={list} id="palette-list" role="listbox" aria-label="Results" class="mt-2 max-h-[min(26rem,55vh)] overflow-y-auto pb-2">
		{#each items as it, i (it.key)}
			{#if headings[i]}<li role="presentation" class="eyebrow px-2 pt-2.5 pb-0.5 first:pt-1">{headings[i]}</li>{/if}
			<li
				id="pal-{it.key}"
				data-i={i}
				role="option"
				aria-selected={i === active}
				class="flex cursor-pointer items-center gap-2.5 rounded-control px-2 py-1 text-[13px] {i === active ? 'bg-paper' : ''}"
				onmousemove={() => (active = i)}
				onclick={() => choose(i)}
				onkeydown={() => {}}
			>
				<Icon name={it.icon} size={15} class="text-muted" />
				<span class="min-w-0 flex-1"><span class="block truncate font-medium">{it.label}</span><span class="block truncate text-[11.5px] leading-4 text-muted">{it.hint}</span></span>
				{#if i === active}<kbd class="text-small text-muted">Enter</kbd>{/if}
			</li>
		{:else}
			<li class="px-2 py-3 text-muted">Nothing matches “{q}”{scope !== 'all' ? ` in ${scopes.find((s) => s.id === scope)?.label}` : ''}.{#if aiOn} Press Tab to widen the search, or ask the assistant.{/if}</li>
		{/each}
	</ul>
	<p class="mt-1 border-t border-rule-soft pt-2 text-small text-muted"><kbd>↑</kbd> <kbd>↓</kbd> move · <kbd>Enter</kbd> open · <kbd>Tab</kbd> change scope · several words narrow the search</p>
</Dialog>
