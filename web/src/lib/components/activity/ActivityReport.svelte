<script lang="ts">
	import type { ActivityCategory, ActivitySource, ActivityStats, Filters } from '$lib/activity';
	import { agents, categories, sourceText } from '$lib/activity';
	import { logoSeed } from '$lib/avatar';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';

	// The report for the current window and filters: who acted (people versus
	// the panel), the most active people and apps, what kind of change, and
	// when in the week it happens. Every row narrows the timeline.
	let {
		stats,
		filters,
		onfilter
	}: {
		stats: ActivityStats;
		filters: Filters;
		onfilter: (kind: 'actor' | 'app' | 'category' | 'who', id: string) => void;
	} = $props();

	const sources: ActivitySource[] = ['person', 'schedule', 'push', 'system', 'ai'];
	const total = $derived(Math.max(1, stats.total));
	const autoCount = $derived(stats.total - (stats.by_source.person ?? 0));
	const failed = $derived((stats.by_outcome.failed ?? 0) + (stats.by_outcome.denied ?? 0));
	const pct = (v: number) => (v / total) * 100;
	const fmtPct = (v: number) => (stats.total ? `${Math.round(pct(v))}%` : '–');
	const topPeople = $derived(stats.people.slice(0, 6));
	const topApps = $derived(stats.apps.slice(0, 6));
	const peopleMax = $derived(Math.max(1, ...topPeople.map((p) => p.count)));
	const appsMax = $derived(Math.max(1, ...topApps.map((a) => a.count)));
	const cats = $derived(
		categories
			.map((c) => ({ ...c, count: stats.by_category[c.id] ?? 0 }))
			.filter((c) => c.count > 0)
			.sort((a, b) => b.count - a.count)
	);
	const catMax = $derived(Math.max(1, ...cats.map((c) => c.count)));
	const weekMax = $derived(Math.max(1, ...stats.week.flat()));
	const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
	const busiest = $derived.by(() => {
		let best = { d: 0, h: 0, c: 0 };
		stats.week.forEach((row, d) => row.forEach((c, h) => c > best.c && (best = { d, h, c })));
		return best.c ? `Busiest: ${['Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays', 'Sundays'][best.d]} around ${String(best.h).padStart(2, '0')}:00` : '';
	});
</script>

<div class="report">
	<section aria-labelledby="rep-split">
		<h2 id="rep-split" class="rep-h">Who did it</h2>
		<p class="rep-lede">
			<strong class="tabular-nums">{stats.total.toLocaleString()}</strong>
			{stats.total === 1 ? 'event' : 'events'}:
			<strong class="tabular-nums">{fmtPct(stats.by_source.person ?? 0)}</strong> by people,
			<strong class="tabular-nums">{fmtPct(autoCount)}</strong> automatic{#if failed}, <strong class="tabular-nums text-fail">{failed.toLocaleString()}</strong> failed or refused{/if}.
		</p>
		<div class="split" aria-hidden="true">
			{#each sources as s (s)}
				{@const v = stats.by_source[s] ?? 0}
				{#if v}<span class="seg-{s}" style:flex-grow={v} title="{sourceText[s]}: {v}"></span>{/if}
			{/each}
		</div>
		<ul class="legend">
			{#each sources as s (s)}
				{@const v = stats.by_source[s] ?? 0}
				<li>
					<button
						type="button"
						class="legend-item"
						disabled={!v}
						aria-pressed={(s === 'person' && filters.who === 'people') || (s !== 'person' && filters.who === 'automatic')}
						onclick={() => onfilter('who', s === 'person' ? 'people' : 'automatic')}
						title={s === 'person' ? 'Show only what people did' : `${agents[s].text}. Show only automatic activity.`}
					>
						<span class="dot seg-{s}"></span>{sourceText[s]}<span class="ml-auto tabular-nums text-muted">{v.toLocaleString()}</span>
					</button>
				</li>
			{/each}
		</ul>
	</section>

	{#if topPeople.length}
		<section aria-labelledby="rep-people">
			<h2 id="rep-people" class="rep-h">Most active people</h2>
			<ul class="bars">
				{#each topPeople as p (p.id)}
					<li>
						<button type="button" class="bar-row" aria-pressed={filters.actors.includes(p.id)} disabled={!p.person} onclick={() => onfilter('actor', p.id)} title="Show only {p.label}">
							{#if p.person}
								<Logo src={p.person.avatar_url} kind="user" seed={logoSeed(p.person.id, p.person.logo_variant)} class="size-5 rounded-pill" />
							{:else}
								<span class="grid size-5 place-items-center rounded-pill bg-paper-2 text-muted"><Icon name="users" size={11} /></span>
							{/if}
							<span class="bar-label">{p.label}</span>
							<span class="bar-val tabular-nums">{#if p.failed}<span class="fails" title="{p.failed} failed or refused">{p.failed}</span>{/if}{p.count.toLocaleString()}</span>
							<span class="bar-track" aria-hidden="true"><span style:width="{(p.count / peopleMax) * 100}%"></span></span>
						</button>
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if topApps.length}
		<section aria-labelledby="rep-apps">
			<h2 id="rep-apps" class="rep-h">Busiest apps</h2>
			<ul class="bars">
				{#each topApps as a (a.app.id)}
					<li>
						<button type="button" class="bar-row" aria-pressed={filters.apps.includes(a.app.id)} onclick={() => onfilter('app', a.app.id)} title="Show only {a.app.name}">
							<Logo src={a.app.logo_url} kind={a.app.kind === 'site' ? 'site' : 'bot'} seed={logoSeed(a.app.id, a.app.logo_variant)} class="size-5 rounded-[5px]" />
							<span class="bar-label">{a.app.name}</span>
							<span class="bar-val tabular-nums">{#if a.failed}<span class="fails" title="{a.failed} failed or refused">{a.failed}</span>{/if}{a.count.toLocaleString()}</span>
							<span class="bar-track" aria-hidden="true"><span style:width="{(a.count / appsMax) * 100}%"></span></span>
						</button>
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if cats.length}
		<section aria-labelledby="rep-cats">
			<h2 id="rep-cats" class="rep-h">What happened</h2>
			<ul class="bars">
				{#each cats as c (c.id)}
					<li>
						<button type="button" class="bar-row" aria-pressed={filters.categories.includes(c.id as ActivityCategory)} onclick={() => onfilter('category', c.id)} title="Show only {c.label.toLowerCase()}">
							<span class="grid size-5 place-items-center text-muted"><Icon name={c.icon} size={14} /></span>
							<span class="bar-label">{c.label}</span>
							<span class="bar-val tabular-nums">{c.count.toLocaleString()}</span>
							<span class="bar-track" aria-hidden="true"><span style:width="{(c.count / catMax) * 100}%"></span></span>
						</button>
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if stats.total}
		<section aria-labelledby="rep-week">
			<h2 id="rep-week" class="rep-h">When in the week</h2>
			<div class="week" role="img" aria-label="Activity by weekday and hour, in your time zone. {busiest}">
				{#each stats.week as row, d (d)}
					<span class="wk-day">{days[d]}</span>
					{#each row as c, h (h)}
						<span class="wk-cell" style:--v={c ? 0.14 + (c / weekMax) * 0.86 : 0} title="{days[d]} {String(h).padStart(2, '0')}:00 – {c}"></span>
					{/each}
				{/each}
				<span></span>
				{#each Array(24) as _, h (h)}<span class="wk-hour">{h % 6 === 0 ? String(h).padStart(2, '0') : ''}</span>{/each}
			</div>
			{#if busiest}<p class="mt-1.5 text-small text-muted">{busiest}.</p>{/if}
		</section>
	{/if}
</div>

<style>
	.report {
		display: grid;
		gap: 1.5rem;
	}
	.rep-h {
		margin-bottom: 0.5rem;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		font-weight: 600;
	}
	.rep-lede {
		font-size: var(--text-small);
		line-height: 1.25rem;
		color: color-mix(in srgb, var(--color-ink) 85%, var(--color-muted));
	}
	.rep-lede strong {
		font-weight: 600;
		color: var(--color-ink);
	}
	.split {
		display: flex;
		gap: 2px;
		height: 8px;
		margin-top: 0.625rem;
		overflow: hidden;
		border-radius: var(--radius-pill);
		background: var(--color-paper-2);
	}
	.split > span {
		flex-basis: 0;
		min-width: 3px;
		transition: flex-grow 420ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.seg-person {
		background: var(--color-ink);
		opacity: 0.82;
	}
	.seg-schedule {
		background: color-mix(in srgb, var(--color-data) 70%, var(--color-panel));
	}
	.seg-push {
		background: color-mix(in srgb, var(--color-data) 45%, var(--color-panel));
	}
	.seg-system {
		background: color-mix(in srgb, var(--color-data) 28%, var(--color-panel));
	}
	.seg-ai {
		background: color-mix(in srgb, var(--color-action) 70%, var(--color-panel));
	}
	.legend {
		display: grid;
		margin-top: 0.5rem;
	}
	.legend-item,
	.bar-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: 100%;
		border-radius: var(--radius-control);
		padding: 0.1875rem 0.375rem;
		margin-inline: -0.375rem;
		box-sizing: content-box;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		text-align: left;
		transition: background-color 120ms;
	}
	.legend-item:hover:not(:disabled),
	.bar-row:hover:not(:disabled) {
		background: color-mix(in srgb, var(--color-ink) 5%, transparent);
	}
	.legend-item[aria-pressed='true'],
	.bar-row[aria-pressed='true'] {
		background: color-mix(in srgb, var(--color-action) 9%, transparent);
	}
	.legend-item:disabled {
		opacity: 0.5;
	}
	.dot {
		width: 8px;
		height: 8px;
		flex: none;
		border-radius: 2px;
	}
	.bars {
		display: grid;
		gap: 0.125rem;
	}
	.bar-row {
		display: grid;
		grid-template-columns: 1.25rem minmax(0, 1fr) auto;
		grid-template-rows: auto 3px;
		column-gap: 0.5rem;
		row-gap: 0.25rem;
		padding-block: 0.3125rem;
	}
	.bar-row > :global(:first-child) {
		grid-row: span 2;
		align-self: center;
	}
	.bar-label {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.bar-val {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		color: var(--color-muted);
	}
	/* Failures beside a count: a small red tally, explained by its title. */
	.fails {
		border-radius: var(--radius-pill);
		padding: 0 0.3rem;
		font-size: 0.6875rem;
		line-height: 1rem;
		font-weight: 600;
		color: var(--color-fail);
		background: color-mix(in srgb, var(--color-fail) 12%, transparent);
	}
	.bar-track {
		grid-column: 2 / -1;
		overflow: hidden;
		border-radius: var(--radius-pill);
		background: var(--color-rule-soft);
	}
	.bar-track > span {
		display: block;
		height: 100%;
		border-radius: inherit;
		background: color-mix(in srgb, var(--color-ink) 55%, var(--color-panel));
		transition: width 420ms cubic-bezier(0.16, 1, 0.3, 1);
	}
	.bar-row[aria-pressed='true'] .bar-track > span {
		background: var(--color-action);
	}
	.week {
		display: grid;
		grid-template-columns: 1.75rem repeat(24, minmax(0, 1fr));
		gap: 2px;
		align-items: center;
	}
	.wk-day,
	.wk-hour {
		font-size: 0.625rem;
		line-height: 0.75rem;
		color: var(--color-muted);
		font-variant-numeric: tabular-nums;
	}
	.wk-hour {
		white-space: nowrap;
	}
	.wk-cell {
		aspect-ratio: 1;
		border-radius: 2px;
		background: color-mix(in srgb, var(--color-data) calc(var(--v) * 100%), var(--color-rule-soft));
	}
	@media (prefers-reduced-motion: reduce) {
		.split > span,
		.bar-track > span {
			transition: none;
		}
	}
</style>
