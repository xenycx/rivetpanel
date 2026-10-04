<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { type StatusPage, type StatusComponent, type StatusIncident, stateLabel, stateTone, incidentStatuses, maintenanceStatuses, impacts, words } from '$lib/api/kb';
	import { fmtWhen } from '$lib/args';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	type Candidate = { kind: string; id: string; name: string };
	let data = $state<StatusPage | null>(null);
	let candidates = $state<Candidate[]>([]);
	let error = $state('');
	let cfg = $state({ enabled: false, title: '', intro: '' });
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	async function load() {
		try {
			data = await api<StatusPage>('GET', '/status/manage');
			cfg = { enabled: !!data.enabled, title: data.title, intro: data.intro };
			replies = Object.fromEntries(data.incidents.map((i) => [i.id, { status: i.status, message: '' }]));
			candidates = (await api<{ candidates: Candidate[] }>('GET', '/status/manage/candidates')).candidates;
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(load);

	async function saveConfig(e: SubmitEvent) {
		e.preventDefault();
		try {
			await api('PUT', '/status/manage/config', cfg);
			toast(cfg.enabled ? 'Status page saved and published' : 'Status page saved (not published)');
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}

	// Components
	const kindLabel: Record<string, string> = { panel: 'Panel', node: 'Node', bot: 'Bot or server' };
	const shown = $derived(new Set((data?.components ?? []).map((c) => `${c.source?.kind}:${c.source?.id}`)));
	const available = $derived(candidates.filter((c) => !shown.has(`${c.kind}:${c.id}`)));
	let pick = $state('');
	let newName = $state('');
	let newDesc = $state('');
	async function addComponent(e: SubmitEvent) {
		e.preventDefault();
		const c = available.find((x) => `${x.kind}:${x.id}` === pick);
		if (!c) return;
		try {
			await api('POST', '/status/manage/components', { kind: c.kind, ref_id: c.id, name: newName, description: newDesc });
			pick = newName = newDesc = '';
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function editComponent(c: StatusComponent) {
		const name = await promptDialog({ title: 'Public name', body: 'Visitors see this name. Do not use internal host names or addresses.', label: 'Name', value: c.name, confirmLabel: 'Save' });
		if (!name) return;
		const description = await promptDialog({ title: 'Public description', label: 'Description (optional)', value: c.description, confirmLabel: 'Save' });
		try {
			await api('PATCH', `/status/manage/components/${c.id}`, { name, description: description ?? c.description });
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function moveComponent(c: StatusComponent, by: number) {
		const list = [...(data?.components ?? [])];
		const i = list.findIndex((x) => x.id === c.id);
		const j = i + by;
		if (j < 0 || j >= list.length) return;
		[list[i], list[j]] = [list[j], list[i]];
		try {
			await Promise.all(list.map((x, k) => (x.position === k ? null : api('PATCH', `/status/manage/components/${x.id}`, { position: k }))));
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function removeComponent(c: StatusComponent) {
		if (!(await confirmDialog({ title: `Remove “${c.name}”?`, body: 'It disappears from the status page together with its uptime history.', confirmLabel: 'Remove', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/status/manage/components/${c.id}`);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}

	// Incidents
	let composing = $state<'' | 'incident' | 'maintenance'>('');
	let inc = $state({ title: '', impact: 'minor', status: 'investigating', component_ids: [] as string[], starts: '', ends: '', message: '' });
	function startIncident(kind: 'incident' | 'maintenance') {
		composing = kind;
		inc = { title: '', impact: kind === 'incident' ? 'minor' : 'none', status: kind === 'incident' ? 'investigating' : 'scheduled', component_ids: [], starts: '', ends: '', message: '' };
	}
	const toMS = (v: string) => (v ? new Date(v).getTime() : undefined);
	async function createIncident(e: SubmitEvent) {
		e.preventDefault();
		try {
			await api('POST', '/status/manage/incidents', {
				kind: composing,
				title: inc.title,
				impact: inc.impact,
				status: inc.status,
				component_ids: inc.component_ids,
				starts_at_ms: toMS(inc.starts),
				ends_at_ms: toMS(inc.ends),
				message: inc.message
			});
			composing = '';
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	let replies = $state<Record<string, { status: string; message: string }>>({});
	async function postUpdate(i: StatusIncident, e: SubmitEvent) {
		e.preventDefault();
		try {
			await api('POST', `/status/manage/incidents/${i.id}/updates`, replies[i.id]);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
	async function removeIncident(i: StatusIncident) {
		if (!(await confirmDialog({ title: `Delete “${i.title}”?`, body: 'The incident and its timeline are removed from the status page. Prefer resolving it to keep the history.', confirmLabel: 'Delete', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/status/manage/incidents/${i.id}`);
			await load();
		} catch (err) {
			toast(msg(err), 'fail');
		}
	}
</script>

<svelte:head><title>Status page · RivetPanel</title></svelte:head>
<div class="flex flex-wrap items-end justify-between gap-3">
	<div>
		<h2 class="text-section">Status page</h2>
		<p class="mt-1 max-w-3xl text-muted">A public page at <a class="link" href="/status" target="_blank" rel="noopener">/status</a> that shows only the components you add, under the names you choose. Internal names, ids, addresses, owners and error messages are never published.</p>
	</div>
</div>
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}

{#if !data && !error}
	<div class="mt-4"><Skeleton rows={4} label="Loading the status page" /></div>
{:else if data}
	<form class="card mt-5 grid gap-4 p-5" onsubmit={saveConfig}>
		<Switch label="Publish the status page" bind:checked={cfg.enabled}>Anyone can open it without signing in. While off, /status answers “not found” and no samples are taken.</Switch>
		<label class="block"><span class="label">Title</span><input class="field" maxlength="100" bind:value={cfg.title} placeholder="Service status" /></label>
		<label class="block"><span class="label">Introduction (optional)</span><textarea class="field min-h-20" maxlength="1000" bind:value={cfg.intro}></textarea></label>
		<div><button class="btn btn-primary">Save</button></div>
	</form>

	<section class="mt-8">
		<h3 class="text-title font-semibold">Components</h3>
		<p class="text-small text-muted">States are automatic: the panel is up while it answers; a node follows its agent connection; a bot follows its running state and health check. Open incidents raise the state to their impact; maintenance in its window shows as maintenance.</p>
		{#if data.components.length}
			<ul class="card mt-3 divide-y divide-rule-soft">
				{#each data.components as c, i (c.id)}
					<li class="flex flex-wrap items-center gap-2 px-4 py-3">
						<span class="size-2.5 rounded-pill {stateTone[c.state]?.bg}" aria-hidden="true"></span>
						<span class="min-w-0 flex-1">
							<span class="font-medium">{c.name}</span>
							<span class="block text-small text-muted">
								{kindLabel[c.source?.kind ?? ''] ?? ''}{#if c.source?.name}: {c.source.name}{/if}{#if c.source?.missing} <span class="text-fail">(source no longer exists)</span>{/if}
								· {stateLabel[c.state]}{#if c.uptime !== null} · {c.uptime}% over {data.retention_days} days{/if}
							</span>
						</span>
						<button class="btn btn-quiet btn-icon" aria-label="Move {c.name} up" disabled={i === 0} onclick={() => moveComponent(c, -1)}>↑</button>
						<button class="btn btn-quiet btn-icon" aria-label="Move {c.name} down" disabled={i === data.components.length - 1} onclick={() => moveComponent(c, 1)}>↓</button>
						<button class="btn btn-quiet" onclick={() => editComponent(c)}>Edit</button>
						<button class="btn btn-quiet text-fail" onclick={() => removeComponent(c)}>Remove</button>
					</li>
				{/each}
			</ul>
		{/if}
		<form class="card mt-3 grid gap-3 p-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto] sm:items-end" onsubmit={addComponent}>
			<label class="block"><span class="label">Follow</span>
				<select class="field" bind:value={pick} required>
					<option value="" disabled>Choose…</option>
					{#each available as c (c.kind + c.id)}<option value="{c.kind}:{c.id}">{kindLabel[c.kind]}: {c.name}</option>{/each}
				</select>
			</label>
			<label class="block"><span class="label">Public name</span><input class="field" required maxlength="100" bind:value={newName} placeholder="e.g. Game servers (EU)" /></label>
			<label class="block"><span class="label">Description (optional)</span><input class="field" maxlength="300" bind:value={newDesc} /></label>
			<button class="btn btn-primary"><Icon name="plus" />Add</button>
		</form>
		<p class="mt-1 text-small text-muted">You can add nodes with the Nodes permission, and bots and servers you can open.</p>
	</section>

	<section class="mt-8">
		<div class="flex flex-wrap items-center gap-2">
			<h3 class="text-title font-semibold">Incidents and maintenance</h3>
			<span class="ml-auto"></span>
			<button class="btn" onclick={() => startIncident('incident')}><Icon name="alert" />Report incident</button>
			<button class="btn" onclick={() => startIncident('maintenance')}><Icon name="clock" />Schedule maintenance</button>
		</div>
		{#if composing}
			<form class="card mt-3 grid gap-4 p-5" onsubmit={createIncident}>
				<h4 class="font-semibold">{composing === 'incident' ? 'New incident' : 'Scheduled maintenance'}</h4>
				<label class="block"><span class="label">Title</span><input class="field" required maxlength="200" bind:value={inc.title} /></label>
				<div class="grid gap-4 sm:grid-cols-2">
					{#if composing === 'incident'}
						<label class="block"><span class="label">Impact</span><select class="field" bind:value={inc.impact}>{#each impacts as x}<option value={x}>{x}</option>{/each}</select></label>
						<label class="block"><span class="label">Status</span><select class="field" bind:value={inc.status}>{#each incidentStatuses.slice(0, 3) as x}<option value={x}>{words(x)}</option>{/each}</select></label>
					{:else}
						<label class="block"><span class="label">Starts</span><input class="field" type="datetime-local" required bind:value={inc.starts} /></label>
						<label class="block"><span class="label">Ends (optional)</span><input class="field" type="datetime-local" bind:value={inc.ends} /></label>
					{/if}
				</div>
				{#if data.components.length}
					<fieldset><legend class="label">Affected components</legend>
						<div class="flex flex-wrap gap-x-5 gap-y-1">{#each data.components as c (c.id)}<label class="flex items-center gap-2 text-small"><input type="checkbox" value={c.id} bind:group={inc.component_ids} />{c.name}</label>{/each}</div>
					</fieldset>
				{/if}
				<label class="block"><span class="label">First update (public)</span><textarea class="field min-h-24" required maxlength="5000" bind:value={inc.message}></textarea></label>
				<div class="flex gap-2"><button class="btn btn-primary">Publish</button><button type="button" class="btn btn-quiet" onclick={() => (composing = '')}>Cancel</button></div>
			</form>
		{/if}
		{#if !data.incidents.length}
			<p class="mt-3 text-small text-muted">Nothing reported in the last {data.retention_days} days.</p>
		{:else}
			<div class="mt-3 grid gap-3">
				{#each data.incidents as i (i.id)}
					<article class="card p-5">
						<div class="flex flex-wrap items-baseline gap-2">
							<h4 class="font-semibold">{i.title}</h4>
							<span class="rounded-pill border border-rule px-2 py-0.5 text-[12px] capitalize">{i.kind} · {words(i.status)}</span>
							{#if i.active}<span class="text-[12px] text-action">in its window now</span>{/if}
							<button class="btn btn-quiet ml-auto text-fail" onclick={() => removeIncident(i)}><Icon name="trash" />Delete</button>
						</div>
						<p class="text-small text-muted">
							{#if i.kind === 'incident'}{i.impact} impact · {/if}{#if i.starts_at_ms}{fmtWhen(i.starts_at_ms)}{#if i.ends_at_ms} – {fmtWhen(i.ends_at_ms)}{/if} · {/if}{i.components.map((c) => c.name).join(', ') || 'no components'}
						</p>
						<ol class="mt-3 grid gap-2 border-l-2 border-rule-soft pl-4">
							{#each i.updates as u}<li class="text-small"><span class="font-medium capitalize">{words(u.status)}</span> <span class="text-muted">· {fmtWhen(u.created_at_ms)}</span><p class="whitespace-pre-line">{u.body}</p></li>{/each}
						</ol>
						<form class="mt-3 grid gap-2 sm:grid-cols-[12rem_minmax(0,1fr)_auto] sm:items-end" onsubmit={(e) => postUpdate(i, e)}>
							<label class="block"><span class="label">Status</span><select class="field" bind:value={replies[i.id].status}>{#each i.kind === 'incident' ? incidentStatuses : maintenanceStatuses as x}<option value={x}>{words(x)}</option>{/each}</select></label>
							<label class="block"><span class="label">Update (public)</span><input class="field" required maxlength="5000" bind:value={replies[i.id].message} /></label>
							<button class="btn">Post update</button>
						</form>
					</article>
				{/each}
			</div>
		{/if}
	</section>
{/if}
