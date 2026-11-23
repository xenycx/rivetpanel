<script lang="ts">
	import { ApiError, fmtBytes } from '$lib/api/client';
	import { eventLabel, eventTone, metrics, type Moment, type Spike } from '$lib/api/metrics';
	import { fmtCores } from '$lib/api/usage';
	import { chat } from '$lib/ai/chat.svelte';
	import { session } from '$lib/session.svelte';
	import { fmtStamp } from '$lib/charts/timeRange.svelte';
	import { categoricalCss } from '$lib/charts/colors';
	import { theme } from '$lib/ui/theme.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import ProcessTable from './ProcessTable.svelte';

	// "What happened here?": host figures, the apps and processes using the
	// CPU, nearby events and the spike, for one moment on a node. A side
	// drawer on wide screens, a full-screen sheet on phones. <dialog> gives
	// the focus trap and Escape.
	let {
		nodeId,
		nodeName,
		t = $bindable(null),
		onzoom,
		colorOf
	}: {
		nodeId: string;
		nodeName: string;
		/** The moment to explain; null closes the drawer. */
		t?: number | null;
		onzoom: (start: number, end: number) => void;
		/** Categorical colour index of an app (same as the charts). */
		colorOf?: (botId: string) => number;
	} = $props();

	let el: HTMLDialogElement | undefined = $state();
	let opener: Element | null = null;
	let data = $state<Moment | null>(null);
	let error = $state('');
	let loading = $state(false);
	let ctrl: AbortController | null = null;

	$effect(() => {
		if (!el) return;
		if (t !== null && !el.open) {
			opener = document.activeElement;
			el.showModal();
			el.querySelector<HTMLElement>('h2')?.focus();
		} else if (t === null && el.open) el.close();
	});

	$effect(() => {
		const at = t;
		const id = nodeId;
		if (at === null) return;
		ctrl?.abort();
		const c = (ctrl = new AbortController());
		loading = true;
		error = '';
		metrics
			.at(id, Math.round(at), undefined, c.signal)
			.then((m) => {
				if (!c.signal.aborted) data = m;
			})
			.catch((e) => {
				if (!c.signal.aborted) error = e instanceof ApiError && e.status !== 404 ? e.message : 'The details of this moment are unavailable.';
			})
			.finally(() => {
				if (ctrl === c) loading = false;
			});
		return () => c.abort();
	});

	function closed() {
		t = null;
		if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
	}

	const spike = $derived<Spike | null>(data?.spike ?? null);
	const when = $derived(t !== null ? new Date(t).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' }) : '');
	const dur = (s: Spike) => {
		const ms = (s.ended_at_ms ?? Date.now()) - s.started_at_ms;
		return ms < 60_000 ? `${Math.round(ms / 1000)} s` : ms < 3600_000 ? `${Math.round(ms / 60_000)} min` : `${(ms / 3600_000).toFixed(1)} h`;
	};

	function zoomSpike() {
		if (!spike) return;
		const end = spike.ended_at_ms ?? Date.now();
		const pad = Math.max(60_000, (end - spike.started_at_ms) * 0.5);
		onzoom(spike.started_at_ms - pad, Math.min(Date.now(), end + pad));
		t = null;
	}
	function zoomHere() {
		if (t === null) return;
		onzoom(t - 5 * 60_000, Math.min(Date.now(), t + 5 * 60_000));
		t = null;
	}
	function ask() {
		if (t === null) return;
		const stamp = new Date(spike?.started_at_ms ?? t).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' });
		const q = spike ? `What caused the CPU spike at ${stamp} on ${nodeName}?` : `What was using the CPU at ${stamp} on ${nodeName}?`;
		t = null; // the modal must close before the chat can take focus
		void chat.ask(q, false);
	}
</script>

<dialog
	bind:this={el}
	class="m-0 ml-auto h-dvh max-h-none w-full max-w-none bg-transparent p-0 text-ink backdrop:bg-ink/10 open:animate-enter sm:w-[30rem]"
	aria-labelledby="moment-t"
	onclose={closed}
	onclick={(e) => e.target === el && (t = null)}
>
	<div class="pb-safe flex h-full flex-col border-l border-rule bg-raised shadow-overlay">
		<header class="flex items-start gap-3 border-b border-rule-soft px-5 pt-4 pb-3">
			<div class="min-w-0 flex-1">
				<p class="eyebrow">What happened here?</p>
				<h2 id="moment-t" tabindex="-1" class="mt-0.5 font-mono text-title font-semibold tabular-nums outline-none">{when}</h2>
				<p class="text-small text-muted">Node {nodeName}</p>
			</div>
			<button class="btn btn-quiet btn-icon btn-sm -mr-2" aria-label="Close" onclick={() => (t = null)}><Icon name="x" /></button>
		</header>

		<div class="min-h-0 flex-1 overflow-y-auto px-5 py-4 {loading && data ? 'opacity-70' : ''}">
			{#if error}<Notice tone="fail">{error}</Notice>{/if}
			{#if !data && loading}
				<p class="text-muted" role="status">Loading…</p>
			{:else if data}
				<dl class="grid grid-cols-3 gap-2">
					<div class="stat"><dt class="eyebrow">CPU</dt><dd class="font-mono text-title font-semibold tabular-nums">{data.host?.cpu != null ? `${data.host.cpu.toFixed(0)}%` : '—'}</dd></div>
					<div class="stat"><dt class="eyebrow">Memory</dt><dd class="font-mono text-title font-semibold tabular-nums">{data.host?.mem != null ? fmtBytes(data.host.mem) : '—'}</dd></div>
					<div class="stat"><dt class="eyebrow">Load</dt><dd class="font-mono text-title font-semibold tabular-nums">{data.host?.load1 != null ? data.host.load1.toFixed(2) : '—'}</dd></div>
				</dl>

				{#if spike}
					<section class="mt-4 rounded-tile border border-warn/40 bg-warn/5 p-3" aria-label="CPU spike">
						<p class="flex items-center gap-2 font-medium"><Icon name="bolt" class="text-warn" />CPU spike{#if spike.ended_at_ms === null}<span class="pill" data-tone="warn">ongoing</span>{/if}</p>
						<p class="mt-1 font-mono text-small tabular-nums">
							{fmtStamp(spike.started_at_ms)} · {dur(spike)} · peak {spike.peak_cpu_pct.toFixed(0)}% · average {spike.avg_cpu_pct.toFixed(0)}%
						</p>
					</section>
				{/if}

				<section class="mt-5" aria-labelledby="m-apps">
					<h3 id="m-apps" class="eyebrow">Apps using the CPU</h3>
					{#if data.bots.length}
						<ul class="mt-2 grid gap-2">
							{#each data.bots.slice(0, 8) as b (b.bot_id)}
								<li>
									<div class="flex items-baseline justify-between gap-3 text-small">
										<a class="link min-w-0 truncate" href="/bots/{b.bot_id}">{b.name}</a>
										<span class="shrink-0 font-mono tabular-nums">{fmtCores(b.cpu_cores)} · {b.share_pct.toFixed(0)}% · {fmtBytes(b.mem_bytes)}</span>
									</div>
									<div class="mt-1 h-1.5 overflow-hidden rounded-pill bg-rule-soft" role="meter" aria-label="Share of CPU used by {b.name}" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(b.share_pct)}>
										<div class="h-full rounded-pill" style="width: {Math.min(100, b.share_pct)}%; background: {colorOf ? categoricalCss(colorOf(b.bot_id), theme.dark) : 'var(--color-data)'}"></div>
									</div>
								</li>
							{/each}
						</ul>
					{:else}
						<p class="mt-2 text-small text-muted">No app measurements near this moment.</p>
					{/if}
				</section>

				<section class="mt-5" aria-labelledby="m-procs">
					<h3 id="m-procs" class="eyebrow">Top processes</h3>
					{#if data.processes}
						<p class="mt-0.5 text-small text-muted">Snapshot {fmtStamp(data.processes.sampled_at_ms)}{data.processes.source === 'docker-top' ? ' · inside app containers only (host processes are not visible to the panel)' : ''}</p>
						<div class="mt-2"><ProcessTable items={data.processes.items} limit={12} label="Top processes at this moment" /></div>
					{:else}
						<p class="mt-2 text-small text-muted">No process snapshot near this moment. Snapshots are taken every minute and during spikes, and kept for 7 days.</p>
					{/if}
				</section>

				<section class="mt-5" aria-labelledby="m-events">
					<h3 id="m-events" class="eyebrow">Nearby events</h3>
					{#if data.events.length}
						<ol class="mt-2 grid gap-1.5 text-small">
							{#each data.events as e, i (i)}
								<li class="flex items-baseline gap-2">
									<span class="w-16 shrink-0 font-mono text-muted tabular-nums">{fmtStamp(e.t_ms, 0)}</span>
									<span class="pill shrink-0" data-tone={eventTone(e.kind) === 'data' || eventTone(e.kind) === 'muted' ? undefined : eventTone(e.kind)}>{eventLabel[e.kind] ?? e.kind}</span>
									<span class="min-w-0">{#if e.bot_id}<a class="link" href="/bots/{e.bot_id}">{e.bot_name || e.bot_id}</a>: {/if}{e.text}</span>
								</li>
							{/each}
						</ol>
					{:else}
						<p class="mt-2 text-small text-muted">Nothing happened within 10 minutes of this moment.</p>
					{/if}
				</section>
			{/if}
		</div>

		<footer class="flex flex-wrap gap-2 border-t border-rule-soft px-5 py-3">
			{#if spike}
				<button class="btn btn-sm" onclick={zoomSpike}><Icon name="search" size={14} />Zoom to this spike</button>
			{:else}
				<button class="btn btn-sm" onclick={zoomHere}><Icon name="search" size={14} />Zoom to ±5 minutes</button>
			{/if}
			{#if session.features.ai}
				<button class="btn btn-sm btn-primary" onclick={ask}><Icon name="sparkle" size={14} />Ask the assistant</button>
			{/if}
		</footer>
	</div>
</dialog>
