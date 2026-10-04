<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Bot, type Schedule, type ScheduleAction, type ScheduleTask, type TaskAction } from '$lib/api/types';
	import { fmtAgo, fmtWhen } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu, { type MenuItem } from '$lib/components/ui/Menu.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot }: { bot: Bot } = $props();

	let list = $state<Schedule[] | null>(null);
	let error = $state('');
	let now = $state(Date.now());

	const path = $derived(`/bots/${bot.id}/schedules`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	const actions: { id: ScheduleAction; label: string; hint: string; perm: number; feature?: 'backups' | 'deploy' | 'runner' }[] = [
		{ id: 'backup', label: 'Back up', hint: 'Files and sealed environment, kept with the scheduled backups', perm: Perm.files, feature: 'backups' },
		{ id: 'restart', label: 'Restart', hint: 'Only if the bot is running', perm: Perm.power, feature: 'runner' },
		{ id: 'start', label: 'Start', hint: 'Starts the bot, or retries it after it gave up', perm: Perm.power, feature: 'runner' },
		{ id: 'stop', label: 'Stop', hint: 'Stops the bot', perm: Perm.power, feature: 'runner' },
		{ id: 'deploy', label: 'Deploy from GitHub', hint: 'Deploys the newest commit of the linked branch', perm: Perm.files, feature: 'deploy' },
		{ id: 'chain', label: 'Task chain', hint: 'Several steps in order, with waits: console commands, power actions and backups', perm: Perm.power, feature: 'runner' }
	];
	const isGame = $derived(bot.kind === 'game');
	const offered = $derived(actions.filter((a) => can(bot, a.perm) && (!a.feature || session.features[a.feature]) && !(isGame && a.id === 'deploy')));
	const taskLabels: Record<TaskAction, string> = { command: 'Send command', start: 'Start', stop: 'Stop', restart: 'Restart', kill: 'Kill', backup: 'Back up' };
	let tasks = $state<ScheduleTask[]>([]);
	const presets: { label: string; tasks: ScheduleTask[] }[] = [
		{
			label: 'Warned restart',
			tasks: [
				{ action: 'command', payload: 'say The server restarts in 5 minutes', delay_seconds: 0, continue_on_failure: true },
				{ action: 'command', payload: 'say The server restarts in 1 minute', delay_seconds: 240, continue_on_failure: true },
				{ action: 'command', payload: 'save-all', delay_seconds: 50, continue_on_failure: true },
				{ action: 'restart', payload: '', delay_seconds: 10, continue_on_failure: false }
			]
		},
		{
			label: 'Save, then back up',
			tasks: [
				{ action: 'command', payload: 'save-all', delay_seconds: 0, continue_on_failure: true },
				{ action: 'backup', payload: '', delay_seconds: 15, continue_on_failure: false }
			]
		}
	];
	const chainDelay = $derived(tasks.reduce((n, t) => n + (Number(t.delay_seconds) || 0), 0));
	function addTask() {
		tasks = [...tasks, { action: 'command', payload: '', delay_seconds: 0, continue_on_failure: false }];
	}
	function moveTask(i: number, d: number) {
		const j = i + d;
		if (j < 0 || j >= tasks.length) return;
		const next = [...tasks];
		[next[i], next[j]] = [next[j], next[i]];
		tasks = next;
	}
	const actionLabel = (a: ScheduleAction) => actions.find((x) => x.id === a)?.label ?? a;
	const scheduleTitle = (s: Schedule) =>
		s.action === 'chain' ? `${s.tasks.length} ${s.tasks.length === 1 ? 'task' : 'tasks'}: ${s.tasks.map((t) => (t.action === 'command' ? `“${t.payload}”` : taskLabels[t.action].toLowerCase())).join(' → ')}` : actionLabel(s.action);

	async function load() {
		try {
			list = (await api<{ schedules: Schedule[] }>('GET', path)).schedules;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible' && list?.some((s) => s.next_run_at_ms && s.next_run_at_ms <= Date.now())) load();
		}, 15000);
		return () => clearInterval(t);
	});

	// ---- editor -------------------------------------------------------------
	type Mode = 'daily' | 'weekly' | 'hourly' | 'custom';
	const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];
	const zones = (() => {
		try {
			return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf('timeZone');
		} catch {
			return ['UTC'];
		}
	})();
	const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

	let open = $state(false);
	let editing = $state<Schedule | null>(null);
	let action = $state<ScheduleAction>('backup');
	let mode = $state<Mode>('daily');
	let time = $state('03:00');
	let weekday = $state(0);
	let everyHours = $state(6);
	let custom = $state('0 3 * * *');
	let tz = $state(localZone);
	let saving = $state(false);
	let preview = $state<number[]>([]);
	let previewError = $state('');

	function specFor(): string {
		const [h, m] = time.split(':').map((x) => parseInt(x, 10) || 0);
		switch (mode) {
			case 'daily':
				return `${m} ${h} * * *`;
			case 'weekly':
				return `${m} ${h} * * ${weekday}`;
			case 'hourly':
				return `0 */${everyHours} * * *`;
			default:
				return custom.trim();
		}
	}
	const spec = $derived(specFor());

	// Recognize the presets again when editing.
	function load2editor(s: Schedule) {
		const f = s.spec.split(' ');
		const hm = (h: string, m: string) => `${h.padStart(2, '0')}:${m.padStart(2, '0')}`;
		if (f.length === 5 && /^\d+$/.test(f[0]) && /^\d+$/.test(f[1]) && f[2] === '*' && f[3] === '*' && f[4] === '*') {
			mode = 'daily';
			time = hm(f[1], f[0]);
		} else if (f.length === 5 && /^\d+$/.test(f[0]) && /^\d+$/.test(f[1]) && f[2] === '*' && f[3] === '*' && /^[0-6]$/.test(f[4])) {
			mode = 'weekly';
			time = hm(f[1], f[0]);
			weekday = +f[4];
		} else if (f.length === 5 && f[0] === '0' && /^\*\/\d+$/.test(f[1]) && f.slice(2).every((x) => x === '*')) {
			mode = 'hourly';
			everyHours = +f[1].slice(2);
		} else {
			mode = 'custom';
			custom = s.spec;
		}
	}

	function describeSpec(s: string): string {
		const f = s.split(' ');
		if (f.length !== 5) return s;
		const t = /^\d+$/.test(f[0]) && /^\d+$/.test(f[1]) ? `${f[1].padStart(2, '0')}:${f[0].padStart(2, '0')}` : '';
		if (t && f[2] === '*' && f[3] === '*' && f[4] === '*') return `Every day at ${t}`;
		if (t && f[2] === '*' && f[3] === '*' && /^[0-6]$/.test(f[4])) return `Every ${days[+f[4]]} at ${t}`;
		if (t && f[2] === '*' && f[3] === '*' && f[4] === '1-5') return `Weekdays at ${t}`;
		if (f[0] === '0' && /^\*\/\d+$/.test(f[1]) && f.slice(2).every((x) => x === '*')) return `Every ${f[1].slice(2)} hours`;
		if (f[0] === '0' && f.slice(1).every((x) => x === '*')) return 'Every hour';
		if (t && /^\d+$/.test(f[2]) && f[3] === '*' && f[4] === '*') return `Monthly on day ${f[2]} at ${t}`;
		return s;
	}

	function openNew() {
		editing = null;
		action = offered[0]?.id ?? 'backup';
		mode = 'daily';
		time = '03:00';
		weekday = 0;
		everyHours = 6;
		custom = '0 3 * * *';
		tz = localZone;
		tasks = [];
		open = true;
	}
	function openEdit(s: Schedule) {
		editing = s;
		action = s.action;
		tasks = s.tasks.map((t) => ({ ...t }));
		tz = s.timezone;
		load2editor(s);
		open = true;
	}

	let seq = 0;
	$effect(() => {
		if (!open) return;
		const q = `spec=${encodeURIComponent(spec)}&timezone=${encodeURIComponent(tz)}`;
		const my = ++seq;
		const t = setTimeout(async () => {
			try {
				const r = await api<{ upcoming: number[] }>('GET', `/schedules/preview?${q}`);
				if (my === seq) {
					preview = r.upcoming;
					previewError = '';
				}
			} catch (e) {
				if (my === seq) {
					preview = [];
					previewError = msg(e);
				}
			}
		}, 250);
		return () => clearTimeout(t);
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		try {
			const body: Record<string, unknown> = { action, spec, timezone: tz };
			if (action === 'chain') body.tasks = tasks.map((t) => ({ ...t, delay_seconds: Number(t.delay_seconds) || 0 }));
			if (editing) await api('PATCH', `${path}/${editing.id}`, body);
			else await api('POST', path, { ...body, enabled: true });
			open = false;
			toast(editing ? 'Schedule saved' : 'Schedule added', 'success');
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		} finally {
			saving = false;
		}
	}

	async function toggle(s: Schedule) {
		try {
			await api('PATCH', `${path}/${s.id}`, { enabled: !s.enabled });
			toast(s.enabled ? 'Schedule paused' : 'Schedule resumed');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function runNow(s: Schedule) {
		try {
			const r = await api<{ status: string; message: string }>('POST', `${path}/${s.id}/run`);
			toast(r.message, r.status === 'ok' ? 'success' : 'fail');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(s: Schedule) {
		const ok = await confirmDialog({
			title: 'Delete this schedule?',
			body: 'Nothing that already ran is undone.',
			details: [
				['Action', actionLabel(s.action)],
				['When', `${describeSpec(s.spec)} (${s.timezone})`]
			],
			confirmLabel: 'Delete schedule',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `${path}/${s.id}`);
			toast('Schedule deleted');
			await load();
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	function menu(s: Schedule): MenuItem[] {
		if (!s.can_edit) return [];
		return [
			{ label: 'Run now', onselect: () => runNow(s), hint: 'Does not change the next run' },
			{ label: 'Edit', onselect: () => openEdit(s) },
			{ label: s.enabled ? 'Pause' : 'Resume', onselect: () => toggle(s) },
			'separator',
			{ label: 'Delete', danger: true, onselect: () => remove(s) }
		];
	}
	const statusText: Record<string, string> = { ok: 'Ran', failed: 'Failed', skipped: 'Skipped', missed: 'Missed', denied: 'Paused: no permission' };
	const tone = (s: Schedule) =>
		!s.enabled ? 'idle' : s.last_status === 'failed' || s.last_status === 'denied' ? 'fail' : s.last_status === 'missed' || s.last_status === 'skipped' ? 'warn' : 'run';
</script>

<div class="flex flex-wrap items-center gap-2">
	<p class="max-w-prose flex-1 text-muted">Run backups, restarts, starts, stops{isGame ? ', console commands' : ' or deployments'} on a timetable, alone or as a task chain. Each schedule runs with its creator's permissions at the time it runs.</p>
	{#if offered.length}<button class="btn btn-primary" onclick={openNew}><Icon name="plus" />Add schedule</button>{/if}
</div>

{#if error}<Notice tone="fail" class="mt-3">{error}</Notice>{/if}

<div class="mt-4">
	{#if list === null && !error}
		<Skeleton rows={2} label="Loading schedules" />
	{:else if list}
		<ul class="list-card">
			{#each list as s (s.id)}
				<li class="spine flex flex-wrap items-center gap-x-4 gap-y-2 py-3 pr-3 pl-5" data-tone={tone(s)}>
					<div class="min-w-0 flex-1 basis-64">
						<p class="font-medium">
							{scheduleTitle(s)} <span class="font-normal text-muted">·</span> {describeSpec(s.spec)}
							{#if !s.enabled}<span class="ml-1 rounded-control border border-rule px-1.5 py-px text-small font-normal text-muted">Paused</span>{/if}
						</p>
						<p class="text-small text-muted">
							<span class="font-mono">{s.spec}</span>, {s.timezone}. Created by {s.owner_email || 'a removed user'}.
						</p>
						<p class="text-small">
							{#if s.enabled && s.next_run_at_ms}<span>Next {fmtWhen(s.next_run_at_ms)}</span>{/if}
							{#if s.last_run_at_ms}
								<span class={s.last_status === 'failed' || s.last_status === 'denied' ? 'text-fail' : s.last_status === 'ok' ? 'text-run' : 'text-warn'}>
									{s.enabled && s.next_run_at_ms ? ' · ' : ''}{statusText[s.last_status ?? ''] ?? 'Ran'} {fmtAgo(s.last_run_at_ms, now)}{s.last_message ? `: ${s.last_message}` : ''}
								</span>
							{:else if s.enabled}<span class="text-muted"> · Not run yet</span>{/if}
						</p>
					</div>
					<div class="flex items-center gap-1">
						{#if s.can_edit}
							<button class="btn btn-sm" onclick={() => toggle(s)}>{s.enabled ? 'Pause' : 'Resume'}</button>
							<Menu label="More actions for this schedule" items={menu(s)} />
						{/if}
					</div>
				</li>
			{:else}
				<li class="flex items-center gap-3 px-4 py-6 text-muted"><Icon name="clock" class="shrink-0" />No schedules yet. A nightly backup is a good first one.</li>
			{/each}
		</ul>
	{/if}
</div>
<p class="mt-4 max-w-prose text-small text-muted">If the panel is offline when a run is due, the run is recorded as missed and not repeated later. A schedule pauses itself when its creator loses the permission or their account is disabled. Per-bot backup schedules replace the panel-wide backup interval for this bot.</p>

<Dialog bind:open title={editing ? 'Edit schedule' : 'Add a schedule'} size="md">
	<form id="sched-form" class="grid gap-4" onsubmit={save}>
		<fieldset>
			<legend class="label">Action</legend>
			<div class="grid gap-1.5 sm:grid-cols-2">
				{#each offered as a (a.id)}
					<label class="flex cursor-pointer items-start gap-2.5 rounded-control border px-3 py-2 {action === a.id ? 'border-action bg-action/5' : 'border-rule'}">
						<input type="radio" class="mt-0.5" name="action" value={a.id} bind:group={action} />
						<span>{a.label}<span class="help">{a.hint}</span></span>
					</label>
				{/each}
			</div>
		</fieldset>

		{#if action === 'chain'}
			<fieldset class="grid gap-2">
				<legend class="label">Tasks, in order</legend>
				{#if tasks.length === 0}
					<div class="flex flex-wrap items-center gap-2 text-small text-muted">
						Start from
						{#each presets as p (p.label)}<button type="button" class="btn btn-sm" onclick={() => (tasks = p.tasks.map((t) => ({ ...t })))}>{p.label}</button>{/each}
						or add tasks one by one.
					</div>
				{/if}
				{#each tasks as t, i (i)}
					<div class="grid gap-2 rounded-control border border-rule-soft p-2.5 sm:grid-cols-[auto_9rem_1fr_7rem_auto] sm:items-center">
						<span class="text-small font-medium text-muted">{i + 1}.</span>
						<select class="field" bind:value={t.action} aria-label="Task {i + 1} action">
							{#each Object.entries(taskLabels) as [k, l] (k)}<option value={k}>{l}</option>{/each}
						</select>
						{#if t.action === 'command'}
							<input class="field font-mono" bind:value={t.payload} placeholder="say Hello" maxlength="1000" required aria-label="Task {i + 1} command" />
						{:else}<span class="text-small text-muted">{t.action === 'backup' ? 'Files and sealed variables' : 'Power action'}</span>{/if}
						<label class="flex items-center gap-1.5 text-small"><span class="text-muted">after</span><input class="field w-20" type="number" min="0" max="3600" bind:value={t.delay_seconds} aria-label="Wait before task {i + 1}, seconds" /><span class="text-muted">s</span></label>
						<div class="flex items-center gap-1">
							<button type="button" class="btn btn-quiet btn-icon btn-sm" aria-label="Move task {i + 1} up" disabled={i === 0} onclick={() => moveTask(i, -1)}><Icon name="chevronDown" size={14} class="rotate-180" /></button>
							<button type="button" class="btn btn-quiet btn-icon btn-sm" aria-label="Move task {i + 1} down" disabled={i === tasks.length - 1} onclick={() => moveTask(i, 1)}><Icon name="chevronDown" size={14} /></button>
							<button type="button" class="btn btn-quiet btn-icon btn-sm text-fail" aria-label="Remove task {i + 1}" onclick={() => (tasks = tasks.filter((_, j) => j !== i))}><Icon name="x" size={14} /></button>
						</div>
						<label class="flex items-center gap-2 text-small text-muted sm:col-span-5"><input type="checkbox" bind:checked={t.continue_on_failure} />Continue with the next task if this one fails</label>
					</div>
				{/each}
				<div class="flex flex-wrap items-center gap-3">
					<button type="button" class="btn btn-sm" disabled={tasks.length >= 20} onclick={addTask}><Icon name="plus" size={14} />Add task</button>
					<span class="text-small {chainDelay > 3600 ? 'text-fail' : 'text-muted'}">Total wait {Math.floor(chainDelay / 60)} min {chainDelay % 60} s (at most 60 min)</span>
				</div>
			</fieldset>
		{/if}

		<div class="grid gap-3 sm:grid-cols-2">
			<label class="block">
				<span class="label">Repeat</span>
				<select class="field" bind:value={mode}>
					<option value="daily">Every day</option>
					<option value="weekly">Every week</option>
					<option value="hourly">Every few hours</option>
					<option value="custom">Custom (cron)</option>
				</select>
			</label>
			{#if mode === 'daily' || mode === 'weekly'}
				<label class="block"><span class="label">At</span><input class="field" type="time" bind:value={time} required /></label>
			{/if}
			{#if mode === 'weekly'}
				<label class="block">
					<span class="label">On</span>
					<select class="field" bind:value={weekday}>{#each days as d, i (d)}<option value={i}>{d}</option>{/each}</select>
				</label>
			{/if}
			{#if mode === 'hourly'}
				<label class="block">
					<span class="label">Every</span>
					<select class="field" bind:value={everyHours}>{#each [1, 2, 3, 4, 6, 8, 12] as h (h)}<option value={h}>{h} hour{h === 1 ? '' : 's'}</option>{/each}</select>
				</label>
			{/if}
			{#if mode === 'custom'}
				<label class="block sm:col-span-2">
					<span class="label">Cron specification</span>
					<input class="field font-mono" bind:value={custom} spellcheck="false" autocomplete="off" />
					<span class="help">Minute, hour, day of month, month, weekday. For example <code>30 4 * * 1-5</code> is 04:30 on weekdays.</span>
				</label>
			{/if}
			<label class="block sm:col-span-2">
				<span class="label">Time zone</span>
				<input class="field" list="sched-zones" bind:value={tz} required />
				<datalist id="sched-zones">{#each zones as z (z)}<option value={z}></option>{/each}</datalist>
			</label>
		</div>

		<div class="rounded-control border border-rule-soft bg-paper px-3 py-2">
			<p class="text-small text-muted">Next runs <span class="font-mono">({spec})</span></p>
			{#if previewError}
				<p class="text-small text-fail">{previewError}</p>
			{:else if preview.length}
				<ul class="text-small">{#each preview as t (t)}<li>{fmtWhen(t)}</li>{/each}</ul>
			{:else}
				<p class="text-small text-muted">…</p>
			{/if}
		</div>
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="sched-form" disabled={saving || !!previewError || (action === 'chain' && (tasks.length === 0 || chainDelay > 3600))}>{editing ? 'Save schedule' : 'Add schedule'}</button>
	{/snippet}
</Dialog>
