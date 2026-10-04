<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { Bot, EnvVar, Template, TemplateEnv } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot, running }: { bot: Bot; running: boolean } = $props();

	let vars = $state<EnvVar[] | null>(null);
	let template = $state<Template | null>(null);
	let error = $state('');
	let search = $state('');
	// name -> plaintext while shown; hidden again automatically after REVEAL_MS.
	let revealed = $state<Record<string, string>>({});
	const timers = new Map<string, ReturnType<typeof setTimeout>>();
	const REVEAL_MS = 30_000;
	let editing = $state<Record<string, string>>({});

	let addOpen = $state(false);
	let addName = $state('');
	let addValue = $state('');
	let addShow = $state(false);
	let addProblem = $state('');
	let importOpen = $state(false);
	let importText = $state('');
	let importStep = $state<'paste' | 'review'>('paste');
	let busy = $state(false);

	const path = $derived(`/bots/${bot.id}/env`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	async function load() {
		try {
			vars = (await api<{ vars: EnvVar[] }>('GET', path)).vars;
			error = '';
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		if (bot.template_id)
			api<{ templates: Template[] }>('GET', '/templates')
				.then((r) => (template = r.templates.find((t) => t.id === bot.template_id) ?? null))
				.catch(() => {});
		return registerDirty({ label: 'An environment variable', isDirty: () => Object.keys(editing).length > 0 });
	});
	onDestroy(() => timers.forEach(clearTimeout));

	async function put(v: Record<string, string>) {
		vars = (await api<{ vars: EnvVar[] }>('PUT', path, { vars: v })).vars;
	}

	const system = (n: string) => /^RIVET_/.test(n);
	const userVars = $derived((vars ?? []).filter((v) => !system(v.name) && (!search || v.name.toLowerCase().includes(search.toLowerCase()))));
	const systemVars = $derived((vars ?? []).filter((v) => system(v.name)));
	const missing = $derived(template && vars ? template.env.filter((t) => !vars!.some((v) => v.name === t.name)) : []);
	const hint = (n: string): TemplateEnv | undefined => template?.env.find((t) => t.name === n);
	const secretLike = (n: string) => /token|secret|key|password|pass|auth/i.test(n) || !!hint(n)?.secret;

	function hide(n: string) {
		delete revealed[n];
		clearTimeout(timers.get(n));
		timers.delete(n);
	}
	async function reveal(n: string) {
		if (n in revealed) return hide(n);
		try {
			revealed[n] = (await api<{ value: string }>('POST', `${path}/${encodeURIComponent(n)}/reveal`)).value;
			timers.set(n, setTimeout(() => hide(n), REVEAL_MS));
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function startEdit(n: string) {
		if (!(n in revealed)) await reveal(n);
		if (n in revealed) {
			clearTimeout(timers.get(n));
			editing[n] = revealed[n];
		}
	}
	async function saveEdit(n: string) {
		try {
			await put({ [n]: editing[n] });
			delete editing[n];
			hide(n);
			toast(`${n} updated${running ? '. Restart the bot to use it.' : ''}`);
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
	async function remove(n: string) {
		const ok = await confirmDialog({
			title: `Remove ${n}?`,
			body: hint(n)?.required ? `${template?.name} needs ${n} to run. The bot will fail to start without it.` : 'The value is deleted. The bot no longer receives this variable.',
			confirmLabel: 'Remove variable',
			tone: 'danger'
		});
		if (!ok) return;
		try {
			await api('DELETE', `${path}/${encodeURIComponent(n)}`);
			hide(n);
			await load();
			toast(`Removed ${n}`);
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}

	function openAdd(name = '') {
		addName = name;
		addValue = '';
		addShow = false;
		addProblem = '';
		addOpen = true;
	}
	async function add(e: SubmitEvent) {
		e.preventDefault();
		const n = addName.trim();
		if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(n)) return (addProblem = 'Use letters, digits and underscores, starting with a letter.');
		if (/^(RIVET_|LD_)|^PATH$/i.test(n)) return (addProblem = 'This name is reserved by the panel.');
		busy = true;
		try {
			const existed = vars?.some((v) => v.name === n);
			await put({ [n]: addValue });
			addOpen = false;
			toast(`${n} ${existed ? 'updated' : 'added'}${running ? '. Restart the bot to use it.' : ''}`);
		} catch (err) {
			addProblem = msg(err);
		} finally {
			busy = false;
		}
	}

	/** Parses .env text: KEY=VALUE per line, optional "export ", quotes, # comments. */
	function parseDotenv(text: string): Record<string, string> {
		const out: Record<string, string> = {};
		for (const raw of text.split(/\r?\n/)) {
			const line = raw.trim();
			if (!line || line.startsWith('#')) continue;
			const m = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/.exec(line);
			if (!m) continue;
			let v = m[2];
			const qch = v[0];
			if ((qch === '"' || qch === "'") && v.lastIndexOf(qch) > 0) v = v.slice(1, v.lastIndexOf(qch));
			else v = v.replace(/\s+#.*$/, '');
			out[m[1]] = qch === '"' ? v.replace(/\\n/g, '\n') : v;
		}
		return out;
	}
	const parsed = $derived(parseDotenv(importText));
	const parsedNames = $derived(Object.keys(parsed));
	const reserved = $derived(parsedNames.filter((n) => /^(RIVET_|LD_)|^PATH$/i.test(n)));
	async function applyImport() {
		const v = { ...parsed };
		for (const n of reserved) delete v[n];
		busy = true;
		try {
			await put(v);
			importOpen = false;
			importText = '';
			toast(`Imported ${Object.keys(v).length} variable${Object.keys(v).length === 1 ? '' : 's'}${running ? '. Restart the bot to use them.' : ''}`);
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			busy = false;
		}
	}
</script>

{#if missing.length && !running}
	<Notice tone="warn" class="mb-4" title="Values this template needs">
		<ul class="mt-1 space-y-2">
			{#each missing as m (m.name)}
				<li class="flex flex-wrap items-center gap-x-3 gap-y-1">
					<code class="font-medium">{m.name}</code>
					<span class="min-w-0 flex-1 basis-60 text-small text-muted">{m.description}</span>
					<button class="btn btn-sm btn-primary" onclick={() => openAdd(m.name)}>Add {m.label || m.name}</button>
				</li>
			{/each}
		</ul>
	</Notice>
{:else if missing.length}
	<Notice tone="warn" class="mb-4">Missing {missing.map((m) => m.name).join(', ')}. Stop the bot to add {missing.length === 1 ? 'it' : 'them'}.</Notice>
{/if}

<div class="flex flex-wrap items-center gap-2">
	<label class="relative min-w-0 flex-1 basis-48">
		<span class="sr-only">Search variables</span>
		<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
		<input class="field pl-8" type="search" placeholder="Search by name" bind:value={search} />
	</label>
	<button class="btn btn-primary" onclick={() => openAdd()} disabled={running}><Icon name="plus" />Add variable</button>
	<button class="btn" onclick={() => { importStep = 'paste'; importOpen = true; }} disabled={running}>Import .env</button>
</div>

{#if error}<Notice tone="fail" class="mt-3">{error}</Notice>{/if}

<div class="mt-3">
	{#if vars === null && !error}
		<Skeleton rows={3} label="Loading variables" />
	{:else if vars}
		<div class="card overflow-hidden">
			<table class="w-full">
				<thead class="hidden text-left text-small text-muted md:table-header-group">
					<tr class="border-b border-rule-soft"><th class="py-2 pl-3 font-medium">Name</th><th class="py-2 font-medium">Value</th><th class="py-2 font-medium">Changed</th><th><span class="sr-only">Actions</span></th></tr>
				</thead>
				<tbody class="divide-y divide-rule-soft">
					{#each userVars as v (v.name)}
						<tr class="block py-2 md:table-row md:py-0">
							<td class="block px-3 md:table-cell md:py-2.5"><code class="font-medium break-all">{v.name}</code>{#if hint(v.name)}<span class="ml-2 text-small text-muted">{hint(v.name)?.label}</span>{/if}</td>
							<td class="block px-3 md:table-cell md:w-[45%] md:py-2.5">
								{#if v.name in editing}
									<label><span class="sr-only">New value of {v.name}</span><input class="field font-mono text-small" type={secretLike(v.name) ? 'password' : 'text'} bind:value={editing[v.name]} autocomplete="off" spellcheck="false" /></label>
								{:else if v.name in revealed}
									<code class="block max-h-24 overflow-auto text-small break-all whitespace-pre-wrap">{revealed[v.name] || '(empty)'}</code>
								{:else}
									<span class="font-mono text-small text-muted" aria-label="hidden value">••••••••</span>
								{/if}
							</td>
							<td class="hidden text-small text-muted md:table-cell md:py-2.5">{fmtAgo(v.updated_at_ms)}</td>
							<td class="block px-3 md:table-cell md:py-2 md:pr-3 md:text-right">
								<span class="mt-1 inline-flex gap-1 md:mt-0">
									{#if v.name in editing}
										<button class="btn btn-sm btn-primary" onclick={() => saveEdit(v.name)}>Save</button>
										<button class="btn btn-sm" onclick={() => { delete editing[v.name]; hide(v.name); }}>Cancel</button>
									{:else}
										<button class="btn btn-sm btn-quiet" onclick={() => reveal(v.name)} aria-pressed={v.name in revealed} aria-label="{v.name in revealed ? 'Hide' : 'Reveal'} {v.name}"><Icon name={v.name in revealed ? 'eyeOff' : 'eye'} size={14} />{v.name in revealed ? 'Hide' : 'Reveal'}</button>
										<button class="btn btn-sm btn-quiet" onclick={() => startEdit(v.name)} disabled={running} aria-label="Edit {v.name}"><Icon name="pencil" size={14} />Edit</button>
										<button class="btn btn-sm btn-quiet text-fail" onclick={() => remove(v.name)} disabled={running} aria-label="Remove {v.name}"><Icon name="trash" size={14} /></button>
									{/if}
								</span>
							</td>
						</tr>
					{:else}
						<tr><td colspan="4" class="px-3 py-5 text-muted">{search ? `No variable names contain “${search}”.` : 'No variables yet. Add DISCORD_TOKEN, or whatever your code reads from its environment.'}</td></tr>
					{/each}
				</tbody>
			</table>
		</div>

		{#if systemVars.length}
			<h3 class="mt-6 text-title font-semibold">Managed by the panel</h3>
			<p class="text-small text-muted">Set automatically (for example by Analytics). They cannot be edited here.</p>
			<ul class="mt-2 list-card">
				{#each systemVars as v (v.name)}
					<li class="flex flex-wrap items-center gap-2 px-3 py-2">
						<code class="min-w-0 flex-1 text-small break-all">{v.name}</code>
						{#if v.name in revealed}<code class="text-small break-all">{revealed[v.name]}</code>{/if}
						<button class="btn btn-sm btn-quiet" onclick={() => reveal(v.name)}>{v.name in revealed ? 'Hide' : 'Reveal'}</button>
					</li>
				{/each}
			</ul>
		{/if}
	{/if}
</div>
<p class="mt-5 max-w-prose text-small text-muted">Values are encrypted in the database and hidden in every list. Reveal shows one value for 30 seconds. Anyone with Docker access on the host can still read them from a running container.</p>

<Dialog bind:open={addOpen} title={addName && vars?.some((v) => v.name === addName) ? `Update ${addName}` : 'Add a variable'} size="sm">
	<form id="env-add" class="grid gap-4" onsubmit={add} novalidate>
		<label class="block">
			<span class="label">Name</span>
			<input class="field font-mono" bind:value={addName} placeholder="DISCORD_TOKEN" autocomplete="off" spellcheck="false" aria-invalid={addProblem ? 'true' : undefined} />
			{#if hint(addName)}<span class="help">{hint(addName)?.description}</span>{/if}
		</label>
		<label class="block">
			<span class="label">Value</span>
			<span class="relative block">
				<input class="field pr-10 font-mono" type={secretLike(addName) && !addShow ? 'password' : 'text'} bind:value={addValue} autocomplete="off" spellcheck="false" />
				<button type="button" class="btn btn-quiet btn-icon btn-sm absolute top-1/2 right-1 -translate-y-1/2" aria-label={addShow ? 'Hide value' : 'Show value'} aria-pressed={addShow} onclick={() => (addShow = !addShow)}><Icon name={addShow ? 'eyeOff' : 'eye'} /></button>
			</span>
		</label>
		{#if addProblem}<p class="text-small text-fail" role="alert">{addProblem}</p>{/if}
	</form>
	{#snippet footer()}
		<button class="btn" onclick={() => (addOpen = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="env-add" disabled={busy || !addName.trim()}>Save variable</button>
	{/snippet}
</Dialog>

<Dialog bind:open={importOpen} title="Import a .env file" description={importStep === 'paste' ? 'Paste KEY=VALUE lines. Nothing is saved until you confirm.' : 'Check the names before saving. Values stay hidden.'} size="md">
	{#if importStep === 'paste'}
		<label class="block"><span class="sr-only">.env contents</span><textarea class="field h-48 font-mono text-small" bind:value={importText} spellcheck="false" placeholder={'DISCORD_TOKEN=abc\nPREFIX="!"'}></textarea></label>
	{:else}
		<ul class="list-card">
			{#each parsedNames as n (n)}
				<li class="flex items-center justify-between gap-3 px-3 py-1.5">
					<code class="break-all">{n}</code>
					<span class="shrink-0 text-small {reserved.includes(n) ? 'text-fail' : vars?.some((v) => v.name === n) ? 'text-warn' : 'text-run'}">{reserved.includes(n) ? 'Reserved, skipped' : vars?.some((v) => v.name === n) ? 'Replaces current value' : 'New'}</span>
				</li>
			{/each}
		</ul>
	{/if}
	{#snippet footer()}
		{#if importStep === 'paste'}
			<button class="btn" onclick={() => (importOpen = false)}>Cancel</button>
			<button class="btn btn-primary" disabled={!parsedNames.length} onclick={() => (importStep = 'review')}>Review {parsedNames.length || ''} variable{parsedNames.length === 1 ? '' : 's'}</button>
		{:else}
			<button class="btn" onclick={() => (importStep = 'paste')}>Back</button>
			<button class="btn btn-primary" disabled={busy || parsedNames.length === reserved.length} onclick={applyImport}>Save {parsedNames.length - reserved.length} variable{parsedNames.length - reserved.length === 1 ? '' : 's'}</button>
		{/if}
	{/snippet}
</Dialog>
