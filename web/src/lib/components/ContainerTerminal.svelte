<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api/client';
	import type { Bot, Stack } from '$lib/api/types';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// An interactive terminal inside a running container app: a shell (or the
	// app's own command, such as Herdr) with the container's user, files and
	// isolation, never the host. xterm.js loads only when this tab opens.
	let { bot }: { bot: Bot } = $props();

	// A one-click app used from a terminal names its command (Herdr: herdr).
	let command = $state<string[]>([]);
	let ready = $state(false);
	onMount(async () => {
		const sid = bot.container?.stack_id;
		if (sid) {
			try {
				const st = await api<Stack>('GET', `/stacks/${sid}`);
				if (st.template?.console_bot_id === bot.id) command = st.template.console?.command ?? [];
			} catch {
				/* a plain shell then */
			}
		}
		ready = true;
	});

	function termTheme() {
		const v = (n: string) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
		return { background: v('--color-term'), foreground: v('--color-term-ink'), selectionBackground: v('--color-action') + '55', cursor: v('--color-action') };
	}

	let host: HTMLDivElement | undefined = $state();
	let link = $state<'idle' | 'connecting' | 'open' | 'ended'>('idle');
	let reason = $state('');
	let run = $state<string[]>([]);
	let session = $state(0);
	const running = $derived(bot.phase === 'running');

	function start(cmd: string[]) {
		run = cmd;
		session++;
	}
	// Starts the app's own command when it has one, a shell otherwise.
	$effect(() => {
		if (ready && running && link === 'idle') start(command.length ? command : ['sh']);
	});

	$effect(() => {
		const id = session;
		if (!id || !host) return;
		let stopped = false;
		let ws: WebSocket | null = null;
		let term: import('@xterm/xterm').Terminal | undefined;
		let observer: ResizeObserver | undefined;
		link = 'connecting';
		reason = '';
		(async () => {
			const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]);
			await import('@xterm/xterm/css/xterm.css');
			if (stopped || !host) return;
			host.replaceChildren();
			term = new Terminal({
				cursorBlink: true,
				scrollback: 5000,
				fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--font-mono'),
				fontSize: 13,
				lineHeight: 1.2,
				theme: termTheme()
			});
			const fit = new FitAddon();
			term.loadAddon(fit);
			term.open(host);
			fit.fit();
			const proto = location.protocol === 'https:' ? 'wss' : 'ws';
			const q = new URLSearchParams({ cols: String(term.cols), rows: String(term.rows), cmd: JSON.stringify(run) });
			ws = new WebSocket(`${proto}://${location.host}/api/v1/bots/${bot.id}/terminal?${q}`);
			ws.binaryType = 'arraybuffer';
			const enc = new TextEncoder();
			ws.onopen = () => {
				link = 'open';
				term?.focus();
			};
			ws.onmessage = (e) => {
				if (e.data instanceof ArrayBuffer) term?.write(new Uint8Array(e.data));
			};
			ws.onclose = (e) => {
				if (stopped) return;
				link = 'ended';
				reason = e.code === 1000 ? 'The terminal ended.' : 'The terminal could not be opened or the connection dropped.';
			};
			term.onData((d) => ws?.readyState === WebSocket.OPEN && ws.send(enc.encode(d)));
			term.onResize(({ cols, rows }) => ws?.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: 'resize', cols, rows })));
			observer = new ResizeObserver(() => fit.fit());
			observer.observe(host);
		})();
		return () => {
			stopped = true;
			observer?.disconnect();
			ws?.close();
			term?.dispose();
		};
	});
</script>

{#if !running && link !== 'open'}
	<EmptyState title="Start the container to open a terminal">The terminal runs inside the running container, with its own user and files.</EmptyState>
{:else}
	<div class="flex flex-wrap items-center gap-2">
		<p class="mr-auto text-small text-muted">
			{#if link === 'open'}<span class="inline-flex items-center gap-1.5"><span class="size-1.5 rounded-full bg-run"></span>Connected: <code class="text-ink">{run.join(' ')}</code> in the container</span>
			{:else if link === 'connecting'}Connecting…
			{:else}{reason}{/if}
		</p>
		{#if command.length}
			<button class="btn btn-sm" onclick={() => start(command)}><Icon name="restart" size={13} />New {command[0]} session</button>
		{/if}
		<button class="btn btn-sm" onclick={() => start(['sh'])}><Icon name="terminal" size={13} />New shell</button>
	</div>
	<div class="mt-2 h-[min(70dvh,36rem)] overflow-hidden rounded-tile bg-term p-2">
		<div class="size-full" bind:this={host}></div>
	</div>
	<p class="mt-2 text-small text-muted">Changes outside the app's volumes are lost when the container restarts; its root filesystem is read-only.</p>
{/if}
