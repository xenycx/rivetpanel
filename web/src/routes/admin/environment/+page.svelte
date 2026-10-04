<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/state';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { EnvVar, EnvView } from '$lib/api/admin';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import BytesField from '$lib/components/BytesField.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let view = $state<EnvView | null>(null);
	let error = $state('');
	let saving = $state(false);
	let restarting = $state(false);
	let drafts = $state<Record<string, string>>({}); // edited value per variable
	let clear = $state<Set<string>>(new Set()); // secrets to blank
	let resets = $state<Set<string>>(new Set()); // overrides to drop
	let find = $state(page.url.searchParams.get('find') ?? '');
	let show = $state<'all' | 'changed' | 'pending'>('all');
	let showFixed = $state(false);
	let flash = $state('');

	async function load() {
		try {
			view = await api<EnvView>('GET', '/admin/environment');
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The environment could not be loaded.';
		}
	}
	onMount(async () => {
		await load();
		const target = page.url.searchParams.get('find');
		if (target && view?.vars.some((v) => v.name === target)) {
			const fixedVar = !view.vars.find((v) => v.name === target)?.editable;
			if (fixedVar) showFixed = true;
			flash = target;
			find = '';
			await tick();
			document.getElementById(`v-${target}`)?.scrollIntoView({ block: 'center' });
			setTimeout(() => (flash = ''), 2500);
		}
	});

	const editable = $derived((view?.vars ?? []).filter((v) => v.editable));
	const fixed = $derived((view?.vars ?? []).filter((v) => !v.editable));
	const current = (v: EnvVar) => (v.name in drafts ? drafts[v.name] : v.value);
	const changed = (v: EnvVar) => (resets.has(v.name) ? true : v.secret ? clear.has(v.name) || !!drafts[v.name] : v.name in drafts && drafts[v.name] !== v.value);
	const dirty = $derived(editable.filter(changed));
	function edit(v: EnvVar, value: string) {
		drafts[v.name] = value;
		resets.delete(v.name);
		resets = new Set(resets);
	}
	function undo(v: EnvVar) {
		delete drafts[v.name];
		drafts = { ...drafts };
		clear.delete(v.name);
		clear = new Set(clear);
		resets.delete(v.name);
		resets = new Set(resets);
	}
	function reset(v: EnvVar) {
		delete drafts[v.name];
		drafts = { ...drafts };
		clear.delete(v.name);
		clear = new Set(clear);
		resets = new Set(resets).add(v.name);
	}
	function discard() {
		drafts = {};
		clear = new Set();
		resets = new Set();
	}

	const matches = (v: EnvVar) => {
		const f = find.trim().toLowerCase().replace(/^rivet_/, '');
		if (f && !`${v.name} ${v.description} ${v.group} ${v.value}`.toLowerCase().includes(f)) return false;
		if (show === 'changed') return v.override || changed(v);
		if (show === 'pending') return v.pending;
		return true;
	};
	const shown = $derived(editable.filter(matches));
	const groups = $derived((view?.groups ?? []).map((g) => ({ name: g, vars: shown.filter((v) => v.group === g) })).filter((g) => g.vars.length));

	async function save() {
		if (!dirty.length) return;
		const risky = dirty.filter((v) => v.danger && !resets.has(v.name) && current(v) !== '' && (v.name in drafts || clear.has(v.name)));
		if (risky.length) {
			const ok = await confirmDialog({
				title: 'Save changes that need care?',
				body: risky.map((v) => `${v.name}: ${v.danger}`).join('\n'),
				confirmLabel: 'Save anyway',
				tone: 'danger'
			});
			if (!ok) return;
		}
		const set: Record<string, string> = {};
		const unset: string[] = [];
		for (const v of dirty) {
			if (resets.has(v.name)) unset.push(v.name);
			else if (v.secret) set[v.name] = clear.has(v.name) ? '' : drafts[v.name];
			else set[v.name] = drafts[v.name];
		}
		saving = true;
		error = '';
		try {
			view = await api<EnvView>('PUT', '/admin/environment', { set, unset });
			discard();
			toast(view.pending ? 'Saved. The changes apply after a restart.' : 'Saved', 'success');
		} catch (e) {
			error = e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The changes could not be saved.';
		} finally {
			saving = false;
		}
	}

	async function restart() {
		const ok = await confirmDialog({
			title: 'Restart RivetPanel now?',
			body: 'The panel exits and your service manager (systemd, or Docker’s restart policy) starts it again with the saved values. The page is unavailable for a few seconds. Bots keep running.\nIf nothing starts it again, the panel stays down until you start it yourself.',
			confirmLabel: 'Restart panel',
			tone: 'danger'
		});
		if (!ok) return;
		const before = view?.started_at_ms;
		try {
			await api('POST', '/admin/environment/restart');
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The restart could not be requested.';
			return;
		}
		restarting = true;
		let sawDown = false;
		for (let i = 0; i < 90; i++) {
			await new Promise((r) => setTimeout(r, 1000));
			try {
				const r = await fetch('/api/v1/healthz', { cache: 'no-store' });
				if (r.ok && sawDown) break;
				if (!r.ok) sawDown = true;
			} catch {
				sawDown = true;
			}
		}
		await load();
		restarting = false;
		if (view && view.started_at_ms !== before) toast('RivetPanel restarted with the saved values', 'success');
		else error = 'The panel did not come back within 90 seconds. Check the service on the host.';
	}

	const srcText = { default: 'Default', environment: 'Environment file', panel: 'Set here' } as const;
	function shownValue(v: EnvVar, val: string): string {
		if (v.secret) return val ? 'set' : 'not set';
		if (val === '') return v.default ? `${v.default} (default)` : 'not set';
		if (v.kind === 'bytes' && /^\d+$/.test(val)) return val === '0' ? '0 (unlimited)' : fmtBytes(Number(val));
		return val;
	}
</script>

<svelte:head><title>Environment · RivetPanel</title></svelte:head>

<h2 class="text-section">Environment</h2>
<p class="mt-1 max-w-3xl text-muted">Every <code>RIVET_</code> variable the panel reads, with where its value comes from. Changes made here are stored in the panel and take priority over the environment file; they apply after a restart. The environment file itself is never edited.</p>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if view?.rejected}
	<Notice tone="fail" class="mt-4" title="Saved values were ignored at the last start">
		They did not form a valid configuration, so the panel started with the environment file alone: {view.rejected}. Fix the values below and save, or reset them.
	</Notice>
{/if}
{#if view?.unreadable?.length}
	<Notice tone="warn" class="mt-4" title="Some saved values cannot be decrypted">{view.unreadable.join(', ')} were sealed with an encryption key that is not loaded. They were skipped; set them again.</Notice>
{/if}

{#if restarting}
	<Notice tone="info" class="mt-4" live title="Restarting…">Waiting for RivetPanel to come back. This page reloads its values when it does.</Notice>
{:else if view && view.pending > 0}
	<Notice tone="warn" class="mt-4" title="{view.pending} saved change{view.pending === 1 ? '' : 's'} not in effect yet">
		The panel is still running with the values it started with.
		{#if !view.can_restart}Restart the service on the host to apply them: <code>sudo systemctl restart rivetpanel</code> or <code>docker compose restart</code>.{/if}
		{#snippet action()}{#if view?.can_restart}<button class="btn btn-sm btn-primary" onclick={restart}><Icon name="restart" size={13} />Restart panel</button>{/if}{/snippet}
	</Notice>
{/if}

{#if !view && !error}
	<div class="mt-5"><Skeleton rows={6} label="Loading the environment" /></div>
{:else if view}
	<div class="mt-5 flex flex-wrap items-center gap-3">
		<label class="relative min-w-56 flex-1 sm:max-w-sm"><span class="sr-only">Find a variable</span>
			<Icon name="search" class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-muted" />
			<input class="field pl-8" type="search" placeholder="Find a variable, e.g. backup or memory" bind:value={find} />
		</label>
		<div class="inline-flex rounded-control border border-rule bg-raised p-0.5" role="group" aria-label="Show">
			{#each [['all', 'All'], ['changed', 'Set here'], ['pending', 'Needs restart']] as [id, label] (id)}
				<button class="rounded-inner px-2.5 py-1 text-small font-medium {show === id ? 'bg-action text-action-ink' : 'text-muted hover:text-ink'}" aria-pressed={show === id} onclick={() => (show = id as typeof show)}>{label}</button>
			{/each}
		</div>
		<span class="text-small text-muted">{shown.length} of {editable.length} editable</span>
	</div>

	<div class="mt-5 grid gap-6">
		{#each groups as g (g.name)}
			<section aria-labelledby="g-{g.name}">
				<h3 id="g-{g.name}" class="eyebrow mb-2">{g.name}</h3>
				<ul class="list-card">
					{#each g.vars as v (v.name)}
						{@const val = current(v)}
						{@const isChanged = changed(v)}
						{@const resetting = resets.has(v.name)}
						<li id="v-{v.name}" class="grid gap-x-8 gap-y-3 px-4 py-3.5 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)] {flash === v.name ? 'bg-action/10' : isChanged ? 'bg-warn/5' : ''}">
							<div class="min-w-0">
								<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
									<label for="f-{v.name}" class="font-mono text-[13px] font-medium break-all">{v.name}</label>
									<span class="pill" data-tone={v.source === 'panel' ? 'run' : undefined}>{srcText[v.source]}</span>
									{#if v.pending}<span class="pill" data-tone="warn" title="Running now: {shownValue(v, v.running)}">Restart to apply</span>{/if}
									{#if resetting}<span class="pill" data-tone="warn">Will be reset</span>{:else if isChanged}<span class="pill" data-tone="warn">Unsaved</span>{/if}
								</div>
								<p class="mt-1 text-small text-muted">{v.description}</p>
								{#if v.override && v.env_set && !resetting}
									<p class="mt-1 text-small text-muted">The environment file says <span class="font-mono text-ink">{v.secret ? 'a value' : shownValue(v, v.env_value)}</span>; the value set here is used instead.</p>
								{/if}
								{#if v.danger && isChanged && !resetting}<p class="mt-1.5 text-small text-warn">{v.danger}</p>{/if}
							</div>
							<div class="min-w-0">
								{#if v.secret}
									<input id="f-{v.name}" class="field font-mono" type="password" autocomplete="new-password" placeholder={clear.has(v.name) ? 'Will be cleared' : v.set ? 'Set. Type to replace' : 'Not set'} value={drafts[v.name] ?? ''} oninput={(e) => { clear.delete(v.name); clear = new Set(clear); edit(v, e.currentTarget.value); }} disabled={clear.has(v.name) || resetting} />
									<p class="help">Stored encrypted and never shown again.</p>
								{:else if v.kind === 'bytes'}
									<BytesField id="f-{v.name}" value={resetting ? v.env_value : val} placeholder={v.default} disabled={resetting} onchange={(x) => edit(v, x)} />
									<p class="help">{val === '' ? (v.default ? `Default: ${v.default}` : 'Not set') : val === '0' ? 'Unlimited' : `${Number(val).toLocaleString()} bytes`}</p>
								{:else if v.kind === 'bool'}
									<select id="f-{v.name}" class="field" value={resetting ? '' : val} onchange={(e) => edit(v, e.currentTarget.value)} disabled={resetting}>
										<option value="">Default ({v.default === '1' ? 'on' : 'off'})</option><option value="1">On (1)</option><option value="0">Off (0)</option>
									</select>
								{:else if v.kind === 'enum'}
									<select id="f-{v.name}" class="field" value={resetting ? '' : val} onchange={(e) => edit(v, e.currentTarget.value)} disabled={resetting}>
										<option value="">Default ({v.default})</option>
										{#each v.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
									</select>
								{:else if v.kind === 'int'}
									<input id="f-{v.name}" class="field font-mono" type="number" step="1" placeholder={v.default || v.example} value={resetting ? v.env_value : val} oninput={(e) => edit(v, e.currentTarget.value)} disabled={resetting} />
									{#if v.default}<p class="help">Default: {v.default}</p>{/if}
								{:else}
									<input id="f-{v.name}" class="field font-mono" placeholder={v.example || v.default} value={resetting ? v.env_value : val} oninput={(e) => edit(v, e.currentTarget.value)} disabled={resetting} spellcheck="false" autocomplete="off" />
									<p class="help">{v.default && !val ? `Default: ${v.default}. ` : ''}{v.kind === 'duration' ? 'For example 30s, 10m or 24h. ' : ''}{val === '' && v.kind !== 'duration' && !v.default ? 'Blank switches it off.' : ''}</p>
								{/if}
								<div class="mt-1.5 flex flex-wrap gap-1.5">
									{#if isChanged}<button class="btn btn-sm" onclick={() => undo(v)}>Undo</button>{/if}
									{#if v.secret && v.set && !clear.has(v.name) && !isChanged}<button class="btn btn-sm btn-danger" onclick={() => { clear.add(v.name); clear = new Set(clear); delete drafts[v.name]; }}>Clear value</button>{/if}
									{#if v.override && !resetting}<button class="btn btn-sm" onclick={() => reset(v)} title="Remove the value set here">Use {v.env_set ? 'environment file value' : 'default'}</button>{/if}
								</div>
							</div>
						</li>
					{/each}
				</ul>
			</section>
		{:else}
			<p class="card px-4 py-6 text-center text-muted">No variable matches.</p>
		{/each}
	</div>

	<section class="mt-8" aria-labelledby="fixed-h">
		<h3 id="fixed-h" class="flex items-center gap-2 text-title font-semibold">
			<button class="flex items-center gap-1.5" aria-expanded={showFixed} onclick={() => (showFixed = !showFixed)}><Icon name={showFixed ? 'chevronDown' : 'chevronRight'} size={14} />Not editable here</button>
			<span class="pill">{fixed.length}</span>
		</h3>
		<p class="mt-1 max-w-3xl text-small text-muted">Read before the database opens, or able to lock the panel out or widen what it may do to the host, so they stay in the environment file. Sign-in providers and the public address live on Panel settings.</p>
		{#if showFixed}
			<ul class="list-card mt-3">
				{#each fixed as v (v.name)}
					<li id="v-{v.name}" class="grid gap-x-8 gap-y-1 px-4 py-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)] {flash === v.name ? 'bg-action/10' : ''}">
						<div class="min-w-0">
							<p class="font-mono text-[13px] font-medium break-all">{v.name}</p>
							<p class="mt-0.5 text-small text-muted">{v.description}</p>
						</div>
						<div class="min-w-0 text-small">
							<p class="font-mono break-all">{v.secret ? (v.set ? 'set' : 'not set') : v.value === '' ? (v.default ? `${v.default} (default)` : 'not set') : v.value}</p>
							<p class="text-muted">{#if v.managed}<a class="link" href={v.managed}>Edit under Panel settings</a>{:else}Change it in the environment file{/if}</p>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</section>

	<div class="h-24"></div>
	{#if dirty.length}
		<div class="sticky bottom-4 z-10 flex w-fit max-w-full flex-wrap items-center gap-3 rounded-tile border border-rule bg-raised px-4 py-2.5 shadow-overlay" role="region" aria-label="Unsaved changes">
			<span class="text-small"><strong>{dirty.length}</strong> unsaved change{dirty.length === 1 ? '' : 's'}</span>
			<button class="btn btn-sm" onclick={discard} disabled={saving}>Discard</button>
			<button class="btn btn-primary btn-sm" onclick={save} disabled={saving}>{saving ? 'Checking and saving…' : 'Save changes'}</button>
		</div>
	{/if}
{/if}
