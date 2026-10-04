<script lang="ts">
	import { onMount } from 'svelte';
	import { fmtBytes } from '$lib/api/client';
	import type { Bot, Gauge } from '$lib/api/types';
	import type { PowerAction } from '$lib/api/bots';
	import { describe, mayBeLive } from '$lib/status';
	import { chat } from '$lib/ai/chat.svelte';
	import { session } from '$lib/session.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// State, live usage and power controls in one strip above the console.
	// One SSE stream per open bot page; the fleet never opens these.
	let { bot, now, showStats, canPower, acting, onAct }: { bot: Bot; now: number; showStats: boolean; canPower: boolean; acting: boolean; onAct: (a: PowerAction) => void } = $props();

	let g = $state<Gauge | null>(null);
	let link = $state<'connecting' | 'live' | 'lost'>('connecting');
	onMount(() => {
		if (!showStats) return;
		const es = new EventSource(`/api/v1/bots/${bot.id}/stats/stream`);
		es.addEventListener('stats', (e) => {
			g = JSON.parse((e as MessageEvent).data);
			link = 'live';
		});
		es.onerror = () => (link = es.readyState === EventSource.CLOSED ? 'lost' : 'connecting');
		return () => es.close();
	});

	const d = $derived(describe(bot, now));
	const pct = (a: number, b: number) => (b > 0 ? Math.min(100, (a / b) * 100) : 0);
	const tone = (p: number) => (p >= 90 ? 'bg-fail' : p >= 75 ? 'bg-warn' : 'bg-run');
	const live = $derived(!!g?.running);
	const meters = $derived([
		{ k: 'CPU', p: live ? Math.min(100, g!.cpu_percent) : 0, v: live ? `${g!.cpu_percent.toFixed(1)}%` : '0.0%' },
		{ k: 'MEM', p: live ? pct(g!.mem_used_bytes, g!.mem_limit_bytes) : 0, v: live ? fmtBytes(g!.mem_used_bytes) : '0 B' },
		g?.disk_scope === 'node'
			? { k: 'NODE DISK', p: pct(g.disk_total_bytes - g.disk_free_bytes, g.disk_total_bytes), v: g.disk_total_bytes > 0 ? `${fmtBytes(g.disk_free_bytes)} free` : '–' }
			: { k: 'DISK', p: g ? pct(g.disk_used_bytes, g.disk_used_bytes + g.disk_free_bytes) : 0, v: g ? fmtBytes(g.disk_used_bytes) : '–' }
	]);

	// Which controls make sense now. A bot that gave up or exited cleanly is
	// wanted running but idle: the useful action is to start it again.
	const wanted = $derived(bot.desired_state === 'running');
	const idle = $derived(wanted && (bot.phase === 'failed' || bot.phase === 'exited'));
	const can = $derived({
		start: !wanted || idle,
		restart: wanted && !idle,
		stop: wanted,
		kill: mayBeLive(bot)
	});
	const dot = $derived({ run: 'bg-run', warn: 'bg-warn', fail: 'bg-fail', idle: 'bg-muted' }[d.tone]);
	// Disabled buttons drop their color so only the actions that apply stand out.
	const btn = 'btn justify-center gap-1.5 border px-2 sm:px-3 disabled:border-rule-soft disabled:bg-transparent disabled:text-muted disabled:opacity-60';
</script>

<div class="rounded-tile border border-rule-soft bg-panel px-4 py-3" data-tone={d.tone}>
	<div class="flex flex-wrap items-center justify-between gap-3">
		<span class="inline-flex items-center gap-2 rounded-pill bg-paper-2/70 px-3 py-1.5 text-small font-medium" title={d.detail || undefined}>
			<span class="size-2 rounded-pill {dot} {d.busy ? 'animate-pulse' : ''}" aria-hidden="true"></span>{d.label}
		</span>
		{#if canPower}
			<div class="grid w-full grid-cols-4 gap-2 sm:flex sm:w-auto" role="group" aria-label="Power controls">
				<button class="{btn} border-run/45 bg-run/12 text-run hover:bg-run/20" disabled={acting || !can.start} onclick={() => onAct('start')}><Icon name="play" size={11} />Start</button>
				<button class="{btn} border-warn/45 bg-warn/12 text-warn hover:bg-warn/20" disabled={acting || !can.restart} onclick={() => onAct('restart')}><Icon name="restart" size={13} />Restart</button>
				<button class="{btn} border-fail/40 bg-fail/10 text-fail hover:bg-fail/18" disabled={acting || !can.stop} onclick={() => onAct('stop')}><Icon name="stop" size={11} />Stop</button>
				<button class="{btn} border-fail/50 bg-fail/16 text-fail hover:bg-fail/26" disabled={acting || !can.kill} onclick={() => onAct('kill')} title="Ends the process immediately"><Icon name="bolt" size={13} />Kill</button>
			</div>
		{/if}
	</div>

	{#if showStats}
		<div class="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 border-t border-rule-soft pt-3 font-mono text-[.78rem] sm:grid-cols-3 lg:grid-cols-5" role="group" aria-label="Resource usage">
			{#each meters as m (m.k)}
				<div class="min-w-0" title="{m.k}: {m.v}">
					<div class="flex items-baseline justify-between gap-2"><span class="tracking-[0.12em] text-muted">{m.k}</span><span class="truncate tabular-nums">{m.v}</span></div>
					<span class="mt-1.5 block h-1 overflow-hidden rounded-pill bg-paper-2" role="meter" aria-label="{m.k} usage" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(m.p)} aria-valuetext={m.v}>
						<span class="block h-full {tone(m.p)}" style="width: {Math.max(m.p, m.p > 0 ? 3 : 0)}%"></span>
					</span>
				</div>
			{/each}
			<div class="min-w-0" title="Network received since the container started">
				<div class="flex items-baseline justify-between gap-2"><span class="tracking-[0.12em] text-muted">NET IN</span><span class="inline-flex items-center gap-1 tabular-nums"><Icon name="download" size={12} class="text-run" />{g ? fmtBytes(g.net_rx_bytes) : '0 B'}</span></div>
			</div>
			<div class="min-w-0" title="Network sent since the container started">
				<div class="flex items-baseline justify-between gap-2"><span class="tracking-[0.12em] text-muted">NET OUT</span><span class="inline-flex items-center gap-1 tabular-nums"><Icon name="upload" size={12} class="text-warn" />{g ? fmtBytes(g.net_tx_bytes) : '0 B'}</span></div>
			</div>
			{#if link === 'lost'}<span class="col-span-full text-warn">Live usage unavailable</span>{/if}
		</div>
	{/if}

	{#if d.detail}
		<div class="mt-2.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-rule-soft pt-2.5">
			<p class="min-w-0 flex-1 basis-72 text-small {d.tone === 'fail' ? 'text-fail' : 'text-muted'}" aria-live="polite">{d.detail}</p>
			{#if d.tone === 'fail' && session.features.ai}
				<button class="btn btn-sm gap-1.5 border-action/45 text-action" onclick={() => chat.ask('My bot is not running. Check its status, console output and build output, tell me what is wrong and how to fix it.', true)}><Icon name="sparkle" size={13} />Ask AI why</button>
			{/if}
		</div>
	{/if}
</div>
