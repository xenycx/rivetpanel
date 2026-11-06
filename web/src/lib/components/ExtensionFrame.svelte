<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { theme } from '$lib/ui/theme.svelte';

	// A custom extension page. It runs in a sandboxed frame with an opaque
	// origin: it has no cookies, no CSRF token and no network of its own, and
	// cannot read the panel's pages. It asks the panel for things through
	// postMessage, and the panel only ever calls that extension's own routes
	// (on this bot, inside a bot tab) as the signed-in person.
	let { ext, page, botId = '', title, height = 'max(32rem, calc(100dvh - 16rem))' }: { ext: string; page: string; botId?: string; title: string; height?: string } = $props();

	let frame: HTMLIFrameElement | undefined = $state();
	let frameHeight = $state('');
	let ready = $state(false);
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
		if (!frame || e.source !== frame.contentWindow) return;
		const m = e.data;
		if (!m || typeof m !== 'object' || m.rivet !== 1) return;
		if (m.type === 'ready') {
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
					const p = await api<{ bot_id: string; kind: string; query: string }>('GET', `/extensions/confirmations/${m.token}`);
					if (botId && p.bot_id !== botId) return reply(false, { error: { message: 'That confirmation belongs to another app.' } });
					const ok = await confirmDialog({
						title: 'Run this change?',
						body: 'It runs without the read-only guard, as you, against this app’s database. Changes cannot be undone from here; take a backup first if in doubt.',
						details: [
							['Database', p.kind],
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
			case 'resize':
				if (typeof m.height === 'number' && m.height >= 100 && m.height <= 4000) frameHeight = `${Math.round(m.height)}px`;
				return;
		}
	}
</script>

<svelte:window onmessage={onMessage} />

<iframe
	bind:this={frame}
	{src}
	{title}
	sandbox="allow-scripts"
	allow=""
	referrerpolicy="no-referrer"
	loading="lazy"
	class="block w-full rounded-tile border border-rule-soft bg-panel"
	style:height={frameHeight || height}
></iframe>
