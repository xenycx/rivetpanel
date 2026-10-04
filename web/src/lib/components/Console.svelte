<script lang="ts">
	import { theme as uiTheme } from '$lib/ui/theme.svelte';
	// The terminal follows the page's --color-term tokens (dark in both themes).
	function termTheme() {
		const v = (n: string) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();
		return { background: v('--color-term'), foreground: v('--color-term-ink'), selectionBackground: v('--color-action') + '55', cursor: v('--color-action') };
	}
	import { onMount } from 'svelte';
	import { can, Perm, type Bot } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// fill: on wide screens the console takes the rest of the viewport below
	// it, so the whole terminal is visible without scrolling the page.
	let { bot, fill = false }: { bot: Bot; fill?: boolean } = $props();
	let frame: HTMLDivElement | undefined = $state();
	let fillHeight = $state(0);
	$effect(() => {
		if (!fill || !frame) return;
		const size = () => {
			if (!frame) return;
			if (window.innerWidth < 1024) return void (fillHeight = 0);
			const top = frame.getBoundingClientRect().top + window.scrollY;
			fillHeight = Math.max(320, Math.floor(window.innerHeight - top - 24));
		};
		size();
		const ro = new ResizeObserver(size);
		const main = frame.closest('main');
		if (main) ro.observe(main);
		window.addEventListener('resize', size);
		return () => {
			ro.disconnect();
			window.removeEventListener('resize', size);
		};
	});
	// Input needs the power permission (the server re-checks it on every line).
	const canInput = $derived(can(bot, Perm.power));
	const noun = $derived(bot.kind === 'game' ? 'server' : 'bot');

	let host: HTMLDivElement | undefined = $state();
	let link = $state<'connecting' | 'live' | 'reconnecting' | 'ended'>('connecting');
	let notice = $state('');
	let offlineNotice = false; // notice is the "node offline" message
	let line = $state('');
	let paused = $state(false);
	let dropped = $state(0);
	let lines = $state(0);
	let copied = $state(false);
	let ws: WebSocket | null = null;

	// Not reactive: internal handles set up once xterm has loaded.
	let sendInput: (text: string) => boolean = () => false;
	let reconnectNow: () => void = () => {};
	let clearView: () => void = () => {};
	let copyAll: () => Promise<void> = async () => {};
	let downloadAll: () => void = () => {};
	let setPaused: (p: boolean) => void = () => {};
	let retheme: () => void = () => {};
	$effect(() => {
		void uiTheme.dark;
		retheme();
	});

	onMount(() => {
		if (!session.features.console) return;
		let term: import('@xterm/xterm').Terminal | undefined;
		let observer: ResizeObserver | undefined;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let stopped = false;
		let attempt = 0;
		let lastTs = '';
		// While paused, output is held (bounded) and written on resume, so the
		// reader's scroll position is not disturbed.
		let held: string[] = [];
		let heldBytes = 0;
		let isPaused = false;
		// A plain-text copy of what was received, bounded, for copy/download.
		let transcript: string[] = [];
		let transcriptBytes = 0;

		const keep = (s: string) => {
			transcript.push(s);
			transcriptBytes += s.length;
			while (transcriptBytes > 1_000_000 && transcript.length) transcriptBytes -= transcript.shift()!.length;
		};
		const write = (s: string) => {
			if (!isPaused) return term?.write(s);
			held.push(s);
			heldBytes += s.length;
			while (heldBytes > 512_000 && held.length) heldBytes -= held.shift()!.length;
		};

		function connect() {
			const proto = location.protocol === 'https:' ? 'wss' : 'ws';
			const q = (lastTs ? `since=${encodeURIComponent(lastTs)}` : 'tail=300') + (canInput ? '' : '&stdin=0');
			const sock = new WebSocket(`${proto}://${location.host}/api/v1/bots/${bot.id}/console?${q}`);
			ws = sock;
			sock.onopen = () => {
				attempt = 0;
				link = 'live';
				notice = '';
			};
			sock.onmessage = (ev) => {
				let m: any;
				try {
					m = JSON.parse(ev.data);
				} catch {
					return;
				}
				switch (m.type) {
					case 'log':
						if (m.ts && m.ts > lastTs) lastTs = m.ts;
						keep(m.data);
						lines += (m.data.match(/\n/g) ?? []).length || 1;
						write(m.stream === 'stderr' ? `\x1b[38;5;217m${m.data}\x1b[0m` : m.data);
						break;
					case 'dropped':
						dropped += m.count;
						write(`\r\n\x1b[2m[${m.count} lines skipped: output was faster than this connection]\x1b[0m\r\n`);
						break;
					case 'error':
						notice = m.message ?? 'The console reported an error.';
						offlineNotice = m.code === 'node_offline';
						break;
					case 'node':
						// The node's agent is back: output resumes in this session.
						if (m.code === 'online' && offlineNotice) notice = '';
						offlineNotice = false;
						break;
				}
			};
			sock.onclose = (ev) => {
				ws = null;
				if (stopped) return;
				if (ev.code === 1008) {
					link = 'ended';
					notice = 'This console session ended because your access changed or you signed out. Reload the page to reconnect.';
					return;
				}
				link = 'reconnecting';
				const delay = Math.min(1000 * 2 ** attempt++, 15000);
				timer = setTimeout(connect, delay);
			};
		}

		sendInput = (text) => {
			if (ws?.readyState !== WebSocket.OPEN) return false;
			// An earlier refusal (for example "not running" during a restart)
			// must not stay on screen after a later line is sent; a new
			// refusal arrives as its own error message.
			if (link === 'live') notice = '';
			ws.send(JSON.stringify({ type: 'stdin', data: text }));
			return true;
		};
		reconnectNow = () => {
			clearTimeout(timer);
			attempt = 0;
			if (ws) ws.close();
			else connect();
			link = 'connecting';
		};
		clearView = () => {
			term?.clear();
			lines = 0;
		};
		copyAll = async () => {
			await navigator.clipboard.writeText(transcript.join('').replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, ''));
		};
		downloadAll = () => {
			const blob = new Blob([transcript.join('').replace(/\x1b\[[0-9;?]*[ -/]*[@-~]/g, '')], { type: 'text/plain' });
			const a = document.createElement('a');
			a.href = URL.createObjectURL(blob);
			a.download = `${bot.name.replace(/[^\w.-]+/g, '_')}-console.log`;
			a.click();
			setTimeout(() => URL.revokeObjectURL(a.href), 1000);
		};
		setPaused = (p) => {
			isPaused = p;
			if (!p && held.length) {
				term?.write(held.join(''));
				held = [];
				heldBytes = 0;
			}
		};

		(async () => {
			const [{ Terminal }, { FitAddon }] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit')]);
			await import('@xterm/xterm/css/xterm.css');
			if (stopped) return;
			term = new Terminal({
				convertEol: true,
				disableStdin: true, // input goes to the bot's stdin through the field below, not a shell
				scrollback: 5000,
				fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--font-mono'),
				fontSize: 13,
				lineHeight: 1.25,
				theme: termTheme()
			});
			const fit = new FitAddon();
			term.loadAddon(fit);
			if (!host) return;
			term.open(host);
			retheme = () => term && (term.options.theme = termTheme());
			fit.fit();
			observer = new ResizeObserver(() => fit.fit());
			observer.observe(host);
			connect();
		})();

		return () => {
			stopped = true;
			clearTimeout(timer);
			observer?.disconnect();
			ws?.close();
			term?.dispose();
		};
	});

	function send(e: SubmitEvent) {
		e.preventDefault();
		if (line === '') return;
		if (sendInput(line + '\n')) line = '';
	}
	function togglePause() {
		paused = !paused;
		setPaused(paused);
	}
	async function copy() {
		try {
			await copyAll();
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			notice = 'Copying needs clipboard access; use Download instead.';
		}
	}

	const linkText = $derived({ connecting: 'Connecting', live: 'Live', reconnecting: 'Reconnecting', ended: 'Disconnected' }[link]);
	const linkColor = $derived({ connecting: 'text-warn', live: 'text-run', reconnecting: 'text-warn', ended: 'text-fail' }[link]);
</script>

{#if !session.features.console}
	<EmptyState title="Live output needs the Docker runner">
		<p>This panel runs without Docker (RIVET_RUNNER_MODE=none), so bots cannot run here and there is no output to show.</p>
	</EmptyState>
{:else}
	<!-- A terminal window: title bar with the stream's state and tools, the
	     output, and the line that goes to the bot's standard input. -->
	<div bind:this={frame} class="flex flex-col overflow-hidden rounded-tile border border-rule-soft bg-term text-term-ink" style={fillHeight ? `height: ${fillHeight}px` : undefined}>
		<div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-white/8 bg-black/25 px-3.5 py-2">
			<span class="eyebrow !text-term-ink/65">Console</span>
			<span class="inline-flex items-center gap-1.5 text-[.72rem] font-medium {linkColor}" aria-live="polite">
				<span class="size-1.5 rounded-pill bg-current {link === 'live' ? '' : 'animate-pulse'}" aria-hidden="true"></span>{linkText}{#if paused}<span class="text-term-ink/60">, paused</span>{/if}
			</span>
			{#if dropped}<span class="text-[.72rem] text-term-ink/60">{dropped} lines skipped</span>{/if}
			<span class="flex-1"></span>
			<span class="font-mono text-[.78rem] text-term-ink/50">{lines.toLocaleString()} {lines === 1 ? 'line' : 'lines'}</span>
			{#if link !== 'live'}<button class="btn btn-sm !border-white/15 !bg-white/8 !text-term-ink hover:!bg-white/14" onclick={() => reconnectNow()}>Reconnect</button>{/if}
			<div class="flex items-center">
				<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" aria-pressed={paused} onclick={togglePause} title={paused ? 'Resume output' : 'Hold new output so you can read'} aria-label={paused ? 'Resume output' : 'Pause output'}><Icon name={paused ? 'play' : 'pause'} size={13} /></button>
				<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" onclick={copy} title="Copy output" aria-label="Copy output"><Icon name={copied ? 'check' : 'copy'} size={13} /></button>
				<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" onclick={() => downloadAll()} title="Download output" aria-label="Download output"><Icon name="download" size={13} /></button>
				<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" onclick={() => clearView()} title="Clear this view (the {noun} is not affected)" aria-label="Clear view"><Icon name="trash" size={13} /></button>
			</div>
		</div>
		<div bind:this={host} class="{fillHeight ? 'min-h-0 flex-1' : 'h-[clamp(18rem,calc(100dvh-27rem),34rem)]'} overflow-hidden p-2" aria-label="{bot.kind === 'game' ? 'Server' : 'Bot'} output" role="log"></div>
		{#if canInput}
			<form class="flex items-center gap-2 border-t border-white/8 bg-black/20 px-3.5 py-1.5" onsubmit={send}>
				<label class="sr-only" for="stdin">{bot.kind === 'game' ? 'Send a console command' : 'Send a line to the bot'}</label>
				<span class="font-mono text-term-ink/50 select-none" aria-hidden="true">›</span>
				<input id="stdin" aria-describedby="stdin-help" class="min-h-9 min-w-0 flex-1 bg-transparent font-mono text-[.8125rem] text-term-ink outline-none placeholder:text-term-ink/40" placeholder={bot.kind === 'game' ? 'Type a command, for example: say Hello' : 'Send a line to the bot’s standard input'} autocomplete="off" spellcheck="false" bind:value={line} disabled={link !== 'live'} />
				<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" disabled={link !== 'live' || line === ''} aria-label="Send" title="Send"><Icon name="send" size={14} /></button>
			</form>
		{/if}
	</div>
	{#if notice}<p class="mt-2 text-small text-warn" role="status">{notice}</p>{/if}
	<p id="stdin-help" class="{fillHeight ? 'sr-only' : 'mt-2 text-small text-muted'}">
		{#if canInput}{bot.kind === 'game' ? 'Commands go to the server console, not to a shell (no leading slash needed).' : 'Input goes to the bot process, not to a shell.'} One console at a time can send input.{:else}You can watch the output. Sending input needs the start and stop permission.{/if}
		Closing this page does not stop the {noun}.
	</p>
{/if}
