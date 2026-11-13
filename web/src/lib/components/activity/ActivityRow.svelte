<script lang="ts">
	import type { ActivityItem } from '$lib/activity';
	import { actorName, appHref, categoryOf, clockTime, onBehalfOf, phraseOf, scheduledDetail, toneOf } from '$lib/activity';
	import { fmtWhen } from '$lib/args';
	import { logoSeed } from '$lib/avatar';
	import { elapsed, triggerText } from '$lib/ops';
	import BuildOutput from '$lib/components/BuildOutput.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import ActorMark from './ActorMark.svelte';

	// One timeline entry, written as a sentence: who, did what, to which app.
	// Build output expands in place for people who may read the console.
	let {
		item,
		now = Date.now(),
		canOutput = false,
		onfilter
	}: {
		item: ActivityItem;
		now?: number;
		canOutput?: boolean;
		/** Narrow the timeline to this item's person, app or category. */
		onfilter?: (kind: 'actor' | 'app' | 'category', id: string) => void;
	} = $props();

	let open = $state(false);
	const tone = $derived(toneOf(item));
	const who = $derived(actorName(item));
	const phrase = $derived(phraseOf(item));
	const behalf = $derived(onBehalfOf(item));
	const cat = $derived(categoryOf[item.category] ?? categoryOf.other);
	const sched = $derived(scheduledDetail(item));
	const op = $derived(item.op);
	const sha = $derived(op?.source_ref && /^[0-9a-f]{40}$/.test(op.source_ref) ? op.source_ref.slice(0, 7) : '');
	const hasOutput = $derived(!!op && canOutput && op.kind === 'build' && (item.outcome === 'running' || op.log_bytes > 0));
	const appSubject = $derived(phrase.place === 'subject');
	// Crash text and scheduled-run messages read as prose; names and paths as code.
	const proseTarget = $derived(appSubject || !!sched);
	const pill = $derived.by(() => {
		if (item.action === 'bot.crash') return { text: 'Crashed', tone: 'fail' };
		if (item.action === 'bot.gave_up') return { text: 'Gave up restarting', tone: 'fail' };
		if (sched?.status === 'skipped') return { text: 'Skipped', tone: 'idle' };
		if (sched?.status === 'missed') return { text: 'Missed', tone: 'fail' };
		switch (item.outcome) {
			case 'failed':
				return { text: item.type === 'work' ? 'Failed' : 'Did not complete', tone: 'fail' };
			case 'denied':
				return { text: 'Refused', tone: 'fail' };
			case 'running':
				return { text: op?.stage || 'In progress', tone: 'warn' };
			case 'cancelled':
				return { text: 'Cancelled', tone: 'idle' };
		}
		return null;
	});
	const message = $derived(op && item.outcome !== 'running' ? op.message : sched?.message || (proseTarget ? item.target : null));
</script>

<li class="act-row" data-tone={tone} data-busy={item.outcome === 'running'}>
	<div class="act-mark"><ActorMark {item} /></div>
	<div class="min-w-0 flex-1">
		<p class="act-sentence">
			{#if !appSubject}
				{#if item.source === 'person' && item.actor && onfilter}
					<button type="button" class="act-who" onclick={() => onfilter('actor', item.actor!.id)} title="Show only {who}">{who}</button>
				{:else}
					<span class="act-who" class:agent={item.source !== 'person'}>{who}</span>
				{/if}
			{/if}
			{#if phrase.place === 'subject'}
				{@render app()}{' '}{phrase.post}
			{:else if phrase.place === 'inline'}
				{phrase.pre}{' '}{@render app()}{#if phrase.post}{phrase.post.startsWith('’') ? '' : ' '}{phrase.post}{/if}
			{:else if phrase.place === 'on'}
				{phrase.pre}<span class="text-muted">{' on '}</span>{@render app()}
			{:else}
				{phrase.pre}
			{/if}
			{#if sha}<code class="act-code">{sha}</code>{/if}
			{#if item.target && !proseTarget && item.action !== 'account.sign_in'}<code class="act-code break-all">{item.target}</code>{/if}
		</p>
		<p class="act-meta">
			<button type="button" class="act-cat" onclick={() => onfilter?.('category', item.category)} title="Show only {cat.label.toLowerCase()}" disabled={!onfilter}>
				<Icon name={cat.icon} size={12} />{cat.label}
			</button>
			{#if behalf}<span>for {behalf}</span>{/if}
			{#if op}
				{#if op.trigger !== 'manual' && op.trigger !== 'push' && op.trigger !== 'schedule'}<span>{triggerText[op.trigger]}</span>{/if}
				{#if op.source_label}<span class="truncate">{op.source_label}</span>{/if}
				<span class="tabular-nums">{item.outcome === 'running' ? 'running for' : 'took'} {elapsed(op, now)}</span>
			{/if}
		</p>
		{#if pill}<p class="act-pill-narrow"><span class="act-pill" data-tone={pill.tone}>{pill.text}</span></p>{/if}
		{#if message}<p class="act-message" class:fail={tone === 'fail'}>{message}</p>{/if}
		{#if hasOutput}
			<button class="btn btn-sm btn-quiet mt-1 -ml-2" aria-expanded={open} onclick={() => (open = !open)}><Icon name="terminal" size={14} />{open ? 'Hide output' : 'Build output'}</button>
			{#if open && op}<div class="mt-2"><BuildOutput botId={op.bot_id} opId={op.id} /></div>{/if}
		{/if}
	</div>
	<div class="act-aside">
		{#if pill}<span class="act-pill act-pill-wide" data-tone={pill.tone}>{pill.text}</span>{/if}
		<time class="act-time" datetime={new Date(item.at_ms).toISOString()} title={fmtWhen(item.at_ms)}>{clockTime(item.at_ms)}</time>
	</div>
</li>

{#snippet app()}
	{#if item.app}
		<a class="act-app" href={appHref(item.app)}>
			<Logo src={item.app.logo_url} kind={item.app.kind === 'site' ? 'site' : 'bot'} seed={logoSeed(item.app.id, item.app.logo_variant)} class="size-4 rounded-[4px]" />{item.app.name}</a
		>
	{/if}
{/snippet}

<style>
	.act-row {
		--tone: var(--color-rule);
		position: relative;
		display: flex;
		align-items: flex-start;
		gap: 0.875rem;
		padding: 0.75rem 1rem 0.75rem 1.25rem;
		animation: act-in 360ms cubic-bezier(0.16, 1, 0.3, 1) both;
	}
	.act-row[data-tone='run'] {
		--tone: var(--color-run);
	}
	.act-row[data-tone='warn'] {
		--tone: var(--color-warn);
	}
	.act-row[data-tone='fail'] {
		--tone: var(--color-fail);
		background: color-mix(in srgb, var(--color-fail) 3.5%, transparent);
	}
	@keyframes act-in {
		from {
			opacity: 0;
			transform: translateY(-4px);
		}
	}
	/* A knot left of the mark carries the state: green finished work, amber
	   work in progress, red failures. Plain changes need none. */
	.act-mark {
		position: relative;
		z-index: 1;
		padding-top: 1px;
	}
	.act-mark::after {
		content: '';
		position: absolute;
		left: -4.5px;
		top: 13px;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--tone);
		box-shadow: 0 0 0 2px var(--color-panel);
		transform: translateX(-100%);
	}
	.act-row[data-tone='idle'] .act-mark::after {
		display: none;
	}
	.act-row[data-busy='true'] .act-mark::after {
		animation: act-pulse 1.4s ease-in-out infinite;
	}
	@keyframes act-pulse {
		50% {
			box-shadow:
				0 0 0 2px var(--color-panel),
				0 0 0 5px color-mix(in srgb, var(--color-warn) 28%, transparent);
		}
	}
	.act-sentence {
		line-height: 1.5rem;
		overflow-wrap: anywhere;
	}
	.act-who {
		font-weight: 600;
		color: var(--color-ink);
		margin-right: 0.25em;
	}
	button.act-who:hover {
		text-decoration: underline;
		text-underline-offset: 3px;
		text-decoration-color: var(--color-rule);
	}
	.act-who.agent {
		color: var(--color-ink);
	}
	.act-app {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		vertical-align: -0.2rem;
		margin-inline: 0.15em;
		border-radius: var(--radius-inner);
		padding: 0 0.3rem 0 0.2rem;
		font-weight: 500;
		line-height: 1.375rem;
		background: color-mix(in srgb, var(--color-ink) 4.5%, transparent);
		transition: background-color 120ms;
	}
	.act-app:hover {
		background: color-mix(in srgb, var(--color-ink) 9%, transparent);
	}
	.act-code {
		margin-left: 0.35em;
		font-size: var(--text-small);
		color: color-mix(in srgb, var(--color-ink) 85%, var(--color-muted));
	}
	.act-meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		column-gap: 0.625rem;
		row-gap: 0.125rem;
		margin-top: 0.125rem;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		color: var(--color-muted);
	}
	.act-meta > * + *::before {
		content: '';
		display: inline-block;
		width: 3px;
		height: 3px;
		margin-right: 0.625rem;
		border-radius: 50%;
		background: currentColor;
		opacity: 0.55;
		vertical-align: middle;
	}
	.act-cat {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		color: inherit;
	}
	.act-cat:hover:not(:disabled) {
		color: var(--color-ink);
	}
	.act-message {
		margin-top: 0.25rem;
		max-width: 75ch;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		color: color-mix(in srgb, var(--color-ink) 80%, var(--color-muted));
		overflow-wrap: anywhere;
	}
	.act-message.fail {
		color: var(--color-fail);
	}
	.act-aside {
		display: flex;
		flex: none;
		align-items: center;
		gap: 0.625rem;
		padding-top: 0.125rem;
	}
	.act-time {
		min-width: 3.25rem;
		text-align: right;
		font-size: var(--text-small);
		font-variant-numeric: tabular-nums;
		color: var(--color-muted);
	}
	.act-pill {
		--c: var(--color-muted);
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		border-radius: var(--radius-pill);
		padding: 0.0625rem 0.5rem;
		font-size: 0.75rem;
		font-weight: 500;
		line-height: 1.125rem;
		white-space: nowrap;
		color: var(--c);
		background: color-mix(in srgb, var(--c) 11%, transparent);
	}
	.act-pill[data-tone='fail'] {
		--c: var(--color-fail);
	}
	.act-pill[data-tone='warn'] {
		--c: var(--color-warn);
	}
	.act-pill-narrow {
		display: none;
		margin-top: 0.375rem;
	}
	@media (max-width: 40rem) {
		.act-row {
			gap: 0.625rem;
			padding-right: 0.75rem;
		}
		.act-pill-wide {
			display: none;
		}
		.act-pill-narrow {
			display: block;
		}
		.act-time {
			min-width: 0;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.act-row,
		.act-row[data-busy='true'] .act-mark::after {
			animation: none;
		}
	}
</style>
