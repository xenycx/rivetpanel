<script lang="ts">
	import Icon from '$lib/components/ui/Icon.svelte';
	import { refreshOptions, type TimeRange } from './timeRange.svelte';

	// Time range toolbar: presets, the absolute span after a drag-zoom (with
	// zoom out and a small from/to picker), refresh interval and Live.
	let { range, live = true }: { range: TimeRange; live?: boolean } = $props();

	let picking = $state(false);
	let fromIn = $state('');
	let toIn = $state('');

	const local = (t: number) => {
		const d = new Date(t - new Date(t).getTimezoneOffset() * 60_000);
		return d.toISOString().slice(0, 16);
	};
	function openPicker() {
		const { start, end } = range.resolve();
		fromIn = local(start);
		toIn = local(end);
		picking = !picking;
	}
	function apply(e: SubmitEvent) {
		e.preventDefault();
		const s = new Date(fromIn).getTime();
		const t = new Date(toIn).getTime();
		if (s < t) {
			range.setAbsolute(s, t);
			picking = false;
		}
	}
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && picking && (picking = false)} />

<div class="flex flex-wrap items-center gap-2">
	<div class="seg" role="group" aria-label="Time range">
		{#each range.presets as p (p.id)}
			<button class="seg-item font-mono tabular-nums" aria-pressed={range.preset?.id === p.id} onclick={() => range.setPreset(p.id)}>{p.label}</button>
		{/each}
	</div>
	<div class="relative">
		<button class="btn btn-sm" aria-expanded={picking} onclick={openPicker} title="Pick a start and end">
			<Icon name="clock" size={14} /><span class="font-mono text-small tabular-nums">{range.relative ? 'Custom' : range.label}</span>
		</button>
		{#if picking}
			<form class="absolute right-0 z-30 mt-1 grid w-72 gap-2 rounded-overlay border border-rule bg-raised p-3 shadow-overlay" onsubmit={apply}>
				<label class="grid gap-1 text-small"><span class="text-muted">From</span><input class="field font-mono" type="datetime-local" bind:value={fromIn} required /></label>
				<label class="grid gap-1 text-small"><span class="text-muted">To</span><input class="field font-mono" type="datetime-local" bind:value={toIn} required /></label>
				<div class="mt-1 flex justify-end gap-2">
					<button type="button" class="btn btn-sm btn-quiet" onclick={() => (picking = false)}>Cancel</button>
					<button class="btn btn-sm btn-primary">Apply</button>
				</div>
			</form>
		{/if}
	</div>
	<button class="btn btn-sm btn-icon" onclick={() => range.zoomOut()} aria-label="Zoom out" title="Zoom out (double-click a chart to undo a zoom)"><Icon name="minimize" size={14} /></button>
	<label class="flex items-center gap-1.5 text-small text-muted">
		<Icon name="restart" size={14} /><span class="sr-only">Refresh every</span>
		<select class="field h-8 py-0 pr-7 font-mono text-small tabular-nums" value={range.refreshMs} onchange={(e) => (range.refreshMs = Number(e.currentTarget.value))} disabled={range.live || !range.relative} title={range.relative ? 'Refresh interval' : 'A fixed span does not refresh'}>
			{#each refreshOptions as o (o.ms)}<option value={o.ms}>{o.label}</option>{/each}
		</select>
	</label>
	{#if live}
		<button class="btn btn-sm" aria-pressed={range.live} disabled={!range.canLive} onclick={() => range.setLive(!range.live)} title={range.canLive ? 'Stream a sample every few seconds' : 'Live is available for ranges up to 1 hour'}>
			<span class="inline-block size-2 rounded-pill {range.live ? 'bg-run motion-safe:animate-pulse' : 'bg-muted/50'}"></span>Live
		</button>
	{/if}
</div>
