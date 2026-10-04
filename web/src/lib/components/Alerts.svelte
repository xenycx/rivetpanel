<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo } from '$lib/args';
	import type { Bot } from '$lib/api/types';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	let { bot, admin }: { bot: Bot; admin: boolean } = $props();

	// Bots only: what the bot's own code reports (SDK heartbeat) and what the
	// host sees from outside (an active probe). Who gets told lives in
	// Settings → Notifications.
	type Health = { state: 'unknown' | 'ok' | 'stale' | 'not_ready'; last_seen_at_ms: number | null; ready: boolean | null; alerts: { heartbeat_after_s: number }; webhook: boolean };
	type Probe = { kind: ''|'tcp'|'http'; host_port: number; path: string; interval_s: number; timeout_ms: number; failure_threshold: number; success_threshold: number; startup_grace_s: number; restart_unhealthy: boolean; status: 'disabled'|'unknown'|'starting'|'healthy'|'unhealthy'; consecutive_failures: number; consecutive_successes: number; last_checked_at_ms: number|null; last_error: string|null };
	let health = $state<Health | null>(null);
	let probe = $state<Probe | null>(null);
	let probeDraft = $state<Probe | null>(null);
	let error = $state('');
	let saving = $state(false);
	let now = $state(Date.now());
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');
	const path = $derived(`/bots/${bot.id}`);

	async function load(first = false) {
		try {
			const [h,p] = await Promise.all([api<Health>('GET', `${path}/health`), api<Probe>('GET', `${path}/health-probe`)]);
			health = h; probe = p;
			if (first) probeDraft = { ...p };
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load(true);
		const t = setInterval(() => {
			now = Date.now();
			if (document.visibilityState === 'visible') load();
		}, 10000);
		return () => clearInterval(t);
	});

	const probeDirty = $derived(!!probe && !!probeDraft && JSON.stringify({ ...probeDraft, status:probe.status, consecutive_failures:probe.consecutive_failures, consecutive_successes:probe.consecutive_successes, last_checked_at_ms:probe.last_checked_at_ms, last_error:probe.last_error }) !== JSON.stringify(probe));
	async function saveProbe() {
		if (!probeDraft) return; saving=true;
		try { probe = await api<Probe>('PUT', `${path}/health-probe`, probeDraft); probeDraft={...probe}; toast(probe.kind ? 'Health probe saved' : 'Health probe disabled','success'); }
		catch(e){toast(msg(e),'fail')} finally {saving=false}
	}

	const stateText = {
		unknown: ['No heartbeat yet', 'The bot has not reported. Add the RivetPanel SDK to see whether it is connected to Discord, not just whether its process runs.'],
		ok: ['Reporting', 'The bot is pushing heartbeats and says it is ready.'],
		stale: ['Not reporting', 'The container runs, but the bot stopped pushing. It may be stuck or disconnected from Discord.'],
		not_ready: ['Not ready', 'The bot reports that it is not connected to Discord.']
	} as const;
	const tone = $derived(health ? ({ unknown: 'idle', ok: 'run', stale: 'fail', not_ready: 'warn' } as const)[health.state] : 'idle');
	const quiet = (s: number) => (s < 3600 ? `${s / 60} minute${s === 60 ? '' : 's'}` : '1 hour');
	const tcpPorts = $derived(bot.ports.filter((p)=>p.protocol==='tcp'));
	const probeWord = { disabled: 'Off', unknown: 'Waiting for the first check', starting: 'Starting (grace period)', healthy: 'Healthy', unhealthy: 'Unhealthy' } as const;
	const probeTone = $derived(probe?.status==='healthy'?'run':probe?.status==='unhealthy'?'fail':probe?.status==='starting'?'warn':'idle');
</script>

{#if error}<Notice tone="fail" class="mb-4">{error}</Notice>{/if}
{#if !health && !error}
	<Skeleton rows={3} label="Loading health" />
{:else if health}
	<!-- The two signals side by side: what the bot says and what the host sees. -->
	<div class="grid gap-3 md:grid-cols-2">
		<section aria-label="Application health" class="stat spine overflow-hidden pl-5" data-tone={tone}>
			<p class="eyebrow">Heartbeat</p>
			<p class="mt-1 text-title font-semibold">{stateText[health.state][0]}</p>
			<p class="text-small text-muted">{health.last_seen_at_ms ? `Last push ${fmtAgo(health.last_seen_at_ms, now)}` : 'Reported by the SDK inside the bot'}</p>
			{#if bot.restart_count}<p class="mt-1 text-small text-warn">{bot.restart_count} crash{bot.restart_count === 1 ? '' : 'es'} in a row in the current run.</p>{/if}
		</section>
		<section aria-label="Probe status" class="stat spine overflow-hidden pl-5" data-tone={probeTone}>
			<p class="eyebrow">{probe?.kind ? `${probe.kind.toUpperCase()} probe` : 'Probe'}</p>
			<p class="mt-1 text-title font-semibold">{probeWord[probe?.status ?? 'disabled']}</p>
			<p class="truncate text-small {probe?.last_error ? 'text-fail' : 'text-muted'}" title={probe?.last_error ?? undefined}>
				{#if probe?.last_error}{probe.last_error}{:else if probe?.last_checked_at_ms}Checked {fmtAgo(probe.last_checked_at_ms, now)}{:else}Checked by the host from outside the bot{/if}
			</p>
		</section>
	</div>
	<p class="mt-3 text-small text-muted">
		The process state in the header only says whether the container runs. A heartbeat says the bot's code is alive and connected; a probe catches a process that hangs without exiting.
		Who is told about crashes, deployments and a silent bot is set in <a class="link" href="/bots/{bot.id}?tab=settings#notifications">Settings → Notifications</a>{health.alerts.heartbeat_after_s ? ` (currently after ${quiet(health.alerts.heartbeat_after_s)} without a heartbeat)` : ''}.
		<a class="link" href="/docs#health">How health and alerts work</a>
	</p>

	<div class="mt-6">
		<SettingsSection title="Heartbeat from the SDK" description="Every push from the RivetPanel SDK counts as a heartbeat. It can also say whether the Discord connection is ready. Bots that never reported stay unknown and never alert.">
			<div class="grid gap-3">
				<p class="max-w-prose">{stateText[health.state][1]}</p>
				<ol class="grid gap-2 text-small">
					<li class="flex gap-3"><span class="step-no">1</span><span class="min-w-0 flex-1">Install the SDK for your runtime and call it once at start-up. <a class="link" href="/bots/{bot.id}?tab=page">Setup instructions are on the Page tab</a>.</span></li>
					<li class="flex gap-3"><span class="step-no">2</span><span class="min-w-0 flex-1">Report <code>ready: true</code> once your client has logged in to Discord, and <code>false</code> when it disconnects.</span></li>
					<li class="flex gap-3"><span class="step-no">3</span><span class="min-w-0 flex-1">Choose in <a class="link" href="/bots/{bot.id}?tab=settings#notifications">Settings → Notifications</a> how long a silent bot may stay quiet before you are told.</span></li>
				</ol>
			</div>
		</SettingsSection>

		<SettingsSection title="Active health probe" description="The host connects to one of the bot's published TCP ports on 127.0.0.1. Use it for bots that serve HTTP (a dashboard, webhooks) or listen on a port. Only the bot's own ports can be checked.">
			{#snippet aside()}
				<p class="mt-2 text-small"><a class="link" href="/bots/{bot.id}?tab=network">Publish a port in Network</a></p>
			{/snippet}
			{#if probeDraft}
				{@const off = !admin || !probeDraft.kind}
				<div class="grid gap-x-4 gap-y-4 sm:grid-cols-2 xl:grid-cols-3">
					<label><span class="label">Probe type</span><select class="field" bind:value={probeDraft.kind} disabled={!admin}><option value="">Off</option><option value="tcp">TCP connection</option><option value="http">HTTP GET</option></select>
						<span class="help">{probeDraft.kind === 'http' ? 'Healthy when the reply is 200–399. Redirects are not followed.' : probeDraft.kind === 'tcp' ? 'Healthy when the port accepts a connection.' : 'Choose TCP or HTTP to set up checks.'}</span></label>
					{#if probeDraft.kind}
						<label><span class="label">Published port</span><select class="field" bind:value={probeDraft.host_port} disabled={off}><option value={0}>Choose a TCP port</option>{#each tcpPorts as p (p.host_port)}<option value={p.host_port}>{p.host_port} → container {p.container_port}</option>{/each}</select>
							<span class="help">The host side of a port published in Network.</span></label>
					{/if}
					{#if probeDraft.kind === 'http'}<label><span class="label">HTTP path</span><input class="field font-mono" maxlength="256" placeholder="/healthz" bind:value={probeDraft.path} disabled={!admin} /><span class="help">Requested on every check, for example <code>/healthz</code>.</span></label>{/if}
				</div>
				{#if probeDraft.kind}
					<fieldset class="mt-5 grid min-w-0 gap-x-4 gap-y-4 border-t border-rule-soft pt-4 sm:grid-cols-2 xl:grid-cols-3" disabled={off}>
						<legend class="sr-only">Timing and thresholds</legend>
						<label><span class="label">Check every</span><select class="field" bind:value={probeDraft.interval_s}>{#each [5, 10, 15, 30, 60, 120, 300] as s (s)}<option value={s}>{s} seconds</option>{/each}</select>
							<span class="help">Time between two checks.</span></label>
						<label><span class="label">Timeout</span><select class="field" bind:value={probeDraft.timeout_ms}>{#each [500, 1000, 2000, 5000, 10000] as ms (ms)}<option value={ms}>{ms / 1000} seconds</option>{/each}</select>
							<span class="help">A slower check counts as failed.</span></label>
						<label><span class="label">Startup grace</span><select class="field" bind:value={probeDraft.startup_grace_s}>{#each [0, 10, 30, 60, 120, 300, 600] as s (s)}<option value={s}>{s} seconds</option>{/each}</select>
							<span class="help">Failures right after a start are ignored while the bot boots.</span></label>
						<label><span class="label">Failures before unhealthy</span><input class="field" type="number" min="1" max="10" bind:value={probeDraft.failure_threshold} />
							<span class="help">Failed checks in a row, 1 to 10.</span></label>
						<label><span class="label">Successes before healthy</span><input class="field" type="number" min="1" max="10" bind:value={probeDraft.success_threshold} />
							<span class="help">Passed checks in a row to recover, 1 to 10.</span></label>
					</fieldset>
				<label class="mt-4 flex items-start gap-2.5"><input type="checkbox" class="mt-0.5" bind:checked={probeDraft.restart_unhealthy} disabled={off} /><span>Restart when unhealthy<span class="help mt-0">Replaces the container once when it turns unhealthy, not on every failed check, so a broken deployment cannot restart in a loop.</span></span></label>
				{/if}
				{#if !tcpPorts.length}<Notice tone="warn" class="mt-4">This bot publishes no TCP port yet. <a href="/bots/{bot.id}?tab=network">Publish one in Network</a> before turning on a probe.</Notice>{/if}
				{#if admin}<div class="mt-4 flex gap-2"><button class="btn btn-primary" disabled={!probeDirty || saving} onclick={saveProbe}>Save health probe</button>{#if probeDirty}<button class="btn btn-quiet" onclick={() => probe && (probeDraft = { ...probe })}>Discard</button>{/if}</div>{/if}
			{/if}
		</SettingsSection>

	</div>
{/if}
