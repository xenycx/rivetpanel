<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { api, ApiError, fetchText, fmtBytes } from '$lib/api/client';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	// The day files of one log (a server's console output or the panel's own
	// log): the current day is live, ended days are gzip archives. A date can
	// appear twice while late lines wait to be added to its archive.
	type Day = { date: string; bytes: number; archived: boolean; capped?: boolean };
	let {
		path,
		filename,
		label = 'Log history',
		empty = 'No day files yet.',
		refresh = 0
	}: { path: string; filename: string; label?: string; empty?: string; refresh?: number } = $props();

	let days = $state<Day[] | null>(null);
	let current = $state('');
	let error = $state('');
	let missing = $state(false); // the panel has no log archive (route absent)
	let viewing = $state<{ date: string; text: string; cut: boolean } | null>(null);
	let viewError = $state('');
	let loadingView = $state('');
	let box: HTMLPreElement | undefined = $state();
	const VIEW_MAX = 16 * 1024 * 1024; // larger live files are offered as a download only
	const VIEW_LINES = 2000;

	async function load() {
		try {
			const r = await api<{ days: Day[] | null; current_day: string }>('GET', path);
			days = r.days ?? [];
			current = r.current_day;
			error = '';
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) missing = true;
			else error = e instanceof ApiError ? e.message : 'The log days could not be read.';
		}
	}
	onMount(load);
	let seen = 0;
	$effect(() => {
		if (refresh !== seen) {
			seen = refresh;
			void load();
		}
	});

	const href = (d: Day) => `/api/v1${path}/${d.date}?archived=${d.archived ? 1 : 0}`;
	const fullDate = (s: string) => {
		const [y, m, d] = s.split('-').map(Number);
		return new Date(y, m - 1, d).toLocaleDateString(undefined, { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric' });
	};

	async function view(d: Day) {
		if (viewing?.date === d.date) {
			viewing = null;
			return;
		}
		loadingView = d.date;
		viewError = '';
		try {
			const text = await fetchText(`${path}/${d.date}?archived=0`);
			const lines = text.replace(/\n$/, '').split('\n');
			const cut = lines.length > VIEW_LINES;
			viewing = { date: d.date, text: (cut ? lines.slice(-VIEW_LINES) : lines).join('\n'), cut };
			await tick();
			if (box) box.scrollTop = box.scrollHeight;
		} catch (e) {
			viewError = e instanceof ApiError ? e.message : 'The file could not be read.';
		} finally {
			loadingView = '';
		}
	}
	const total = $derived((days ?? []).reduce((n, d) => n + d.bytes, 0));
</script>

{#if !missing}
	<section class="min-w-0" aria-label={label}>
		<div class="flex flex-wrap items-center gap-2">
			<h3 class="text-title font-semibold">{label}</h3>
			{#if days?.length}<span class="text-small text-muted">{days.length} file{days.length === 1 ? '' : 's'}, {fmtBytes(total)} on disk</span>{/if}
			<span class="flex-1"></span>
			<button class="btn btn-sm" onclick={load}><Icon name="restart" size={13} />Refresh</button>
		</div>
		{#if error}<p class="mt-2 text-small text-fail" role="alert">{error}</p>{/if}
		{#if days === null && !error}
			<div class="mt-3"><Skeleton rows={3} label="Loading log days" /></div>
		{:else if days && days.length === 0}
			<p class="mt-3 text-small text-muted">{empty}</p>
		{:else if days}
			<ul class="mt-3 list-card">
				{#each days as d (d.date + d.archived)}
					{@const live = !d.archived && d.date === current}
					<li class="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-2.5">
						<Icon name={d.archived ? 'archive' : live ? 'activity' : 'file'} size={15} class="shrink-0 text-muted" />
						<div class="min-w-0 flex-1 basis-56">
							<p class="font-medium"><span class="font-mono">{d.date}</span> <span class="ml-1 text-small font-normal text-muted">{fullDate(d.date)}</span></p>
							<p class="text-small break-all text-muted">{fmtBytes(d.bytes)}{d.archived ? ' compressed' : ''} · {d.archived ? `${filename}-${d.date}.log.gz` : `${filename}-${d.date}.log`}</p>
						</div>
						{#if d.capped}<span class="pill shrink-0" data-tone="warn" title="The file reached the daily limit; later lines of this day were not stored">Capped</span>{/if}
						<span class="pill shrink-0" data-tone={d.archived ? 'idle' : live ? 'run' : 'warn'}>{d.archived ? 'Archived' : live ? 'Today, live' : 'Not archived yet'}</span>
						<div class="flex shrink-0 gap-1.5">
							{#if !d.archived}
								<button class="btn btn-sm" onclick={() => view(d)} disabled={d.bytes > VIEW_MAX || loadingView === d.date} aria-expanded={viewing?.date === d.date} title={d.bytes > VIEW_MAX ? 'Too large to show here; download it' : 'Show the newest lines'}><Icon name="eye" size={13} />{viewing?.date === d.date ? 'Hide' : loadingView === d.date ? 'Reading…' : 'View'}</button>
							{/if}
							<a class="btn btn-sm" href={href(d)} download aria-label="Download {d.date}{d.archived ? ' archive' : ''}"><Icon name="download" size={13} />Download</a>
						</div>
					</li>
				{/each}
			</ul>
			{#if viewError}<p class="mt-2 text-small text-fail" role="alert">{viewError}</p>{/if}
			{#if viewing}
				<div class="mt-3 overflow-hidden rounded-tile border border-rule-soft bg-term text-term-ink">
					<div class="flex flex-wrap items-center gap-2 border-b border-white/8 bg-black/25 px-3.5 py-2 text-[.78rem]">
						<span class="font-mono">{viewing.date}</span>
						<span class="text-term-ink/60">{viewing.cut ? `the newest ${VIEW_LINES.toLocaleString()} lines` : 'the whole file'} (as of opening)</span>
						<span class="flex-1"></span>
						<button class="btn btn-quiet btn-icon btn-sm !text-term-ink/70 hover:!bg-white/10 hover:!text-term-ink" onclick={() => (viewing = null)} aria-label="Close"><Icon name="x" size={13} /></button>
					</div>
					<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
					<pre bind:this={box} class="h-[min(28rem,55vh)] overflow-auto p-3 font-mono text-[12px] leading-5 break-words whitespace-pre-wrap" tabindex="0">{viewing.text || '(empty)'}</pre>
				</div>
			{/if}
		{/if}
	</section>
{/if}
