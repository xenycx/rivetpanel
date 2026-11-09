<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { theme } from '$lib/ui/theme.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// A custom extension page. It runs in a sandboxed frame with an opaque
	// origin: it has no cookies, no CSRF token and no network of its own, and
	// cannot read the panel's pages. It asks the panel for things through
	// postMessage, and the panel only ever calls that extension's own routes
	// (on this bot, inside a bot tab) as the signed-in person.
	//
	// The bridge serves exactly one document: the signed page the panel
	// loaded. It answers the first "ready" only, and if the frame ever loads
	// another document (the page navigated itself somewhere), the frame is
	// removed and nothing it sends is answered again, so code from anywhere
	// else can never drive the extension's routes.
	let { ext, page, botId = '', title, height = 'max(32rem, calc(100dvh - 16rem))' }: { ext: string; page: string; botId?: string; title: string; height?: string } = $props();

	let frame: HTMLIFrameElement | undefined = $state();
	let frameHeight = $state('');
	// Expanded: the page fills the panel's window (not the browser's full
	// screen). The panel always draws its own bar above it, naming the
	// extension and offering the way out, so a page can never pass itself off
	// as the panel. The frame is restyled in place: moving an iframe in the
	// document would reload it.
	let expanded = $state(false);
	function setExpanded(on: boolean) {
		if (expanded === on) return;
		expanded = on;
		if (ready) post({ type: 'expanded', on });
	}
	$effect(() => {
		if (!expanded) return;
		const prev = document.documentElement.style.overflow;
		document.documentElement.style.overflow = 'hidden';
		return () => {
			document.documentElement.style.overflow = prev;
		};
	});
	let ready = $state(false);
	let loads = 0;
	let closed = $state(false);
	function onLoad() {
		loads += 1;
		if (loads > 1) {
			closed = true;
			ready = false;
		}
	}
	const nonce = crypto.getRandomValues(new Uint32Array(4)).join('-');
	const src = $derived(`/api/v1/ext-ui/${encodeURIComponent(ext)}/${encodeURIComponent(page)}`);

	const THEME_VARS: Record<string, string> = {
		'--paper': '--color-paper', '--paper-2': '--color-paper-2', '--panel': '--color-panel', '--ink': '--color-ink', '--muted': '--color-muted',
		'--rule': '--color-rule', '--rule-soft': '--color-rule-soft', '--action': '--color-action', '--action-ink': '--color-action-ink',
		'--run': '--color-run', '--warn': '--color-warn', '--fail': '--color-fail', '--term': '--color-term', '--term-ink': '--color-term-ink'
	};
	function themeVars(): Record<string, string> {
		const cs = getComputedStyle(document.documentElement);
		const out: Record<string, string> = {};
		for (const [to, from] of Object.entries(THEME_VARS)) {
			const v = cs.getPropertyValue(from).trim();
			if (v) out[to] = v;
		}
		return out;
	}
	function post(msg: Record<string, unknown>) {
		// An opaque-origin frame can only be addressed with "*"; the message
		// goes to that one window, never to anything else.
		frame?.contentWindow?.postMessage({ rivet: 1, ...msg }, '*');
	}
	// Re-theme when the panel switches between light and dark.
	$effect(() => {
		void theme.dark;
		void theme.accent;
		if (ready) post({ type: 'theme', theme: themeVars() });
	});

	const pathOK = (p: unknown): p is string => typeof p === 'string' && /^\/[a-z0-9_\-/]{0,100}$/.test(p) && !p.includes('//');
	const errBody = (e: unknown) => ({ error: { message: e instanceof ApiError ? e.message : 'The request failed.' } });

	async function onMessage(e: MessageEvent) {
		if (!frame || closed || e.source !== frame.contentWindow) return;
		const m = e.data;
		if (!m || typeof m !== 'object' || m.rivet !== 1) return;
		if (m.type === 'ready') {
			if (ready) return;
			ready = true;
			post({ type: 'init', nonce, theme: themeVars(), context: { bot_id: botId || null } });
			return;
		}
		if (m.nonce !== nonce) return;
		const reply = (ok: boolean, body: unknown) => post({ type: 'reply', id: m.id, ok, body });
		switch (m.type) {
			case 'rpc': {
				const method = String(m.method || 'GET').toUpperCase();
				if (!['GET', 'POST', 'PUT', 'DELETE'].includes(method) || !pathOK(m.path)) return reply(false, { error: { message: 'Not an allowed request.' } });
				const base = botId ? `/bots/${botId}/ext/${encodeURIComponent(ext)}` : `/ext/${encodeURIComponent(ext)}`;
				try {
					reply(true, await api(method, base + m.path, method === 'GET' ? undefined : (m.body ?? {})));
				} catch (err) {
					reply(false, errBody(err));
				}
				return;
			}
			case 'confirm-write': {
				if (typeof m.token !== 'string' || !/^[0-9a-f]{32}$/.test(m.token)) return reply(false, { error: { message: 'Invalid confirmation.' } });
				try {
					const p = await api<{ bot_id: string; kind: string; query: string; file?: string }>('GET', `/extensions/confirmations/${m.token}`);
					if (botId && p.bot_id !== botId) return reply(false, { error: { message: 'That confirmation belongs to another app.' } });
					// Name the app the change runs against, also on pages outside a bot.
					const app = await api<{ name: string }>('GET', `/bots/${encodeURIComponent(p.bot_id)}`).then((b) => b.name, () => p.bot_id);
					const ok = await confirmDialog({
						title: 'Run this change?',
						body: 'It runs without the read-only guard, as you, against this app’s database. Changes cannot be undone from here; take a backup first if in doubt.',
						details: [
							['App', app],
							['Database', p.file ? `SQLite file ${p.file}` : p.kind],
							['Statement', p.query]
						],
						confirmLabel: 'Run change',
						tone: 'danger'
					});
					if (!ok) return reply(true, null);
					reply(true, await api('POST', `/extensions/confirmations/${m.token}`));
				} catch (err) {
					reply(false, errBody(err));
				}
				return;
			}
			case 'toast':
				if (typeof m.text === 'string' && m.text.length <= 200) toast(m.text, m.tone === 'fail' ? 'fail' : m.tone === 'success' ? 'success' : 'info');
				return;
			case 'navigate':
				if (typeof m.path === 'string' && m.path.startsWith('/') && !m.path.startsWith('//') && !m.path.startsWith('/api/')) goto(m.path);
				return;
			case 'expand':
				setExpanded(m.on === true);
				return;
			case 'resize':
				if (typeof m.height === 'number' && m.height >= 100 && m.height <= 4000) frameHeight = `${Math.round(m.height)}px`;
				return;
		}
	}
</script>

<svelte:window onmessage={onMessage} onkeydown={(e) => { if (expanded && e.key === 'Escape' && !document.querySelector('dialog[open]')) setExpanded(false); }} />

{#if closed}
	<div class="grid place-items-center rounded-tile border border-rule-soft bg-panel p-8 text-center text-small text-muted" style:min-height="12rem" role="status">
		<p>This page tried to open another address, so the panel closed it.<br />Reload the page to open it again.</p>
	</div>
{:else}
<div class={expanded ? 'fixed inset-0 z-[45] flex flex-col bg-paper' : 'contents'} role={expanded ? 'region' : undefined} aria-label={expanded ? title : undefined}>
	{#if expanded}
		<div class="flex shrink-0 items-center gap-2 border-b border-rule-soft bg-panel px-3 py-1.5 text-small">
			<Icon name="layers" size={13} class="text-muted" />
			<span class="min-w-0 flex-1 truncate text-muted">Extension: <span class="text-ink">{title}</span></span>
			<span class="hidden text-muted sm:inline"><kbd class="rounded-inner border border-rule px-1 font-mono text-[11px]">Esc</kbd></span>
			<button type="button" class="btn btn-sm" onclick={() => setExpanded(false)}><Icon name="minimize" size={13} />Exit full screen</button>
		</div>
	{/if}
	<iframe
		bind:this={frame}
		onload={onLoad}
		{src}
		{title}
		sandbox="allow-scripts"
		allow=""
		referrerpolicy="no-referrer"
		loading="lazy"
		class={expanded ? 'block min-h-0 w-full flex-1 bg-panel' : 'block w-full rounded-tile border border-rule-soft bg-panel'}
		style:height={expanded ? undefined : frameHeight || height}
	></iframe>
</div>
{/if}
