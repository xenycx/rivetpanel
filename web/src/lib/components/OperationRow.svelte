<script lang="ts">
	import type { Operation } from '$lib/api/types';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { active, elapsed, opNoun, opOwnerHref, opTone, statusText, triggerText } from '$lib/ops';
	import BuildOutput from '$lib/components/BuildOutput.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// One operation on a state-spine timeline. Build output expands in place.
	let {
		op,
		now = Date.now(),
		showBot = false,
		canOutput = false,
		initiallyOpen = false,
		actions
	}: { op: Operation; now?: number; showBot?: boolean; canOutput?: boolean; initiallyOpen?: boolean; actions?: import('svelte').Snippet } = $props();

	// svelte-ignore state_referenced_locally (the row keeps its own open state after the first render)
	let open = $state(initiallyOpen);

	const tone = $derived(opTone(op));
	const hasOutput = $derived(canOutput && op.kind === 'build' && (active(op) || op.log_bytes > 0));
	const sha = $derived(op.source_ref && /^[0-9a-f]{40}$/.test(op.source_ref) ? op.source_ref.slice(0, 7) : '');
</script>

<li class="spine py-3 pr-2 pl-5" data-tone={tone} data-busy={active(op)}>
	<div class="flex flex-wrap items-start gap-x-3 gap-y-1">
		<div class="min-w-0 flex-1">
			<p class="font-medium">
				{opNoun(op)}{#if showBot}{' '}of <a class="link" href={opOwnerHref(op)}>{op.bot_name}</a>{/if}{#if sha} <code class="text-small">{sha}</code>{/if}
				<span class="ml-1 text-small font-normal {tone === 'fail' ? 'text-fail' : tone === 'run' ? 'text-run' : 'text-muted'}">{active(op) ? op.stage || statusText[op.status] : statusText[op.status]}</span>
			</p>
			<p class="text-small text-muted">
				<span title={fmtWhen(op.created_at_ms)}>{fmtAgo(op.created_at_ms, now)}</span>, {op.trigger === 'manual' && op.actor ? `by ${op.actor}` : triggerText[op.trigger] + (op.actor ? ` (${op.actor})` : '')}.
				{#if op.source_label}<span class="break-all">{op.source_label}.</span>{/if}
				{active(op) ? 'Running for' : 'Took'} {elapsed(op, now)}.
			</p>
			{#if op.message && !active(op)}<p class="mt-0.5 {tone === 'fail' ? 'text-fail' : 'text-ink/85'} break-words">{op.message}</p>{/if}
		</div>
		<div class="flex shrink-0 gap-1">
			{#if hasOutput}
				<button class="btn btn-sm" aria-expanded={open} onclick={() => (open = !open)}><Icon name="terminal" size={14} />{open ? 'Hide output' : 'Output'}</button>
			{/if}
			{#if actions}{@render actions()}{/if}
		</div>
	</div>
	{#if open && hasOutput}<div class="mt-2"><BuildOutput botId={op.bot_id} opId={op.id} /></div>{/if}
</li>
