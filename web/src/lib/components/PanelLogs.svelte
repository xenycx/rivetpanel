<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { LogEntry, LogLevel, LogStats } from '$lib/api/admin';
	import { fmtAgo } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// The panel's own log, read from memory: the newest lines since it started.
	// The full history stays wherever stderr goes (journalctl, docker logs).
	let entries = $state<LogEntry[]>([]);
	let stats = $state<LogStats | null>(null);
	let level = $state<'all' | 'info' | 'warn' | 'error'>('all');
	let search = $state('');
	let live = $state(true);
	let error = $state('');
	let box: HTMLDivElement | undefined = $state();
	let pinned = $state(true); // scrolled to the bottom: follow new lines
	let loadedOnce = $state(false);
	const KEEP = 1000; // lines kept in the page

	const levelParam = $derived(level === 'all' ? '' : level);
	const url = (after?: number) => {
		const p = new URLSearchParams({ limit: after ? '300' : '300' });
		if (levelParam) p.set('level', levelParam);
		if (search.trim()) p.set('q', search.trim());
		if (after) p.set('after', String(after));
		return `/admin/logs?${p}`;
	};

	let generation = 0; // a changed filter drops answers to older requests
	async function load(full: boolean) {
		const gen = full ? ++generation : generation;
		try {
			const after = full ? 0 : (entries.at(-1)?.seq ?? 0);
			const r = await api<{ entries: LogEntry[]; stats: LogStats }>('GET', url(after));
			if (gen !== generation) return;
			stats = r.stats;
			if (full) entries = r.entries;
			else if (r.entries.length) entries = [...entries, ...r.entries].slice(-KEEP);
			error = '';
			loadedOnce = true;
			if (pinned && (full || r.entries.length)) {
				await tick();
				if (box) box.scrollTop = box.scrollHeight;
			}
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The log could not be read.';
		}
	}
	let timer: ReturnType<typeof setInterval> | undefined;
	onMount(() => {
		void load(true);
		timer = setInterval(() => {
			if (live && document.visibilityState === 'visible') void load(false);
		}, 2500);
		return () => clearInterval(timer);
	});
	let debounce: ReturnType<typeof setTimeout> | undefined;
	function refilter() {
		clearTimeout(debounce);
		debounce = setTimeout(() => load(true), 250);
	}
	function onScroll() {
		if (!box) return;
		pinned = box.scrollHeight - box.scrollTop - box.clientHeight < 24;
	}

	const time = (ms: number) => new Date(ms).toLocaleTimeString([], { hour12: false }) + '.' + String(ms % 1000).padStart(3, '0');
	const day = (ms: number) => new Date(ms).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
	const tone: Record<LogLevel, string> = { debug: 'text-muted', info: 'text-run', warn: 'text-warn', error: 'text-fail' };
	let open = $state<Record<number, boolean>>({});

	async function copy() {
		const text = entries.map((e) => `${new Date(e.time_ms).toISOString()} ${e.level.toUpperCase()} ${e.msg}${(e.attrs ?? []).map((a) => ` ${a.key}=${JSON.stringify(a.value)}`).join('')}`).join('\n');
		try {
			await navigator.clipboard.writeText(text);
			toast('Copied the shown lines', 'success');
		} catch {
			toast('Copying is not allowed here', 'fail');
		}
	}
	const downloadHref = $derived(`/api/v1/admin/logs?download=1${levelParam ? `&level=${levelParam}` : ''}${search.trim() ? `&q=${encodeURIComponent(search.trim())}` : ''}`);
</script>

<div class="flex flex-wrap items-center gap-2">
	<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Level">
		{#each [['all', 'Everything'], ['info', 'Info'], ['warn', 'Warnings'], ['error', 'Errors']] as [id, label] (id)}
			<button class="rounded-inner px-2.5 py-1 text-small font-medium {level === id ? 'bg-paper-2 text-ink shadow-[inset_0_0_0_1px_var(--color-rule)]' : 'text-muted hover:text-ink'}" aria-pressed={level === id} onclick={() => { level = id as typeof level; void load(true); }}>{label}</button>
		{/each}
	</div>
	<label class="min-w-48 flex-1 sm:max-w-sm"><span class="sr-only">Search the log</span><input class="field" type="search" placeholder="Search messages and fields" bind:value={search} oninput={refilter} /></label>
	<label class="flex items-center gap-2 text-small"><input type="checkbox" bind:checked={live} />Follow live</label>
	<span class="flex-1"></span>
	<button class="btn btn-sm" onclick={copy} disabled={!entries.length}><Icon name="copy" size={13} />Copy</button>
	<a class="btn btn-sm" href={downloadHref} download><Icon name="download" size={13} />Download</a>
</div>

{#if stats}
	<p class="mt-2 text-small text-muted">
		{stats.total.toLocaleString()} line{stats.total === 1 ? '' : 's'} since the panel started
		· <span class={stats.errors ? 'text-fail' : ''}>{stats.errors} error{stats.errors === 1 ? '' : 's'}</span>
		· <span class={stats.warnings ? 'text-warn' : ''}>{stats.warnings} warning{stats.warnings === 1 ? '' : 's'}</span>
		· the newest {stats.held.toLocaleString()} are held in memory{stats.held >= stats.capacity ? ` (oldest ${fmtAgo(stats.oldest_ms)})` : ''}.
	</p>
{/if}
{#if error}<p class="mt-2 text-small text-fail" role="alert">{error}</p>{/if}

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div
	bind:this={box}
	onscroll={onScroll}
	class="mt-3 h-[min(34rem,60vh)] overflow-auto rounded-tile border border-rule-soft bg-term p-2 font-mono text-[12px] leading-5 text-term-ink"
	role="log"
	aria-label="Panel log"
	aria-live="off"
	tabindex="0"
>
	{#each entries as e, i (e.seq)}
		{@const newDay = i === 0 || day(entries[i - 1].time_ms) !== day(e.time_ms)}
		{#if newDay}<p class="py-1 text-center text-[11px] tracking-wide text-term-ink/50 uppercase">{day(e.time_ms)}</p>{/if}
		<div class="group rounded-inner px-1.5 py-px hover:bg-white/5 {e.level === 'error' ? 'bg-fail/10' : ''}">
			<button class="flex w-full items-start gap-2 text-left" onclick={() => (open[e.seq] = !open[e.seq])} aria-expanded={open[e.seq] ?? false} disabled={!e.attrs?.length}>
				<span class="shrink-0 text-term-ink/50">{time(e.time_ms)}</span>
				<span class="w-11 shrink-0 font-semibold uppercase {tone[e.level]}">{e.level}</span>
				<span class="min-w-0 flex-1 break-words whitespace-pre-wrap">{e.msg}{#if e.attrs?.length && !open[e.seq]}<span class="text-term-ink/50">{#each e.attrs.slice(0, 4) as a (a.key)}&nbsp; {a.key}=<span class="text-term-ink/80">{a.value.length > 60 ? a.value.slice(0, 60) + '…' : a.value}</span>{/each}{e.attrs.length > 4 ? ` +${e.attrs.length - 4}` : ''}</span>{/if}</span>
			</button>
			{#if open[e.seq] && e.attrs}
				<dl class="mt-0.5 mb-1 ml-[8.5rem] grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 text-term-ink/80">
					{#each e.attrs as a (a.key)}<dt class="text-term-ink/50">{a.key}</dt><dd class="break-all whitespace-pre-wrap">{a.value}</dd>{/each}
				</dl>
			{/if}
		</div>
	{:else}
		<p class="p-3 text-term-ink/60">{loadedOnce ? (search || level !== 'all' ? 'No lines match this filter.' : 'Nothing has been logged yet.') : 'Reading the log…'}</p>
	{/each}
</div>
{#if !pinned && live}
	<p class="mt-1 text-right text-small text-muted"><button class="link" onclick={() => { if (box) box.scrollTop = box.scrollHeight; }}>Jump to the newest line</button></p>
{/if}
<p class="mt-3 max-w-3xl text-small text-muted">Secrets, tokens and passwords are replaced with [redacted] before a line is kept. This view starts empty after every restart; the complete history is in the service log of the host (<code>journalctl -u rivetpanel</code>) or the container (<code>docker logs</code>).</p>
