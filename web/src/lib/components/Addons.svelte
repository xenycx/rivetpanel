<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError, fmtBytes, MiB } from '$lib/api/client';
	import { Perm, can, type AddonKind, type Bot, type BotAddon } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot, stopped, onChanged }: { bot: Bot; stopped: boolean; onChanged?: () => void } = $props();
	const isGame = $derived(bot.kind === 'game');
	const noun = $derived(isGame ? 'server' : 'bot');

	let kinds = $state<AddonKind[]>([]);
	let available = $state(true);
	let list = $state<BotAddon[] | null>(null);
	let error = $state('');
	let busy = $state('');
	let revealed = $state<Record<string, Record<string, string>>>({});
	let logs = $state<Record<string, string>>({});
	let memEdit = $state<Record<string, number>>({});

	const admin = $derived(can(bot, Perm.admin));
	const canEnv = $derived(can(bot, Perm.env));
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const attached = $derived(new Set((list ?? []).map((a) => a.kind)));

	async function load() {
		try {
			list = (await api<{ addons: BotAddon[] }>('GET', `/bots/${bot.id}/addons`)).addons;
		} catch (e) {
			error = msg(e);
			list = [];
		}
	}
	onMount(() => {
		api<{ addons: AddonKind[]; available: boolean }>('GET', '/addons')
			.then((r) => ((kinds = r.addons), (available = r.available)))
			.catch(() => {});
		load();
		const t = setInterval(() => !document.hidden && load(), 10000);
		return () => clearInterval(t);
	});

	async function add(k: AddonKind) {
		busy = k.id;
		error = '';
		try {
			await api('POST', `/bots/${bot.id}/addons`, { kind: k.id });
			toast(`${k.display_name} added. It starts with the bot.`);
			await load();
			onChanged?.();
		} catch (e) {
			error = msg(e);
		} finally {
			busy = '';
		}
	}

	async function remove(a: BotAddon) {
		const r = await confirmDialog({
			title: `Remove ${a.display_name}?`,
			body: `The ${a.display_name} container and all of its data (${fmtBytes(a.data_bytes)}) are deleted permanently. Variables such as ${a.variables[0]} disappear from the bot.`,
			confirmLabel: 'Remove and delete data',
			tone: 'danger',
			typeToConfirm: a.kind
		});
		if (!r) return;
		busy = a.kind;
		try {
			await api('DELETE', `/bots/${bot.id}/addons/${a.kind}`);
			toast(`${a.display_name} removed`);
			delete revealed[a.kind];
			await load();
			onChanged?.();
		} catch (e) {
			error = msg(e);
		} finally {
			busy = '';
		}
	}

	async function saveMemory(a: BotAddon) {
		busy = a.kind;
		try {
			await api('PATCH', `/bots/${bot.id}/addons/${a.kind}`, { memory_bytes: Math.round(memEdit[a.kind] * MiB) });
			toast('Memory limit saved');
			delete memEdit[a.kind];
			await load();
		} catch (e) {
			error = msg(e);
		} finally {
			busy = '';
		}
	}

	async function reveal(a: BotAddon) {
		if (revealed[a.kind]) {
			delete revealed[a.kind];
			return;
		}
		try {
			revealed[a.kind] = (await api<{ variables: Record<string, string> }>('POST', `/bots/${bot.id}/addons/${a.kind}/reveal`)).variables;
		} catch (e) {
			error = msg(e);
		}
	}

	async function showLogs(a: BotAddon) {
		if (logs[a.kind] !== undefined) {
			delete logs[a.kind];
			return;
		}
		try {
			logs[a.kind] = (await api<{ output: string }>('GET', `/bots/${bot.id}/addons/${a.kind}/logs?lines=200`)).output || 'No output yet.';
		} catch (e) {
			error = msg(e);
		}
	}

	function stateText(a: BotAddon): { text: string; tone: string } {
		const s = a.status;
		if (!s.state) return { text: `Starts with the ${noun}`, tone: 'text-muted' };
		if (s.state === 'running') {
			if (s.health === 'healthy') return { text: 'Running', tone: 'text-run' };
			if (s.health === 'unhealthy') return { text: 'Unhealthy', tone: 'text-fail' };
			return { text: 'Starting', tone: 'text-warn' };
		}
		if (s.state === 'exited' && s.exit_code !== 0) return { text: `Stopped (exit ${s.exit_code})`, tone: 'text-fail' };
		return { text: 'Stopped', tone: 'text-muted' };
	}
</script>

<SettingsSection
	title="Databases"
	description="Databases that run next to this {noun} in their own containers. They are reachable only from this {noun}, by host name, on a private network without internet access, and start and stop with the {noun}. Their data is kept outside the {noun}'s files and is not part of its backups."
>
	{#if !available}
		<Notice>This panel runs without Docker, so databases are not available.</Notice>
	{:else if list === null}
		<Skeleton rows={2} />
	{:else}
		{#if error}<Notice tone="fail" class="mb-3" live>{error}</Notice>{/if}
		{#if list.length === 0}
			<p class="text-muted">No databases yet.</p>
		{/if}
		<ul class="grid gap-3">
			{#each list as a (a.kind)}
				{@const st = stateText(a)}
				<li class="surface p-4">
					<div class="flex flex-wrap items-baseline justify-between gap-2">
						<div>
							<span class="text-title font-semibold">{a.display_name}</span>
							<span class="ml-2 text-small {st.tone}">{st.text}</span>
						</div>
						<span class="text-small text-muted">host <code>{a.host}:{a.port}</code> · {fmtBytes(a.memory_bytes)} memory · {fmtBytes(a.data_bytes)} data</span>
					</div>
					<p class="mt-1 text-muted">{a.description}</p>
					<p class="mt-2 text-small text-muted">
						The {noun} receives {#each a.variables as v, i (v)}<code>{v}</code>{i < a.variables.length - 1 ? ', ' : ''}{/each}.{#if isGame} Use Show connection to copy the host, user and password into a plugin's configuration file.{:else} Reference them from your own variables as <code>{'${' + a.variables[0] + '}'}</code>.{/if}
					</p>
					<div class="mt-3 flex flex-wrap gap-2">
						{#if canEnv}<button class="btn btn-sm" onclick={() => reveal(a)}><Icon name={revealed[a.kind] ? 'eyeOff' : 'eye'} size={14} />{revealed[a.kind] ? 'Hide connection' : 'Show connection'}</button>{/if}
						{#if can(bot, Perm.console)}<button class="btn btn-sm" onclick={() => showLogs(a)}><Icon name="terminal" size={14} />{logs[a.kind] !== undefined ? 'Hide log' : 'Log'}</button>{/if}
						{#if admin}
							{#if memEdit[a.kind] !== undefined}
								<span class="flex items-center gap-1">
									<input class="field w-24 py-1" type="number" min={1} bind:value={memEdit[a.kind]} aria-label="Memory in MiB" /><span class="text-small text-muted">MiB</span>
									<button class="btn btn-sm btn-primary" disabled={busy === a.kind || !stopped} onclick={() => saveMemory(a)}>Save</button>
									<button class="btn btn-sm btn-quiet" onclick={() => delete memEdit[a.kind]}>Cancel</button>
								</span>
							{:else}
								<button class="btn btn-sm" disabled={!stopped} title={stopped ? '' : `Stop the ${noun} first`} onclick={() => (memEdit[a.kind] = Math.round(a.memory_bytes / MiB))}><Icon name="sliders" size={14} />Memory</button>
							{/if}
							<button class="btn btn-sm btn-quiet text-fail" disabled={!stopped || busy === a.kind} title={stopped ? '' : `Stop the ${noun} first`} onclick={() => remove(a)}><Icon name="trash" size={14} />Remove</button>
						{/if}
					</div>
					{#if revealed[a.kind]}
						<dl class="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 border-t border-rule-soft pt-3 font-mono text-[12px]">
							{#each Object.entries(revealed[a.kind]).sort() as [k, v] (k)}<dt class="text-muted">{k}</dt><dd class="break-all select-all">{v}</dd>{/each}
						</dl>
					{/if}
					{#if logs[a.kind] !== undefined}
						<pre class="mt-3 max-h-72 overflow-auto rounded-tile bg-paper p-3 font-mono text-[12px] whitespace-pre-wrap">{logs[a.kind]}</pre>
					{/if}
				</li>
			{/each}
		</ul>

		{#if admin}
			<h3 class="mt-6 font-semibold">Add a database</h3>
			{#if !stopped}<p class="mt-1 text-small text-warn">Stop the {noun} to add or remove databases.</p>{/if}
			<div class="mt-3 grid gap-3 sm:grid-cols-2">
				{#each kinds.filter((k) => !attached.has(k.id)) as k (k.id)}
					<div class="flex flex-col rounded-tile border border-rule-soft bg-panel p-4">
						<span class="flex items-baseline justify-between gap-2"><span class="font-semibold">{k.display_name}</span><span class="text-small text-muted">{fmtBytes(k.default_memory_bytes)}</span></span>
						<span class="mt-1 flex-1 text-small text-muted">{k.description}</span>
						<button class="btn btn-sm mt-3 self-start" disabled={!stopped || busy === k.id} onclick={() => add(k)}><Icon name="plus" size={14} />{busy === k.id ? 'Adding…' : 'Add'}</button>
					</div>
				{/each}
			</div>
		{/if}
	{/if}
</SettingsSection>
