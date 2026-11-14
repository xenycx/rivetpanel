<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { session } from '$lib/session.svelte';
	import { chat, type Change, type Message, type RunBlock, type Step } from '$lib/ai/chat.svelte';
	import { currentView, describeView, type ViewContext } from '$lib/ai/context.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';

	// One assistant for the whole panel. It follows the person from page to
	// page, and every message carries what they were looking at.
	const view = $derived(currentView(page.url.pathname, page.url.search));
	let attach = $state(true);
	let screen = $state<'chat' | 'history' | 'settings'>('chat');
	let scroller = $state<HTMLDivElement | null>(null);
	let box = $state<HTMLTextAreaElement | null>(null);
	let pinned = true;

	// Another account must never see this chat.
	let lastUser = '';
	$effect(() => {
		const u = session.user?.id ?? '';
		if (u !== lastUser) {
			if (lastUser) chat.reset();
			lastUser = u;
		}
	});

	// A different bot or site starts with its context attached again.
	$effect(() => {
		void view.id;
		attach = true;
	});
	const sent = $derived<ViewContext | null>(attach ? view : null);
	chat.currentView = () => sent;
	const hasTarget = $derived(!!sent && sent.kind !== 'page');
	const autopilot = $derived(chat.mode === 'auto');
	const isAdmin = $derived(session.user?.role === 'admin');
	const noProvider = $derived(chat.ready && chat.providers.length === 0);

	const suggestions = $derived(
		view.kind === 'bot' && attach
			? ['Why isn’t my bot starting?', 'Check the latest console output for errors', 'Review my startup command and entry file']
			: view.kind === 'site' && attach
				? ['Why does my page look broken?', 'Review my index.html for problems']
				: ['Deploy a GitHub repository for me', 'Run a Docker image and give it a web address', 'Which of my apps are having problems?', 'Switch the panel to dark mode']
	);

	// Keep the newest output in view unless the reader scrolled up.
	$effect(() => {
		void chat.messages.length;
		void chat.latest?.streaming;
		void chat.latest?.steps.length;
		void chat.latest?.status;
		void screen;
		if (scroller && pinned) tick().then(() => scroller && (scroller.scrollTop = scroller.scrollHeight));
	});
	$effect(() => {
		if (chat.open && screen === 'chat') tick().then(() => box?.focus());
	});
	onMount(() => {
		const onKey = (e: KeyboardEvent) => {
			if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key === '.') {
				e.preventDefault();
				chat.toggle();
			}
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});

	type Item = { t: 'msg'; m: Message } | { t: 'run'; r: RunBlock };
	// Each run belongs right after the message that started it.
	const timeline = $derived.by((): Item[] => {
		const out: Item[] = [];
		const used = new Set<string>();
		const msgs = chat.messages;
		msgs.forEach((m, i) => {
			out.push({ t: 'msg', m });
			if (m.role !== 'user') return;
			const next = msgs.slice(i + 1).find((x) => x.role === 'user');
			for (const r of chat.runs) {
				if (r.created_at_ms >= m.created_at_ms && (!next || r.created_at_ms < next.created_at_ms) && !used.has(r.id)) {
					used.add(r.id);
					out.push({ t: 'run', r });
				}
			}
		});
		for (const r of chat.runs) if (!used.has(r.id)) out.push({ t: 'run', r });
		return out;
	});

	const live = (r: RunBlock) => ['queued', 'running', 'waiting_approval'].includes(r.status);
	const parse = (s?: string) => {
		try {
			return s ? JSON.parse(s) : {};
		} catch {
			return {};
		}
	};
	function label(s: Step): string {
		const a = parse(s.args);
		switch (s.name) {
			case 'target_status': return 'Checked status';
			case 'read_logs': return 'Read console output';
			case 'build_output': return 'Read build output';
			case 'list_files': return `Listed ${a.path && a.path !== '.' ? a.path : 'the project root'}`;
			case 'read_file': return `Read ${a.path ?? 'a file'}`;
			case 'search_files': return `Searched files for “${a.query ?? ''}”`;
			case 'environment_names': return 'Checked variable names';
			case 'request_environment_values': return `Asked for ${(a.names ?? []).join(', ') || 'values'}`;
			case 'web_search': return `Searched the web for “${a.query ?? ''}”`;
			case 'web_fetch': return `Read ${a.url ?? 'a web page'}`;
			case 'propose_file_change': return `${a.delete ? 'Delete' : 'Change'} ${a.path ?? 'a file'}`;
			case 'run_diagnostic': return `Ran ${(a.argv ?? []).join(' ') || 'a diagnostic'}`;
			case 'restart_bot': return 'Restart the bot';
			case 'list_targets': return 'Listed your bots and sites';
			case 'focus_target': return 'Opened a bot or site';
			case 'auto_repair_envelope': return 'Auto repair plan';
			case 'panel_docs': return `Looked up “${a.query ?? ''}” in the guide`;
			case 'read_doc': return `Read the guide: ${a.page ?? ''}${a.section ? ` › ${a.section}` : ''}`;
			case 'panel_info': return 'Checked what this panel offers';
			case 'analyze_repo': return `Analyzed ${a.repo ?? 'a repository'}`;
			case 'deploy_github': return `Deployed ${a.repo ?? 'a repository'}`;
			case 'deploy_site': return `Published a site from ${a.repo ?? 'GitHub'}`;
			case 'deploy_image': return `Ran ${a.image ?? 'a Docker image'}`;
			case 'deploy_compose': return a.dry_run ? 'Checked a compose file' : `Imported compose stack${a.name ? ` “${a.name}”` : ''}`;
			case 'list_app_templates': return a.query ? `Searched apps for “${a.query}”` : 'Browsed the app catalog';
			case 'install_app_template': return `Installed ${a.template_id ?? 'an app'}`;
			case 'wait_for_ready': return 'Waited for it to start';
			case 'set_environment': return `Set ${Object.keys(a.vars ?? {}).join(', ') || 'variables'}`;
			case 'delete_environment': return `Removed ${(a.names ?? []).join(', ') || 'variables'}`;
			case 'power': return ({ start: 'Started', stop: 'Stopped', restart: 'Restarted', kill: 'Killed' } as Record<string, string>)[a.action] ?? 'Changed power state';
			case 'update_bot_settings': return 'Changed settings';
			case 'redeploy': return 'Redeployed the latest commit';
			case 'delete_bot': return 'Deleted an app';
			case 'console_command': return `Sent “${a.line ?? ''}” to the console`;
			case 'panel_routes': return a.query ? `Looked up ${a.query} in the panel API` : 'Looked up the panel API';
			case 'panel_api': return `${a.method ?? 'GET'} ${a.path ?? ''}`;
			case 'client_action': return a.action === 'navigate' ? `Opened ${a.value}` : `Set ${String(a.action ?? '').replace('set_', '')} to ${a.value}`;
		}
		return s.name.replaceAll('_', ' ');
	}
	const done = (s: Step) => ['completed', 'approved', 'configured'].includes(s.status);
	const bad = (s: Step) => ['failed', 'rejected', 'cancelled'].includes(s.status);
	const summary = (r: RunBlock) => {
		const n = r.steps.filter((s) => s.name !== 'auto_repair_envelope').length;
		const t = r.input_tokens + r.output_tokens;
		return `${live(r) ? 'Working' : 'Worked'} · ${n} step${n === 1 ? '' : 's'}${t ? ` · ${t.toLocaleString()} tokens` : ''}`;
	};
	const pending = (r: RunBlock) => r.steps.filter((s) => s.approval || s.secure);
	// Links to what a run created: the app's page and its public address.
	const DEPLOYS = new Set(['deploy_github', 'deploy_site', 'deploy_image', 'install_app_template']);
	function made(r: RunBlock): { href: string; label: string; external: boolean }[] {
		const out: { href: string; label: string; external: boolean }[] = [];
		for (const s of r.steps) {
			if (!DEPLOYS.has(s.name) || s.status !== 'completed' || !s.output) continue;
			const o = parse(s.output);
			const page = typeof o.page === 'string' && o.page.startsWith('/') ? o.page : '';
			if (page) out.push({ href: page, label: `Open ${o.name ?? (s.name === 'deploy_site' ? 'site' : 'app')}`, external: false });
			if (typeof o.url === 'string' && /^https?:\/\//.test(o.url)) out.push({ href: o.url, label: o.url.replace(/^https?:\/\//, '').replace(/\/$/, ''), external: true });
			const id = page ? undefined : /\(id ([0-9a-f-]{36})\)/.exec(s.output)?.[1];
			if (id) out.push({ href: `/bots/${id}`, label: 'Open app', external: false });
		}
		return out;
	}
	const when = (ms: number) => new Date(ms).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
			e.preventDefault();
			void chat.send();
		}
	}
	function pick(text: string) {
		chat.prompt = text;
		void chat.send();
	}
</script>

{#snippet diff(text: string)}
	<pre class="max-h-56 overflow-auto rounded-control bg-term p-2 font-mono text-[.7rem] leading-snug text-term-ink">{#each text.split('\n') as line}<span class={line.startsWith('+') && !line.startsWith('+++') ? 'text-run' : line.startsWith('-') && !line.startsWith('---') ? 'text-fail' : line.startsWith('@@') ? 'text-muted' : ''}>{line}
</span>{/each}</pre>
{/snippet}

{#snippet changeCard(r: RunBlock, ch: Change)}
	<section class="rounded-tile border border-action/30 bg-action/5 p-2.5">
		<div class="flex items-center gap-2">
			<Icon name="commit" class="text-action" />
			<strong class="min-w-0 flex-1 text-small leading-snug">{ch.summary}</strong>
			<span class="pill" data-tone={ch.status === 'applied' ? 'run' : ch.status === 'conflicted' || ch.status === 'failed' ? 'fail' : undefined}>{ch.status}</span>
		</div>
		{#each ch.files ?? [] as f}
			<details class="mt-2" open={ch.status === 'draft'}>
				<summary class="cursor-pointer font-mono text-small"><span class="mr-1 text-action">{f.operation}</span>{f.path}</summary>
				<div class="mt-1">{@render diff(f.diff)}</div>
			</details>
		{/each}
		{#if ch.status === 'applied' && !live(r)}
			<button class="btn btn-sm mt-2" onclick={() => chat.undo(r.id, ch.id)}><Icon name="history" />Undo</button>
		{/if}
	</section>
{/snippet}

{#snippet approvalCard(r: RunBlock, s: Step)}
	{@const a = parse(s.args)}
	<section class="rounded-tile border border-warn/40 bg-warn/6 p-2.5" aria-label={s.title ?? label(s)}>
		<p class="flex items-center gap-2 text-small font-semibold"><Icon name="shield" class="text-warn" />{s.title ?? label(s)}</p>
		{#if s.name === 'run_diagnostic'}
			<p class="mt-1.5 overflow-x-auto rounded-control bg-term px-2 py-1.5 font-mono text-[.72rem] whitespace-nowrap text-term-ink">$ {(a.argv ?? []).join(' ')}</p>
			<p class="mt-1 text-[.72rem] text-muted">Runs offline in a throwaway copy of the files. Your live bot is not touched.</p>
		{:else if s.name === 'restart_bot'}
			<p class="mt-1 text-small text-muted">The bot restarts with its current files and variables.</p>
		{:else if s.name === 'propose_file_change'}
			<p class="mt-1 text-small text-muted">Review the diff above, then apply it to <span class="font-mono">{a.path}</span>. Undo stays available.</p>
		{:else if s.name === 'panel_api'}
			<p class="mt-1.5 overflow-x-auto rounded-control bg-term px-2 py-1.5 font-mono text-[.72rem] whitespace-nowrap text-term-ink">{a.method} {a.path}</p>
		{:else if s.detail && typeof s.detail === 'object'}
			<dl class="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[.72rem]">
				{#each Object.entries(s.detail).filter(([, v]) => v != null && v !== '' && !(Array.isArray(v) && !v.length)) as [k, v]}
					<dt class="text-muted">{k.replaceAll('_', ' ')}</dt>
					<dd class="min-w-0 truncate font-mono text-ink" title={Array.isArray(v) ? v.join(' ') : String(v)}>{Array.isArray(v) ? v.join(' ') : String(v)}</dd>
				{/each}
			</dl>
		{/if}
		{#if s.secure}
			<div class="mt-2 space-y-2">
				<p class="text-[.72rem] text-muted">Values go straight to the bot’s environment. The assistant never sees them.</p>
				{#each s.names ?? [] as n}
					<label class="block"><span class="label font-mono">{n}</span>
						<input class="field" type="password" autocomplete="new-password" value={chat.secureValues[s.id]?.[n] ?? ''} oninput={(e) => { chat.secureValues[s.id] ??= {}; chat.secureValues[s.id][n] = e.currentTarget.value; }} />
					</label>
				{/each}
				<div class="flex gap-2">
					<button class="btn btn-sm btn-primary" onclick={() => chat.secure(r.id, s)}>Save securely</button>
					<button class="btn btn-sm" onclick={() => chat.decide(r.id, s, false)}>Skip</button>
				</div>
			</div>
		{:else}
			<div class="mt-2 flex gap-2">
				<button class="btn btn-sm btn-primary" onclick={() => chat.decide(r.id, s, true)}>Approve</button>
				<button class="btn btn-sm" onclick={() => chat.decide(r.id, s, false)}>Reject</button>
			</div>
		{/if}
	</section>
{/snippet}

{#snippet runBlock(r: RunBlock)}
	{@const steps = r.steps.filter((s) => s.name !== 'auto_repair_envelope')}
	<div class="space-y-2">
		{#if steps.length || live(r)}
			<details class="group rounded-tile border border-rule-soft bg-paper-2/40" open={live(r) || undefined}>
				<summary class="flex cursor-pointer items-center gap-2 px-2.5 py-1.5 text-small text-muted select-none">
					{#if live(r)}<span class="size-2 shrink-0 animate-pulse rounded-pill bg-action" aria-hidden="true"></span>{:else}<Icon name="activity" size={13} />{/if}
					<span class="min-w-0 flex-1 truncate">{summary(r)}{#if r.focus}{' '}· {r.focus}{/if}</span>
					<Icon name="chevronDown" size={13} class="transition-transform group-open:rotate-180" />
				</summary>
				<ul class="space-y-0.5 border-t border-rule-soft px-2.5 py-1.5">
					{#each steps as s (s.id)}
						<li>
							<details class="group/step">
								<summary class="flex cursor-pointer items-center gap-2 py-0.5 text-small select-none {s.output || s.args ? '' : 'pointer-events-none'}">
									{#if done(s)}<Icon name="check" size={13} class="text-run" />
									{:else if bad(s)}<Icon name="alert" size={13} class="text-fail" />
									{:else if s.status === 'waiting'}<Icon name="clock" size={13} class="text-warn" />
									{:else}<span class="mx-[3px] size-2 shrink-0 animate-pulse rounded-pill bg-action" aria-hidden="true"></span>{/if}
									<span class="min-w-0 flex-1 truncate {bad(s) ? 'text-fail' : ''}" title={label(s)}>{label(s)}</span>
									{#if s.duration}<span class="font-mono text-[.68rem] text-muted">{s.duration < 1000 ? `${s.duration} ms` : `${(s.duration / 1000).toFixed(1)} s`}</span>{/if}
								</summary>
								{#if s.output}<pre class="mt-1 mb-1 max-h-48 overflow-auto rounded-control bg-term p-2 font-mono text-[.7rem] leading-snug whitespace-pre-wrap text-term-ink">{s.exit != null ? `exit ${s.exit}\n` : ''}{s.output}</pre>{/if}
							</details>
						</li>
					{/each}
					{#if live(r) && r.note}<li class="pt-1 text-[.72rem] text-muted italic">{r.note.length > 140 ? r.note.slice(0, 140) + '…' : r.note}</li>{/if}
				</ul>
			</details>
		{/if}
		{#if made(r).length}
			<div class="flex flex-wrap gap-1.5">
				{#each made(r) as m (m.href)}
					<a class="btn btn-sm" href={m.href} target={m.external ? '_blank' : undefined} rel={m.external ? 'noopener noreferrer' : undefined}><Icon name={m.external ? 'globe' : 'terminal'} size={13} />{m.label}</a>
				{/each}
			</div>
		{/if}
		{#each r.changes as ch (ch.id)}{@render changeCard(r, ch)}{/each}
		{#each pending(r) as s (s.id)}{@render approvalCard(r, s)}{/each}
		{#if r.citations.length}
			<ol class="space-y-0.5 text-small">
				{#each r.citations as c}<li class="truncate"><a class="link" href={c.URL ?? c.url} target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer">{c.Title ?? c.title ?? c.URL ?? c.url}</a></li>{/each}
			</ol>
		{/if}
		{#if r.streaming}
			<div><SafeMarkdown source={r.streaming} /><span class="ml-0.5 inline-block size-1.5 animate-pulse rounded-full bg-action"></span></div>
		{/if}
		{#if r.status === 'failed' || r.status === 'interrupted'}
			<Notice tone="fail" live>{r.error_message || 'The run stopped before it finished.'}</Notice>
		{:else if r.status === 'cancelled'}
			<p class="text-small text-muted">Stopped.</p>
		{/if}
	</div>
{/snippet}

{#if session.user && session.features.ai}
	<!-- Opened from the top bar's Ask AI button or Ctrl+. (no floating launcher). -->
	{#if chat.open}
		<div class="fixed inset-0 z-40 bg-black/40 sm:hidden" role="presentation" onclick={() => chat.hide()}></div>
		<div
			class="fixed inset-x-0 bottom-0 z-40 flex h-[calc(100dvh-3rem)] flex-col outline-none overflow-hidden rounded-t-card border border-rule bg-panel shadow-overlay sm:inset-x-auto sm:right-5 sm:bottom-5 sm:h-[min(42rem,calc(100dvh-2.5rem))] sm:w-[26rem] sm:rounded-card {chat.wide ? 'sm:h-[calc(100dvh-2.5rem)] sm:w-[min(46rem,calc(100vw-2.5rem))]' : ''}"
			role="dialog"
			tabindex="-1"
			aria-label="AI assistant"
			onkeydown={(e) => e.key === 'Escape' && !e.defaultPrevented && chat.hide()}
		>
			<header class="flex items-center gap-2 border-b border-rule-soft px-3 py-2.5">
				{#if screen !== 'chat'}
					<button class="btn btn-quiet btn-icon btn-sm" onclick={() => (screen = 'chat')} aria-label="Back to the chat"><Icon name="chevronLeft" size={16} /></button>
					<h2 class="min-w-0 flex-1 truncate font-semibold">{screen === 'history' ? 'Chats' : 'Model and mode'}</h2>
				{:else}
					<span class="grid size-8 shrink-0 place-items-center rounded-control bg-paper-2 text-muted"><Icon name="sparkle" size={16} /></span>
					<div class="min-w-0 flex-1">
						<h2 class="truncate leading-tight font-semibold">{chat.selected && chat.messages.length ? chat.selected.title : 'Ask AI'}</h2>
						<p class="truncate text-[.72rem] text-muted">{chat.provider ? `${chat.provider.name} · ${chat.selected?.model ?? chat.provider.default_model}` : 'RivetPanel assistant'}</p>
					</div>
					<button class="btn btn-quiet btn-icon btn-sm" onclick={() => (screen = 'history')} aria-label="Previous chats" title="Previous chats"><Icon name="history" size={16} /></button>
					<button class="btn btn-quiet btn-icon btn-sm" onclick={() => (screen = 'settings')} aria-label="Model and mode" title="Model and mode"><Icon name="sliders" size={16} /></button>
					<button class="btn btn-quiet btn-icon btn-sm" onclick={() => { chat.newChat(); screen = 'chat'; }} aria-label="New chat" title="New chat"><Icon name="plus" size={16} /></button>
					<button class="btn btn-quiet btn-icon btn-sm hidden sm:inline-flex" onclick={() => (chat.wide = !chat.wide)} aria-label={chat.wide ? 'Smaller window' : 'Larger window'} title={chat.wide ? 'Smaller window' : 'Larger window'}><Icon name={chat.wide ? 'minimize' : 'maximize'} size={15} /></button>
				{/if}
				<button class="btn btn-quiet btn-icon btn-sm" onclick={() => chat.hide()} aria-label="Close" title="Close"><Icon name="x" size={16} /></button>
			</header>

			{#if screen === 'history'}
				<div class="min-h-0 flex-1 overflow-y-auto p-2">
					{#each chat.conversations as c (c.id)}
						<div class="group flex items-center gap-1 rounded-control {chat.selected?.id === c.id ? 'bg-paper-2/70' : 'hover:bg-paper-2/50'}">
							<button class="min-w-0 flex-1 px-2.5 py-2 text-left" onclick={async () => { await chat.openConversation(c); screen = 'chat'; }}>
								<span class="block truncate text-small font-medium">{c.title}</span>
								<span class="text-[.72rem] text-muted">{when(c.updated_at_ms)}</span>
							</button>
							<button class="btn btn-quiet btn-icon btn-sm text-muted opacity-0 group-focus-within:opacity-100 group-hover:opacity-100" onclick={() => chat.removeConversation(c)} aria-label="Delete {c.title}"><Icon name="trash" size={14} /></button>
						</div>
					{:else}
						<p class="p-4 text-center text-small text-muted">No chats yet. They are kept privately for 90 days.</p>
					{/each}
				</div>
			{:else if screen === 'settings'}
				<div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
					<fieldset>
						<legend class="label">When it wants to change something</legend>
						<div class="mt-1 grid gap-2">
							<label class="flex cursor-pointer items-start gap-2.5 rounded-tile border border-rule-soft p-2.5 {chat.mode === 'approval' ? 'border-action/50 bg-action/5' : ''}">
								<input type="radio" name="mode" value="approval" bind:group={chat.mode} class="mt-0.5" />
								<span><strong class="block text-small">Ask me first</strong><span class="text-small text-muted">It looks around freely and stops for your yes before it deploys, changes a setting, a variable or a file, or restarts something.</span></span>
							</label>
							<label class="flex cursor-pointer items-start gap-2.5 rounded-tile border border-rule-soft p-2.5 {chat.mode === 'auto' ? 'border-action/50 bg-action/5' : ''}">
								<input type="radio" name="mode" value="auto" bind:group={chat.mode} class="mt-0.5" />
								<span><strong class="block text-small">Autopilot</strong><span class="text-small text-muted">It does what you ask from start to finish: deploys, sets variables, changes settings, fixes and restarts, without stopping to ask. It acts with your permissions only.</span></span>
							</label>
						</div>
					</fieldset>
					{#if chat.providers.length}
						<div class="grid gap-3">
							<label class="block"><span class="label">Provider</span>
								<select class="field" value={chat.selected?.provider_id ?? chat.provider?.id ?? ''} disabled={!chat.selected || chat.active} onchange={(e) => chat.selectProvider(e.currentTarget.value)}>
									{#each chat.providers as p}<option value={p.id}>{p.name}</option>{/each}
								</select>
							</label>
							<label class="block"><span class="label">Model</span>
								<input class="field font-mono text-small" value={chat.selected?.model ?? chat.provider?.default_model ?? ''} disabled={!chat.selected || chat.active} onchange={(e) => chat.saveModel(e.currentTarget.value)} />
							</label>
							{#if !chat.selected}<p class="text-small text-muted">Send a message first to change the model for that chat.</p>{/if}
						</div>
					{/if}
				</div>
			{:else}
				<div class="min-h-0 flex-1 overflow-y-auto px-3.5 py-3" bind:this={scroller} aria-live="polite" onscroll={() => { if (scroller) pinned = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80; }}>
					{#if noProvider}
						<div class="py-6">
							<Notice tone="warn" title="The assistant is not set up yet">
								{#if isAdmin}Add an AI provider under <a href="/admin/settings">Administration → Panel settings</a> to turn it on.{:else}Ask an administrator to add an AI provider to this panel.{/if}
							</Notice>
						</div>
					{:else if chat.messages.length === 0 && chat.runs.length === 0}
						<div class="flex h-full flex-col justify-center py-4">
							<span class="grid size-11 place-items-center rounded-tile bg-paper-2 text-muted"><Icon name="sparkle" size={22} /></span>
							<h3 class="mt-3 text-section font-semibold">How can I help?</h3>
							<p class="mt-1 text-small text-muted">
								{#if view.kind !== 'page' && attach}I can see you are on <strong class="text-ink">{view.label}</strong>{view.section ? ` (${view.section.replace(/[-_]/g, ' ')})` : ''}. Ask me to fix it, change its settings or variables, or anything else in the panel.{:else}Paste a GitHub link or a Docker image and I will deploy it, set its variables and check that it runs. I can also fix apps, change settings and set up the panel for you.{/if}
							</p>
							<div class="mt-4 grid gap-1.5">
								{#each suggestions as s}
									<button class="rounded-tile border border-rule-soft bg-raised px-3 py-2 text-left text-small hover:border-action/50 hover:bg-action/5" onclick={() => pick(s)}>{s}</button>
								{/each}
							</div>
						</div>
					{:else}
						<div class="space-y-3.5">
							{#each timeline as it (it.t === 'msg' ? it.m.id : it.r.id)}
								{#if it.t === 'msg'}
									{#if it.m.role === 'user'}
										<div class="flex flex-col items-end gap-1">
											<p class="max-w-[88%] rounded-tile rounded-br-sm bg-paper-2 px-3 py-2 text-body whitespace-pre-wrap text-ink">{it.m.content}</p>
											{#if it.m.context?.label || it.m.context?.kind === 'page'}
												<span class="inline-flex max-w-[88%] items-center gap-1 truncate text-[.7rem] text-muted" title={it.m.context?.detail ?? it.m.context?.path}>
													<Icon name={it.m.context?.kind === 'bot' ? 'terminal' : it.m.context?.kind === 'site' ? 'globe' : 'file'} size={11} />{describeView({ kind: 'page', label: it.m.context?.label, section: it.m.context?.section, path: it.m.context?.path ?? '' })}
												</span>
											{/if}
										</div>
									{:else}
										<div class="text-body"><SafeMarkdown source={it.m.content} /></div>
									{/if}
								{:else}
									{@render runBlock(it.r)}
								{/if}
							{/each}
						</div>
					{/if}
				</div>

				{#if chat.error}<div class="px-3 pb-2"><Notice tone="fail" live>{chat.error}</Notice></div>{/if}
				<form class="border-t border-rule-soft bg-panel p-2.5" onsubmit={(e) => { e.preventDefault(); void chat.send(); }}>
					<div class="mb-1.5 flex flex-wrap items-center gap-1.5">
						{#if attach}
							<span class="inline-flex max-w-full items-center gap-1.5 rounded-pill border border-rule-soft bg-paper-2/50 py-0.5 pr-1 pl-2 text-[.72rem]" title="This page is shared with the assistant: {view.path}">
								<Icon name={view.kind === 'bot' ? 'terminal' : view.kind === 'site' ? 'globe' : 'file'} size={11} class="text-action" />
								<span class="min-w-0 truncate">{describeView(view)}{view.detail ? ` · ${view.detail}` : ''}</span>
								<button type="button" class="grid size-4 place-items-center rounded-pill text-muted hover:bg-paper-2 hover:text-ink" onclick={() => (attach = false)} aria-label="Do not share this page with the assistant"><Icon name="x" size={10} /></button>
							</span>
						{:else}
							<button type="button" class="inline-flex items-center gap-1 rounded-pill border border-dashed border-rule px-2 py-0.5 text-[.72rem] text-muted hover:text-ink" onclick={() => (attach = true)}><Icon name="plus" size={10} />Share “{describeView(view)}”</button>
						{/if}
						<div class="ml-auto inline-flex rounded-control border border-rule-soft text-[.72rem]" role="group" aria-label="Mode">
							<button type="button" class="rounded-l-control px-2 py-0.5 {chat.mode === 'approval' ? 'bg-paper-2 font-medium' : 'text-muted hover:text-ink'}" aria-pressed={chat.mode === 'approval'} onclick={() => (chat.mode = 'approval')} title="Stops for your yes before it changes anything">Ask first</button>
							<button type="button" class="inline-flex items-center gap-1 rounded-r-control px-2 py-0.5 {autopilot ? 'bg-action font-medium text-action-ink' : 'text-muted hover:text-ink'}" aria-pressed={autopilot} onclick={() => (chat.mode = 'auto')} title="Does the whole job without stopping to ask"><Icon name="sparkle" size={10} />Autopilot</button>
						</div>
					</div>
					<div class="flex items-end gap-2">
						<label class="sr-only" for="ai-chat-input">Message the assistant</label>
						<textarea id="ai-chat-input" bind:this={box} class="field max-h-40 min-h-[2.5rem] flex-1 resize-none py-2 [field-sizing:content]" rows="1" bind:value={chat.prompt} onkeydown={onKeydown} placeholder={noProvider ? 'The assistant is not set up' : hasTarget ? `Ask about ${view.label}, or anything else…` : 'Deploy, fix or change anything…'} disabled={noProvider}></textarea>
						{#if chat.active}
							<button type="button" class="btn btn-icon" onclick={() => chat.cancel()} aria-label="Stop" title="Stop"><Icon name="stop" size={12} /></button>
						{:else}
							<button class="btn btn-primary btn-icon" disabled={chat.busy || !chat.prompt.trim() || noProvider} aria-label="Send" title="Send (Enter)"><Icon name="send" size={15} /></button>
						{/if}
					</div>
</form>
			{/if}
		</div>
	{/if}
{/if}
