<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtDuration, fmtWhen } from '$lib/args';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	type Check = { id: string; group: string; title: string; status: 'ok' | 'warn' | 'fail' | 'info'; detail: string; fix?: string };
	type Report = { generated_at_ms: number; version: string; go_version: string; uptime_ms: number; checks: Check[] };
	let report = $state<Report | null>(null);
	let error = $state('');
	let running = $state(false);

	async function run() {
		running = true;
		try {
			report = await api<Report>('GET', '/admin/diagnostics');
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The checks could not run.';
		} finally {
			running = false;
		}
	}
	onMount(run);

	const groups = $derived(report ? [...new Set(report.checks.map((c) => c.group))] : []);
	const counts = $derived({
		fail: report?.checks.filter((c) => c.status === 'fail').length ?? 0,
		warn: report?.checks.filter((c) => c.status === 'warn').length ?? 0,
		ok: report?.checks.filter((c) => c.status === 'ok').length ?? 0,
		info: report?.checks.filter((c) => c.status === 'info').length ?? 0
	});
	const tone = { ok: 'run', warn: 'warn', fail: 'fail', info: 'idle' } as const;
	const word = { ok: 'OK', warn: 'Check', fail: 'Problem', info: 'Note' } as const;
</script>

<svelte:head><title>Diagnostics · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Diagnostics</h2>
		<p class="mt-1 max-w-3xl text-muted">Read-only checks of this installation. The same report is available on the host with <code>rivetpanel doctor</code>.</p>
	</div>
	<div class="flex gap-2">
		<a class="btn" href="/api/v1/admin/diagnostics?download=1" download><Icon name="download" />Download report</a>
		<button class="btn btn-primary" onclick={run} disabled={running}><Icon name="restart" />{running ? 'Checking…' : 'Run again'}</button>
	</div>
</div>

{#if error}<Notice tone="fail" class="mt-4">{error}</Notice>{/if}
{#if !report && !error}
	<div class="mt-4"><Skeleton rows={5} /></div>
{:else if report}
	<Notice tone={counts.fail ? 'fail' : counts.warn ? 'warn' : 'success'} class="mt-4" title={counts.fail ? `${counts.fail} problem${counts.fail === 1 ? '' : 's'} need attention` : counts.warn ? `${counts.warn} thing${counts.warn === 1 ? '' : 's'} to check` : 'Everything checked out'}>
		Version {report.version}, running for {fmtDuration(report.uptime_ms)}. Checked {fmtWhen(report.generated_at_ms)}.
	</Notice>
	<p class="mt-3 flex flex-wrap gap-2 text-small" aria-label="Summary">
		{#each [['run', counts.ok, 'OK'], ['warn', counts.warn, 'to check'], ['fail', counts.fail, counts.fail === 1 ? 'problem' : 'problems'], ['idle', counts.info, counts.info === 1 ? 'note' : 'notes']] as [t, n, w] (t)}
			{#if n}<span class="pill" data-tone={t}>{n} {w}</span>{/if}
		{/each}
	</p>
	<div class="mt-2 grid gap-x-6 xl:grid-cols-2">
		{#each groups as g (g)}
			<section class="mt-5 min-w-0" aria-labelledby="g-{g}">
				<h3 id="g-{g}" class="eyebrow">{g}</h3>
				<ul class="mt-2 list-card">
					{#each report.checks.filter((c) => c.group === g) as c (c.id)}
						<li class="spine flex items-start gap-3 py-3 pr-3 pl-5" data-tone={tone[c.status]}>
							<div class="min-w-0 flex-1">
								<p class="font-medium">{c.title}</p>
								<p class="mt-0.5 text-small break-words text-ink/85">{c.detail}</p>
								{#if c.fix && c.status !== 'ok'}<p class="mt-1 text-small text-muted">{c.fix}</p>{/if}
							</div>
							<span class="pill shrink-0" data-tone={tone[c.status]}>{word[c.status]}</span>
						</li>
					{/each}
				</ul>
			</section>
		{/each}
	</div>
{/if}
