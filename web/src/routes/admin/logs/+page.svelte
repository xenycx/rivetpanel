<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { can } from '$lib/session.svelte';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import LogDays from '$lib/components/LogDays.svelte';

	type Metrics = { telemetry_hours: number; usage_5m_days: number; usage_1h_days: number; usage_1d_days: number; status_days: number };
	type Settings = {
		archive_time: string;
		timezone: string;
		retention_days: number;
		max_archive_mb: number;
		max_day_mb: number;
		capture_bots: boolean;
		panel_file: boolean;
		capture_minutes: number;
		metrics: Metrics;
	};
	type Run = {
		started_at_ms: number;
		finished_at_ms: number;
		trigger: string;
		ok: boolean;
		captured_lines: number;
		archived: number;
		archived_bytes: number;
		deleted: number;
		deleted_bytes: number;
		errors?: string[];
	};
	type View = {
		settings: Settings;
		defaults: Settings;
		bounds: Record<string, { min: number; max: number }>;
		usage: { live_bytes: number; live_files: number; archived_bytes: number; archived_files: number; oldest_archive?: string; operation_log_bytes: number; operation_log_files: number };
		last_run: Run | null;
		running: boolean;
		next_run_at_ms: number;
		current_day: string;
		capture_available: boolean;
		folder: string;
		archive_folder: string;
	};

	const manage = $derived(can('settings.manage'));
	const viewSystem = $derived(can('system.view'));
	let v = $state<View | null>(null);
	let f = $state<Settings | null>(null); // the form
	let loadError = $state('');
	let saveError = $state('');
	let saving = $state(false);
	let running = $state(false);
	let refreshDays = $state(0);

	const zones: string[] = (() => {
		try {
			return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf('timeZone');
		} catch {
			return [];
		}
	})();
	const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
	const zoneOptions = $derived.by(() => {
		const list = ['UTC', ...zones.filter((z) => z !== 'UTC')];
		if (f?.timezone && !list.includes(f.timezone)) list.unshift(f.timezone);
		return list;
	});

	function fill(x: View) {
		v = x;
		f = structuredClone(x.settings);
	}
	async function load() {
		try {
			fill(await api<View>('GET', '/admin/log-archive'));
			loadError = '';
		} catch (e) {
			loadError = e instanceof ApiError ? e.message : 'The log settings could not be loaded.';
		}
	}
	onMount(() => {
		if (manage || viewSystem) void load();
	});
	// A save starts a run in the background; follow it until it finishes.
	$effect(() => {
		if (!v?.running) return;
		const t = setTimeout(async () => {
			await load();
			if (!v?.running) refreshDays++;
		}, 1500);
		return () => clearTimeout(t);
	});

	// Field checks mirror the server's (it checks again).
	const numeric: [keyof Settings | `metrics.${keyof Metrics}`, string][] = [
		['retention_days', 'Keep archives'],
		['max_archive_mb', 'Log size limit'],
		['max_day_mb', 'Daily file limit'],
		['capture_minutes', 'Copy interval'],
		['metrics.telemetry_hours', 'Resource graphs'],
		['metrics.usage_5m_days', '5-minute detail'],
		['metrics.usage_1h_days', 'Hourly detail'],
		['metrics.usage_1d_days', 'Daily totals'],
		['metrics.status_days', 'Status page history']
	];
	function get(s: Settings, k: string): number {
		return k.startsWith('metrics.') ? s.metrics[k.slice(8) as keyof Metrics] : (s[k as keyof Settings] as number);
	}
	const errors = $derived.by(() => {
		const out: Record<string, string> = {};
		if (!f || !v) return out;
		if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(f.archive_time ?? '')) out.archive_time = 'Choose a time between 00:00 and 23:59.';
		for (const [k] of numeric) {
			const n = get(f, k);
			const b = v.bounds[k];
			if (typeof n !== 'number' || !Number.isInteger(n)) out[k] = 'Enter a whole number.';
			else if (b && (n < b.min || n > b.max)) out[k] = `Must be between ${b.min.toLocaleString()} and ${b.max.toLocaleString()}.`;
		}
		const m = f.metrics;
		if (!out['metrics.usage_1h_days'] && !out['metrics.usage_5m_days'] && m.usage_5m_days > m.usage_1h_days) out['metrics.usage_1h_days'] = 'Hourly detail must be kept at least as long as 5-minute detail.';
		if (!out['metrics.usage_1d_days'] && !out['metrics.usage_1h_days'] && m.usage_1h_days > m.usage_1d_days) out['metrics.usage_1d_days'] = 'Daily totals must be kept at least as long as hourly detail.';
		return out;
	});
	let serverField = $state<{ key: string; msg: string } | null>(null);
	const fieldError = (k: string) => errors[k] ?? (serverField?.key === k ? serverField.msg : '');
	const dirty = $derived(!!f && !!v && JSON.stringify(f) !== JSON.stringify(v.settings));

	function changes(): Record<string, unknown> {
		if (!f || !v) return {};
		const body: Record<string, unknown> = {};
		const metrics: Record<string, number> = {};
		for (const k of Object.keys(f) as (keyof Settings)[]) {
			if (k === 'metrics') continue;
			if (f[k] !== v.settings[k]) body[k] = f[k];
		}
		for (const k of Object.keys(f.metrics) as (keyof Metrics)[]) if (f.metrics[k] !== v.settings.metrics[k]) metrics[k] = f.metrics[k];
		if (Object.keys(metrics).length) body.metrics = metrics;
		return body;
	}
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!manage || Object.keys(errors).length) return;
		saving = true;
		saveError = '';
		serverField = null;
		try {
			fill(await api<View>('PUT', '/admin/log-archive', changes()));
			toast('Log and retention settings saved', 'success');
		} catch (err) {
			const msg = err instanceof ApiError ? err.message : 'The settings could not be saved.';
			const key = msg.match(/^(metrics\.\w+|retention_days|max_archive_mb|max_day_mb|capture_minutes)\b/)?.[1] ?? (/archive time/.test(msg) ? 'archive_time' : /time zone/.test(msg) ? 'timezone' : /analytics retention/.test(msg) ? 'metrics.usage_1h_days' : '');
			if (key) serverField = { key, msg: msg.charAt(0).toUpperCase() + msg.slice(1) + '.' };
			else saveError = msg.charAt(0).toUpperCase() + msg.slice(1);
		} finally {
			saving = false;
		}
	}
	function reset() {
		if (v) f = structuredClone(v.settings);
		serverField = null;
		saveError = '';
	}

	async function runNow() {
		const ok = await confirmDialog({
			title: 'Run the log archive now?',
			body: 'Copies the newest console output, compresses every ended day into the archive folder, and then deletes archives beyond the retention and size limit. The current day stays live.',
			details: v ? [['Keep archives', `${v.settings.retention_days} days`], ['Size limit', v.settings.max_archive_mb ? `${v.settings.max_archive_mb.toLocaleString()} MB` : 'none']] : undefined,
			confirmLabel: 'Run now'
		});
		if (!ok) return;
		running = true;
		try {
			const r = await api<Run>('POST', '/admin/log-archive/run');
			if (r.ok) toast(`Archive finished: ${r.archived} file${r.archived === 1 ? '' : 's'} archived, ${r.deleted} deleted`, 'success');
			else toast(`Archive finished with ${r.errors?.length ?? 0} error${r.errors?.length === 1 ? '' : 's'}; see Last run`, 'warn');
			await load();
			refreshDays++;
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The archive could not run.', 'fail');
		} finally {
			running = false;
		}
	}

	const days = (h: number) => (h % 24 === 0 ? `${h / 24} day${h === 24 ? '' : 's'}` : `${(h / 24).toFixed(1)} days`);
	const trigger: Record<string, string> = { schedule: 'on schedule', startup: 'at startup', manual: 'run by hand', settings: 'after a settings change' };
	const tzLabel = (z: string) => z || `Server local time`;
</script>

<svelte:head><title>Logs and retention · RivetPanel</title></svelte:head>

<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Logs and retention</h2>
		<p class="mt-1 max-w-3xl text-muted">Console output of every bot and game server and the panel's own log are written to one file per day. Once a day has ended it is compressed into the archive folder and kept for the time set here. Graph history is kept on its own schedule below.</p>
	</div>
	{#if manage && v}<button class="btn btn-primary" onclick={runNow} disabled={running || v.running}><Icon name="archive" />{running || v.running ? 'Archiving…' : 'Run archival now'}</button>{/if}
</div>

{#if loadError}<Notice tone="fail" class="mt-4" live>{loadError}</Notice>{/if}
{#if !manage && viewSystem && v}<Notice class="mt-4" title="Read-only">You can see these settings; changing them needs the panel settings permission.</Notice>{/if}

{#if !v && !loadError}
	<div class="mt-4"><Skeleton rows={5} label="Loading log settings" /></div>
{:else if v && f}
	<!-- Disk use and the last job. -->
	<dl class="mt-5 grid grid-cols-2 gap-3 xl:grid-cols-4">
		<div class="stat"><dt class="eyebrow">Live day files</dt><dd class="mt-1 font-mono text-section font-semibold">{fmtBytes(v.usage.live_bytes)}<span class="ml-2 text-small font-normal text-muted">{v.usage.live_files} file{v.usage.live_files === 1 ? '' : 's'}</span></dd></div>
		<div class="stat"><dt class="eyebrow">Archived</dt><dd class="mt-1 font-mono text-section font-semibold">{fmtBytes(v.usage.archived_bytes)}<span class="ml-2 text-small font-normal text-muted">{v.usage.archived_files} file{v.usage.archived_files === 1 ? '' : 's'}{v.settings.max_archive_mb ? ` of ${v.settings.max_archive_mb.toLocaleString()} MB` : ''}</span></dd></div>
		<div class="stat"><dt class="eyebrow">Oldest archive</dt><dd class="mt-1 font-mono text-section font-semibold">{v.usage.oldest_archive || '—'}</dd></div>
		<div class="stat"><dt class="eyebrow">Build and backup output</dt><dd class="mt-1 font-mono text-section font-semibold">{fmtBytes(v.usage.operation_log_bytes)}<span class="ml-2 text-small font-normal text-muted">{v.usage.operation_log_files} file{v.usage.operation_log_files === 1 ? '' : 's'}, not archived</span></dd></div>
	</dl>

	<div class="mt-3 card p-4 sm:p-5">
		<div class="flex flex-wrap items-center gap-2">
			<Icon name="history" size={18} class="text-muted" />
			<h3 class="text-title font-semibold">Last run</h3>
			{#if v.running}<span class="pill" data-tone="warn">Running</span>{:else if v.last_run}<span class="pill" data-tone={v.last_run.ok ? 'run' : 'fail'}>{v.last_run.ok ? 'Succeeded' : 'Finished with errors'}</span>{/if}
			<span class="flex-1"></span>
			<span class="text-small text-muted">Today is <span class="font-mono">{v.current_day}</span>; next run {fmtWhen(v.next_run_at_ms)}</span>
		</div>
		{#if v.last_run}
			{@const r = v.last_run}
			<p class="mt-2 text-small">
				{fmtWhen(r.finished_at_ms || r.started_at_ms)} ({fmtAgo(r.finished_at_ms || r.started_at_ms)}), {trigger[r.trigger] ?? r.trigger}, took {((r.finished_at_ms - r.started_at_ms) / 1000).toFixed(1)} s.
				Copied {r.captured_lines.toLocaleString()} console line{r.captured_lines === 1 ? '' : 's'}, archived {r.archived} file{r.archived === 1 ? '' : 's'} ({fmtBytes(r.archived_bytes)}), deleted {r.deleted} ({fmtBytes(r.deleted_bytes)}).
			</p>
			{#if r.errors?.length}
				<ul class="mt-2 space-y-1 text-small text-fail">
					{#each r.errors as e, i (i)}<li class="break-words">{e}</li>{/each}
				</ul>
			{/if}
		{:else}
			<p class="mt-2 text-small text-muted">The job has not run since these settings were introduced.</p>
		{/if}
		<p class="mt-2 text-small break-all text-muted">Live files: <code>{v.folder}</code> · archive: <code>{v.archive_folder}</code></p>
	</div>

	<form class="mt-6" onsubmit={save}>
		<fieldset disabled={!manage} class="min-w-0">
			<SettingsSection title="Daily archive" description="When a day ends and how long its archive is kept.">
				<div class="grid gap-4 sm:grid-cols-2">
					<label class="block">
						<span class="label">Archive time</span>
						<input class="field font-mono" type="time" step="60" bind:value={f.archive_time} aria-invalid={!!fieldError('archive_time')} />
						<span class="help {fieldError('archive_time') ? '!text-fail' : ''}">{fieldError('archive_time') || `A day runs from ${f.archive_time || '00:00'} to ${f.archive_time || '00:00'} the next day and is named by the date it starts on.`}</span>
					</label>
					<label class="block">
						<span class="label">Time zone</span>
						<select class="field" bind:value={f.timezone} aria-invalid={!!fieldError('timezone')}>
							<option value="">Server local time</option>
							{#each zoneOptions as z (z)}<option value={z}>{z}</option>{/each}
						</select>
						<span class="help {fieldError('timezone') ? '!text-fail' : ''}">{fieldError('timezone') || `Times in the files use it too. ${tzLabel(f.timezone) === browserZone ? 'Same as this browser.' : `This browser uses ${browserZone}.`}`}</span>
					</label>
					<label class="block">
						<span class="label">Keep archives for</span>
						<span class="flex items-center gap-2"><input class="field max-w-36 font-mono" type="number" min={v.bounds.retention_days?.min} max={v.bounds.retention_days?.max} step="1" bind:value={f.retention_days} aria-invalid={!!fieldError('retention_days')} /><span class="text-small text-muted">days</span></span>
						<span class="help {fieldError('retention_days') ? '!text-fail' : ''}">{fieldError('retention_days') || `Older archives are deleted. ${v.bounds.retention_days?.min}–${v.bounds.retention_days?.max.toLocaleString()} days; default ${v.defaults.retention_days}.`}</span>
					</label>
					<label class="block">
						<span class="label">Log size limit</span>
						<span class="flex items-center gap-2"><input class="field max-w-36 font-mono" type="number" min="0" max={v.bounds.max_archive_mb?.max} step="1" bind:value={f.max_archive_mb} aria-invalid={!!fieldError('max_archive_mb')} /><span class="text-small text-muted">MB</span></span>
						<span class="help {fieldError('max_archive_mb') ? '!text-fail' : ''}">{fieldError('max_archive_mb') || (f.max_archive_mb ? `When archives and today's live files together exceed ${(f.max_archive_mb / 1024).toFixed(f.max_archive_mb >= 10240 ? 0 : 1)} GB, the oldest archives are deleted first.` : '0 means no limit: only the retention days apply.')}</span>
					</label>
					<label class="block">
						<span class="label">Daily file limit</span>
						<span class="flex items-center gap-2"><input class="field max-w-36 font-mono" type="number" min={v.bounds.max_day_mb?.min} max={v.bounds.max_day_mb?.max} step="1" bind:value={f.max_day_mb} aria-invalid={!!fieldError('max_day_mb')} /><span class="text-small text-muted">MB</span></span>
						<span class="help {fieldError('max_day_mb') ? '!text-fail' : ''}">{fieldError('max_day_mb') || `Per server and for the panel log, per day; default ${v.defaults.max_day_mb} MB. When a day's file reaches it, one "log capped" line is written and the rest of that day is not stored. Nothing is written while free disk space is below the panel's minimum (RIVET_MIN_FREE_DISK_BYTES).`}</span>
					</label>
				</div>
			</SettingsSection>

			<SettingsSection title="What is written" description="Day files are created with owner-only permissions next to the database and are not part of rivetpanel backup.">
				<div class="space-y-4">
					<Switch label="Keep console output of bots and game servers" bind:checked={f.capture_bots}>
						Copied from Docker, or from the node's agent for remote servers, and stored on the panel.{#if !v.capture_available} This panel has no container runtime and no nodes, so there is nothing to copy right now.{/if}
					</Switch>
					<label class="block pl-12">
						<span class="label">Copy new output every</span>
						<span class="flex items-center gap-2"><input class="field max-w-28 font-mono" type="number" min={v.bounds.capture_minutes?.min} max={v.bounds.capture_minutes?.max} step="1" bind:value={f.capture_minutes} disabled={!f.capture_bots} aria-invalid={!!fieldError('capture_minutes')} /><span class="text-small text-muted">minutes</span></span>
						<span class="help {fieldError('capture_minutes') ? '!text-fail' : ''}">{fieldError('capture_minutes') || `${v.bounds.capture_minutes?.min}–${v.bounds.capture_minutes?.max}. Lower it for very chatty servers: Docker keeps only about 30 MB per container, and lines it drops before a copy are lost.`}</span>
					</label>
					<Switch label="Write the panel's own log to day files" bind:checked={f.panel_file}>The same lines as the panel's standard error (journalctl or docker logs); listed under Panel log days below.</Switch>
				</div>
			</SettingsSection>

			<SettingsSection title="Graph history" description="How long the data behind charts is kept. Older data is deleted by the hourly cleanup.">
				<div class="grid gap-4 sm:grid-cols-2">
					{#each [
						{ k: 'telemetry_hours', unit: 'hours', help: (n: number) => `CPU, memory and network graphs of the host, nodes and each bot keep 30-second samples for ${days(n)}.` },
						{ k: 'usage_5m_days', unit: 'days', help: (n: number) => `Analytics keeps 5-minute detail for ${n} day${n === 1 ? '' : 's'}.` },
						{ k: 'usage_1h_days', unit: 'days', help: (n: number) => `Then hourly detail for ${n} days.` },
						{ k: 'usage_1d_days', unit: 'days', help: (n: number) => `Then daily totals for ${n} days (about ${(n / 365).toFixed(1)} years).` },
						{ k: 'status_days', unit: 'days', help: (n: number) => `Status page uptime keeps daily samples for ${n} days; the public page shows the last 90.` }
					] as row (row.k)}
						{@const key = `metrics.${row.k}`}
						{@const b = v.bounds[key]}
						{@const label = numeric.find(([n]) => n === key)?.[1]}
						<label class="block">
							<span class="label">{label}</span>
							<span class="flex items-center gap-2"><input class="field max-w-36 font-mono" type="number" min={b?.min} max={b?.max} step="1" bind:value={f.metrics[row.k as keyof Metrics]} aria-invalid={!!fieldError(key)} /><span class="text-small text-muted">{row.unit}</span></span>
							<span class="help {fieldError(key) ? '!text-fail' : ''}">{fieldError(key) || `${row.help(f.metrics[row.k as keyof Metrics])} ${b ? `${b.min.toLocaleString()}–${b.max.toLocaleString()}; ` : ''}default ${v.defaults.metrics[row.k as keyof Metrics].toLocaleString()}.`}</span>
						</label>
					{/each}
				</div>
				<p class="mt-3 text-small text-muted">Analytics detail cannot shrink with coarser resolution: 5-minute ≤ hourly ≤ daily. A value saved here overrides <code>RIVET_TELEMETRY_RETENTION</code>.</p>
			</SettingsSection>
		</fieldset>

		{#if saveError}<Notice tone="fail" class="mt-2" live>{saveError}</Notice>{/if}
		{#if manage}
			<div class="sticky bottom-4 mt-2 flex flex-wrap justify-end gap-2">
				{#if dirty}<button type="button" class="btn shadow-overlay" onclick={reset}>Discard</button>{/if}
				<button class="btn btn-primary shadow-overlay" disabled={saving || !dirty || Object.keys(errors).length > 0}>{saving ? 'Saving…' : 'Save and apply'}</button>
			</div>
		{/if}
	</form>
{/if}

{#if viewSystem}
	<div class="mt-10 border-t border-rule-soft pt-8">
		<LogDays path="/admin/logs/days" filename="rivetpanel" label="Panel log days" refresh={refreshDays} empty="No day files yet. They appear once the panel log is written to files." />
		<p class="mt-2 text-small text-muted">Archives are gzip files that <code>zcat</code> and browsers read. The in-memory viewer is under <a class="link" href="/admin/host?tab=logs">Host → Panel logs</a>; it redacts secrets, these files do not beyond what the panel logs.</p>
	</div>
{/if}
