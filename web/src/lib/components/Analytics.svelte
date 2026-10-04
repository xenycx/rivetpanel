<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fetchText } from '$lib/api/client';
	import { fmtTime } from '$lib/args';
	import type { Analytics } from '$lib/api/types';
	import Sparkline from './Sparkline.svelte';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import WidgetGrid from './WidgetGrid.svelte';

	let { botId, canAdmin, stopped }: { botId: string; canAdmin: boolean; stopped: boolean } = $props();

	const windows = ['1h', '6h', '24h', '7d'];
	let win = $state('1h');
	let data = $state<Analytics | null>(null);
	let error = $state('');
	let customize = $state(false);
	let newKey = $state('');
	const langs = [
		{ id: 'discordjs', label: 'discord.js', file: 'rivetpanel.js' },
		{ id: 'discordpy', label: 'discord.py', file: 'rivetpanel.py' },
		{ id: 'go', label: 'Go', file: 'rivetpanel/rivetpanel.go' },
		{ id: 'rust', label: 'Rust', file: 'src/rivetpanel.rs' },
		{ id: 'java', label: 'Java', file: 'RivetPanel.java' },
		{ id: 'ruby', label: 'Ruby', file: 'rivetpanel.rb' }
	] as const;
	let lang = $state<(typeof langs)[number]['id']>('discordjs');
	let snippet = $state('');
	let showSetup = $state(false);

	// Dashboard layout is a per-browser preference: which stats to show and what to call them.
	type Layout = { hidden: string[]; labels: Record<string, string> };
	const lsKey = `rivetpanel.analytics.${botId}`;
	let layout = $state<Layout>({ hidden: [], labels: {} });
	try {
		const raw = localStorage.getItem(lsKey);
		if (raw) layout = { hidden: [], labels: {}, ...JSON.parse(raw) };
	} catch {
		/* storage unavailable: defaults */
	}
	function persist() {
		try {
			localStorage.setItem(lsKey, JSON.stringify(layout));
		} catch {
			/* ignore */
		}
	}

	const known: Record<string, string> = { guilds: 'Servers', members: 'Members', ping: 'Ping (ms)', voice_channels: 'Voice channels' };
	const label = (n: string) => layout.labels[n] || known[n] || n.replace(/[_.]/g, ' ');
	const visible = $derived((data?.stats ?? []).filter((s) => !layout.hidden.includes(s.name)));
	const fmt = (v: number) => (Math.abs(v) >= 1000 ? v.toLocaleString() : String(+v.toFixed(2)));
	const maxCmd = $derived(Math.max(1, ...(data?.commands ?? []).map((c) => c.count)));
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			data = await api<Analytics>('GET', `/bots/${botId}/analytics?window=${win}`);
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		const t = setInterval(() => document.visibilityState === 'visible' && load(), 5000);
		return () => clearInterval(t);
	});
	$effect(() => {
		win;
		load();
	});
	$effect(() => {
		if (showSetup) fetchText(`/sdk/${lang}`).then((t) => (snippet = t)).catch(() => (snippet = ''));
	});

	async function generate() {
		if (data?.key_set && !(await confirmDialog({ title: 'Replace the telemetry key?', body: 'The old key stops working immediately. The bot must be restarted to use the new one.', confirmLabel: 'Generate a new key' }))) return;
		error = '';
		try {
			newKey = (await api<{ key: string }>('POST', `/bots/${botId}/telemetry-key`)).key;
			await load();
			showSetup = true;
		} catch (e) {
			error = msg(e);
		}
	}
	async function revoke() {
		if (!(await confirmDialog({ title: 'Revoke the telemetry key?', body: 'The bot can no longer send stats. Existing analytics stay until they expire.', confirmLabel: 'Revoke key', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/bots/${botId}/telemetry-key`);
			newKey = '';
			await load();
			toast('Telemetry key revoked');
		} catch (e) {
			error = msg(e);
		}
	}
	const ago = (ms: number) => {
		const s = Math.round((Date.now() - ms) / 1000);
		return s < 60 ? `${s}s ago` : s < 3600 ? `${Math.round(s / 60)} min ago` : fmtTime(ms);
	};
</script>

<div class="flex flex-wrap items-center justify-between gap-3">
	<div class="flex gap-1" role="group" aria-label="Time window">
		{#each windows as w (w)}
			<button class="btn {win === w ? 'btn-primary' : ''}" aria-pressed={win === w} onclick={() => (win = w)}>{w}</button>
		{/each}
	</div>
	<div class="flex gap-2">
		<button class="btn" onclick={() => (customize = !customize)} aria-expanded={customize}>Customize</button>
		<button class="btn" onclick={() => (showSetup = !showSetup)} aria-expanded={showSetup}>Connect your bot</button>
	</div>
</div>
{#if error}<Notice tone="fail" class="mt-3">{error}</Notice>{/if}

{#if customize && data}
	<div class="mt-3 rounded-tile border border-rule-soft bg-panel p-3">
		<p class="mb-2 text-muted">Choose what appears on this dashboard and rename it. Saved in this browser.</p>
		<div class="grid gap-2 sm:grid-cols-2">
			{#each data.stats as s (s.name)}
				<div class="flex items-center gap-2">
					<input type="checkbox" checked={!layout.hidden.includes(s.name)} aria-label="Show {s.name}" onchange={(e) => { layout.hidden = e.currentTarget.checked ? layout.hidden.filter((n) => n !== s.name) : [...layout.hidden, s.name]; persist(); }} />
					<code class="w-32 shrink-0 truncate font-mono text-[12px]">{s.name}</code>
					<input class="field" placeholder={known[s.name] ?? s.name} value={layout.labels[s.name] ?? ''} maxlength="40" oninput={(e) => { layout.labels[s.name] = e.currentTarget.value; persist(); }} />
				</div>
			{:else}
				<p class="text-muted">Stats you send will be listed here.</p>
			{/each}
		</div>
	</div>
{/if}

{#if showSetup}
	<div class="mt-3 rounded-tile border border-rule-soft bg-panel p-4">
		<h2 class="text-title font-semibold">Send stats from your bot</h2>
		<ol class="mt-2 list-decimal space-y-1 pl-5">
			<li>Generate a key{#if !stopped} <span class="text-warn">(stop the bot first: the key is stored in its environment)</span>{/if}. It is added to the bot as <code class="font-mono">RIVET_TELEMETRY_KEY</code>, together with <code class="font-mono">RIVET_URL</code> when the panel's public address is configured.</li>
			<li>Copy the snippet below into your bot and call it as shown at the top of the file.</li>
			<li>Start the bot. Stats appear within a minute. Pushes are limited to 60 per minute and one sample per stat every 10 seconds.</li>
		</ol>
		{#if canAdmin}
			<div class="mt-3 flex flex-wrap gap-2">
				<button class="btn btn-primary" onclick={generate} disabled={!stopped}>{data?.key_set ? 'Generate a new key' : 'Generate key'}</button>
				{#if data?.key_set}<button class="btn btn-danger" onclick={revoke}>Revoke key</button>{/if}
			</div>
		{:else}<p class="mt-2 text-muted">Ask a full admin of this bot to generate a key.</p>{/if}
		{#if newKey}
			<p class="mt-3">Your new key (shown once): <code class="break-all bg-paper-2 px-1 font-mono text-[13px]">{newKey}</code></p>
			<p class="text-muted">Other clients can call <code class="font-mono">POST /api/v1/bot-telemetry</code> (or the WebSocket at <code class="font-mono">/api/v1/bot-telemetry/ws</code>) with <code class="font-mono">Authorization: Bearer …</code> and a JSON body <code class="font-mono">{'{"ready":true,"stats":{"guilds":3},"commands":[{"name":"ban"}],"events":[]}'}</code>. Every push is also a heartbeat (see Health & alerts).</p>
		{/if}
		<div class="mt-4 flex flex-wrap gap-1" role="tablist" aria-label="SDK language">
			{#each langs as l (l.id)}
				<button role="tab" aria-selected={lang === l.id} class="btn btn-sm {lang === l.id ? 'btn-primary' : ''}" onclick={() => (lang = l.id)}>{l.label}</button>
			{/each}
		</div>
		<div class="mt-2 flex items-center justify-between text-muted"><span class="font-mono text-small">{langs.find((l) => l.id === lang)?.file}</span><button class="btn btn-sm btn-quiet" onclick={() => navigator.clipboard?.writeText(snippet).then(() => toast('Snippet copied'))}>Copy</button></div>
		<pre class="mt-1 max-h-80 overflow-auto bg-term p-3 font-mono text-[12px] text-term-ink">{snippet}</pre>
	</div>
{/if}

{#if data}
	{#if data.stats.length === 0 && data.commands.length === 0 && data.events.length === 0 && (data.widgets?.length ?? 0) === 0}
		<div class="mt-4">
			<EmptyState title={data.key_set ? 'Waiting for the first report' : 'Send stats from your bot'}>
				<p>{data.key_set ? 'The key is set. Restart the bot with the SDK snippet in place; stats appear here as soon as it reports.' : 'Server counts, ping, command usage and events come from a small snippet in your bot. Nothing is collected until you add it.'}</p>
				{#snippet actions()}{#if !showSetup}<button class="btn btn-primary" onclick={() => (showSetup = true)}>Connect your bot</button>{/if}{/snippet}
			</EmptyState>
		</div>
	{:else}
		<WidgetGrid widgets={data.widgets ?? []} {botId} {canAdmin} onRemoved={load} />
		<div class="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
			{#each visible as s (s.name)}
				<div class="rounded-tile border border-rule-soft bg-panel p-3">
					<div class="text-muted">{label(s.name)}</div>
					<div class="mt-0.5 text-2xl font-semibold tabular-nums">{fmt(s.latest)}</div>
					<div class="mt-1"><Sparkline values={s.points.map((p) => p.v)} max={Math.max(1, ...s.points.map((p) => p.v))} label="{label(s.name)} over time" width={200} height={32} /></div>
				</div>
			{/each}
		</div>
		<p class="mt-2 text-muted">{data.last_at_ms ? `Last report ${ago(data.last_at_ms)}` : ''}</p>

		<div class="mt-4 grid gap-6 lg:grid-cols-2">
			<section aria-label="Command usage">
				<h2 class="text-title font-semibold">Command usage</h2>
				<ul class="mt-2 space-y-1.5">
					{#each data.commands as c (c.name)}
						<li class="grid grid-cols-[8rem_1fr_auto] items-center gap-2">
							<code class="truncate font-mono text-[13px]">/{c.name}</code>
							<div class="h-2 bg-paper-2"><div class="h-2 bg-action" style="width: {(c.count / maxCmd) * 100}%"></div></div>
							<span class="tabular-nums text-muted">{c.count.toLocaleString()}</span>
						</li>
					{:else}<li class="text-muted">No commands reported in this window.</li>{/each}
				</ul>
			</section>
			<section aria-label="Recent events">
				<h2 class="text-title font-semibold">Recent events</h2>
				<ul class="mt-2 divide-y divide-rule border-y border-rule">
					{#each data.events.slice(0, 12) as ev, i (i)}
						<li class="flex items-baseline gap-3 py-1.5"><span class="w-24 shrink-0 text-muted">{ago(ev.t)}</span><code class="font-mono text-[13px]">{ev.name}</code>{#if ev.data !== undefined && ev.data !== null}<span class="min-w-0 truncate text-muted">{JSON.stringify(ev.data)}</span>{/if}</li>
					{:else}<li class="py-2 text-muted">No events yet.</li>{/each}
				</ul>
			</section>
		</div>
	{/if}
{/if}
