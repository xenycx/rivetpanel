<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import type { LocationInfo, NodeInfo } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import { confirmDialog, promptDialog } from '$lib/ui/dialogs.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import NodeStrip from '$lib/components/NodeStrip.svelte';

	type Draft = { name: string; location_id: string; public_address: string; enabled: boolean; draining: boolean };
	let nodes = $state<NodeInfo[] | null>(null);
	let locations = $state<LocationInfo[] | null>(null);
	let drafts = $state<Record<string, Draft>>({});
	let error = $state('');
	let addOpen = $state(false);
	let adding = $state(false);
	let add = $state({ name: '', location_id: '', ttl_minutes: 15 });
	let created = $state<{ token: string; command: string; expires_at_ms: number } | null>(null);

	const message = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const time = (ms: number | null | undefined) => (ms ? new Date(ms).toLocaleString() : 'Never');
	const nodeStatus = (n: NodeInfo) => !n.enabled ? 'Disabled' : n.draining ? 'Draining' : n.transport === 'local' ? 'Local' : n.agent?.connected ? 'Connected' : 'Offline';
	const tone = (n: NodeInfo) => !n.enabled ? undefined : n.draining ? 'warn' : n.transport === 'local' || n.agent?.connected ? 'run' : 'fail';

	async function load() {
		try {
			const [nr, lr] = await Promise.all([
				api<{ nodes: NodeInfo[] }>('GET', '/nodes'),
				api<{ locations: LocationInfo[] }>('GET', '/nodes/locations')
			]);
			nodes = nr.nodes;
			locations = lr.locations;
			drafts = Object.fromEntries(nr.nodes.map((n) => [n.id, { name: n.name, location_id: n.location_id, public_address: n.public_address, enabled: n.enabled, draining: n.draining }]));
			add.location_id ||= lr.locations[0]?.id ?? '';
			error = '';
		} catch (e) {
			error = message(e);
		}
	}
	onMount(load);

	async function save(n: NodeInfo) {
		try {
			await api('PATCH', `/nodes/${n.id}`, drafts[n.id]);
			toast(`${drafts[n.id].name} updated`, 'success');
			await load();
		} catch (e) { toast(message(e), 'fail'); }
	}
	async function enroll(e: SubmitEvent) {
		e.preventDefault();
		adding = true;
		try {
			created = await api('POST', '/nodes/enrollments', add);
			await load();
		} catch (err) { toast(message(err), 'fail'); }
		finally { adding = false; }
	}
	async function revoke(n: NodeInfo) {
		if (!(await confirmDialog({ title: `Revoke ${n.name}'s certificate?`, body: 'The agent is disconnected immediately and cannot reconnect. Enroll it again with a new identity before using this node.', confirmLabel: 'Revoke certificate', tone: 'danger' }))) return;
		try { await api('POST', `/nodes/${n.id}/revoke-certificates`, {}); toast('Certificate revoked', 'success'); await load(); }
		catch (e) { toast(message(e), 'fail'); }
	}
	async function reenroll(n: NodeInfo) {
		try {
			created = await api('POST', `/nodes/${n.id}/enrollment`, { ttl_minutes: 15 });
			addOpen = true;
			await load();
		} catch (e) { toast(message(e), 'fail'); }
	}
	async function remove(n: NodeInfo) {
		if (!(await confirmDialog({ title: `Delete ${n.name}?`, body: 'Only an agent node with no servers can be deleted. Its saved identity and connection history are removed.', confirmLabel: 'Delete node', tone: 'danger' }))) return;
		try { await api('DELETE', `/nodes/${n.id}`); toast('Node deleted', 'success'); await load(); }
		catch (e) { toast(message(e), 'fail'); }
	}
	async function addLocation() {
		const name = await promptDialog({ title: 'New location', label: 'Name', placeholder: 'Frankfurt', confirmLabel: 'Add location', validate: (v) => v.trim() ? null : 'Enter a name.' });
		if (name === null) return;
		try { await api('POST', '/nodes/locations', { name: name.trim(), description: '' }); toast('Location added', 'success'); await load(); }
		catch (e) { toast(message(e), 'fail'); }
	}
	async function editLocation(l: LocationInfo) {
		const name = await promptDialog({ title: `Rename ${l.name}`, label: 'Name', value: l.name, confirmLabel: 'Save', validate: (v) => v.trim() ? null : 'Enter a name.' });
		if (name === null) return;
		try { await api('PATCH', `/nodes/locations/${l.id}`, { name: name.trim(), description: l.description }); await load(); }
		catch (e) { toast(message(e), 'fail'); }
	}
	async function deleteLocation(l: LocationInfo) {
		if (!(await confirmDialog({ title: `Delete ${l.name}?`, body: 'A location can only be deleted after every node has been moved elsewhere.', confirmLabel: 'Delete location', tone: 'danger' }))) return;
		try { await api('DELETE', `/nodes/locations/${l.id}`); await load(); }
		catch (e) { toast(message(e), 'fail'); }
	}
	function openAdd() { created = null; add = { name: '', location_id: locations?.[0]?.id ?? '', ttl_minutes: 15 }; addOpen = true; }
	function copy(v: string) { navigator.clipboard?.writeText(v).then(() => toast('Copied', 'success')); }
</script>

<svelte:head><title>Nodes · RivetPanel</title></svelte:head>

<div class="flex flex-wrap items-start justify-between gap-3">
	<div><h2 class="text-section">Nodes</h2><p class="mt-1 max-w-3xl text-muted">Hosts that run bots and game servers. Drain a node before maintenance to keep new servers off it.</p></div>
	{#if session.features.agents}<button class="btn btn-primary" onclick={openAdd}><Icon name="plus" />Add remote node</button>{/if}
</div>

{#if !session.features.agents}<Notice tone="info" class="mt-4">Remote nodes are off. Enable the <code>agents</code> preview module and restart the panel to enroll one.</Notice>{/if}
{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if nodes === null && !error}<div class="mt-5"><Skeleton rows={5} label="Loading nodes" /></div>{/if}

{#if nodes}
	<div class="mt-5 grid gap-4">
		{#each nodes as n (n.id)}
			{@const d = drafts[n.id]}
			<section class="card p-4 sm:p-5">
				<div class="flex flex-wrap items-start justify-between gap-3">
					<div><div class="flex items-center gap-2"><h3 class="text-title font-semibold">{n.name}</h3><span class="pill" data-tone={tone(n)}>{nodeStatus(n)}</span></div><p class="mt-1 text-small text-muted">{n.transport === 'local' ? 'Control-plane host' : n.agent?.hostname || 'Remote agent'} · {n.server_count} {n.server_count === 1 ? 'server' : 'servers'}</p></div>
					{#if n.transport === 'agent'}<div class="flex gap-2">{#if n.agent?.certificate_serial}<button class="btn btn-sm" onclick={() => revoke(n)}>Revoke certificate</button>{:else}<button class="btn btn-sm" onclick={() => reenroll(n)}>Create enrollment</button>{/if}<button class="btn btn-sm btn-danger" onclick={() => remove(n)} disabled={n.server_count > 0}>Delete</button></div>{/if}
				</div>
				<div class="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
					<label><span class="label">Name</span><input class="field" bind:value={d.name} maxlength="64" /></label>
					<label><span class="label">Location</span><select class="field" bind:value={d.location_id}>{#each locations ?? [] as l (l.id)}<option value={l.id}>{l.name}</option>{/each}</select></label>
					<label><span class="label">Public address</span><input class="field font-mono" bind:value={d.public_address} placeholder="play.example.com" /><span class="help">Host name or IP users connect to.</span></label>
					<div class="grid content-start gap-2 pt-1"><label class="flex items-center gap-2"><input type="checkbox" bind:checked={d.enabled} /><span>Enabled</span></label><label class="flex items-center gap-2"><input type="checkbox" bind:checked={d.draining} /><span>Drain: no new servers</span></label></div>
				</div>
				<div class="mt-4 flex flex-wrap items-center gap-3"><button class="btn btn-primary btn-sm" onclick={() => save(n)}>Save</button><span class="text-small text-muted">Last seen: {time(n.last_seen_at_ms)}{#if n.agent?.agent_version} · agent {n.agent.agent_version}{/if}{#if n.agent?.certificate_expires_at_ms} · certificate expires {time(n.agent.certificate_expires_at_ms)}{/if}</span></div>
				{#if n.agent?.capabilities?.problem}<Notice tone="warn" class="mt-3">{String(n.agent.capabilities.problem)}</Notice>{/if}
			</section>
		{/each}
	</div>
{/if}

{#if nodes}
	<section class="mt-8" aria-labelledby="node-metrics-title">
		<h2 id="node-metrics-title" class="text-section">Metrics</h2>
		<p class="mt-1 mb-4 text-muted">CPU, memory, disk and running servers per node, sampled every telemetry interval. Remote nodes report through their agent while connected.</p>
		<NodeStrip />
	</section>
{/if}

<section class="mt-8" aria-labelledby="locations-title">
	<div class="flex items-center justify-between gap-3"><div><h2 id="locations-title" class="text-section">Locations</h2><p class="mt-1 text-muted">Group nodes by datacenter or region.</p></div><button class="btn" onclick={addLocation}><Icon name="plus" />Add location</button></div>
	{#if locations}<ul class="list-card mt-4">{#each locations as l (l.id)}<li class="flex items-center justify-between gap-3 px-4 py-3"><div><span class="font-medium">{l.name}</span><span class="ml-2 text-small text-muted">{l.node_count} {l.node_count === 1 ? 'node' : 'nodes'}</span>{#if l.description}<p class="text-small text-muted">{l.description}</p>{/if}</div><div class="flex gap-2"><button class="btn btn-sm" onclick={() => editLocation(l)}>Rename</button><button class="btn btn-sm btn-danger" onclick={() => deleteLocation(l)} disabled={l.node_count > 0}>Delete</button></div></li>{/each}</ul>{/if}
</section>

<Dialog bind:open={addOpen} title={created ? 'Enroll the node' : 'Add a remote node'} size="lg">
	{#if created}
		<Notice tone="warn" title="Copy this command now">The enrollment token is shown once and expires {time(created.expires_at_ms)}.</Notice>
		<pre class="mt-4 overflow-x-auto rounded-tile bg-paper-2 p-4 text-small whitespace-pre-wrap break-all">{created.command}</pre>
		<div class="mt-4 flex gap-2"><button class="btn btn-primary" onclick={() => copy(created!.command)}><Icon name="copy" />Copy command</button><button class="btn" onclick={() => { addOpen = false; created = null; }}>Done</button></div>
	{:else}
		<form class="grid gap-4" onsubmit={enroll}>
			<label><span class="label">Node name</span><input class="field" bind:value={add.name} maxlength="64" placeholder="edge-one" required /></label>
			<label><span class="label">Location</span><select class="field" bind:value={add.location_id}>{#each locations ?? [] as l (l.id)}<option value={l.id}>{l.name}</option>{/each}</select></label>
			<label><span class="label">Token lifetime</span><select class="field" bind:value={add.ttl_minutes}><option value={15}>15 minutes</option><option value={60}>1 hour</option><option value={1440}>24 hours</option></select></label>
			<Notice tone="info">Run the generated command on the remote host after installing <code>rivet-agent</code>. The agent connects outward; no inbound agent port is needed.</Notice>
			<div class="flex gap-2"><button class="btn btn-primary" disabled={adding}>{adding ? 'Creating…' : 'Create enrollment'}</button><button type="button" class="btn" onclick={() => (addOpen = false)}>Cancel</button></div>
		</form>
	{/if}
</Dialog>
