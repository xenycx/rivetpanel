<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import type { Bot, Port } from '$lib/api/types';

	let { bot, stopped, owner, onSaved }: { bot: Bot; stopped: boolean; owner: boolean; onSaved: (b: Bot) => void } = $props();

	// The form is a draft of the saved settings: it starts from them and
	// follows later changes of the bot while it has no unsaved edits.
	const fromBot = (b: Bot) => ({ outbound: b.network_enabled, bandwidth: b.bandwidth_kbps ?? 0, ports: b.ports.map((p) => ({ ...p })) });
	const saved = $derived(fromBot(bot));
	const start = (() => fromBot(bot))();

	let outbound = $state(start.outbound);
	let bandwidth = $state(start.bandwidth);
	let ports = $state<Port[]>(start.ports);
	let error = $state('');
	let range = $state({ min: 20000, max: 29999, publicBind: false });
	const snapshot = () => JSON.stringify([outbound, bandwidth, ports]);
	let base = $state(snapshot());
	const dirty = $derived(snapshot() !== base);
	let syncedFrom = JSON.stringify(start);
	$effect.pre(() => {
		const s = saved;
		const key = JSON.stringify(s);
		untrack(() => {
			if (key === syncedFrom) return;
			syncedFrom = key;
			if (dirty) return; // keep unsaved edits
			({ outbound, bandwidth, ports } = s);
			base = snapshot();
		});
	});
	onMount(() => {
		api<{ limits: { port_min: number; port_max: number; port_public_bind: boolean } }>('GET', '/runtimes')
			.then((r) => (range = { min: r.limits.port_min, max: r.limits.port_max, publicBind: r.limits.port_public_bind }))
			.catch(() => {});
		return registerDirty({ label: 'Network settings', isDirty: () => dirty, save: async () => await save() });
	});

	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	function add() {
		const used = new Set(ports.map((p) => p.host_port));
		let hp = range.min;
		while (used.has(hp) && hp < range.max) hp++;
		ports = [...ports, { container_port: 8080, host_port: hp, protocol: 'tcp', host_ip: '127.0.0.1' }];
	}

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		error = '';
		try {
			// Ports first: they cannot be added while networking is off, and networking
			// cannot be switched off while ports exist.
			let b = bot;
			if (!outbound) b = await api<Bot>('PUT', `/bots/${bot.id}/ports`, { ports: [] });
			b = await api<Bot>('PATCH', `/bots/${bot.id}`, { network_enabled: outbound, bandwidth_kbps: bandwidth || 0 });
			if (outbound && owner) b = await api<Bot>('PUT', `/bots/${bot.id}/ports`, { ports });
			bot = b;
			ports = b.ports.map((p) => ({ ...p }));
			onSaved(b);
			base = snapshot();
			toast('Network settings saved. They apply the next time the bot starts.');
			return true;
		} catch (err) {
			error = msg(err);
			return false;
		}
	}
</script>

<form class="grid max-w-3xl gap-6" onsubmit={save}>
	<fieldset class="grid gap-3" disabled={!stopped}>
		<legend class="mb-1 text-title font-semibold">Outbound internet access</legend>
		<label class="flex items-start gap-2"><input type="checkbox" class="mt-1" bind:checked={outbound} /><span>Allow the bot to connect to the internet.<span class="help">Discord bots need this to reach Discord. Turning it off also disables published ports.</span></span></label>
		<label class="block max-w-xs">
			<span class="label">Bandwidth limit</span>
			<span class="flex items-center gap-2"><input class="field" type="number" min="0" step="any" bind:value={bandwidth} placeholder="0" /><span class="shrink-0 text-muted">kbit/s</span></span>
			<span class="help">0 means unlimited. Recorded for this bot, but <strong>not enforced yet</strong>: Docker has no built-in bandwidth cap, so it needs traffic shaping on the host.</span>
		</label>
	</fieldset>

	<fieldset class="grid gap-3" disabled={!stopped || !outbound || !owner}>
		<legend class="mb-1 text-title font-semibold">Published ports</legend>
		{#if !owner}<Notice>Only the bot's owner or an administrator can publish host ports.</Notice>{/if}
		{#if ports.length}
			<div class="overflow-x-auto">
				<table class="w-full text-left">
					<thead class="text-muted"><tr><th class="pb-1 font-medium">Host address</th><th class="pb-1 font-medium">Host port</th><th class="pb-1 font-medium">Container port</th><th class="pb-1 font-medium">Protocol</th><th></th></tr></thead>
					<tbody>
						{#each ports as p, i (i)}
							<tr>
								<td class="pr-2 pb-2"><select class="field" bind:value={p.host_ip} aria-label="Host address"><option value="127.0.0.1">127.0.0.1 (this host only)</option>{#if range.publicBind || p.host_ip === '0.0.0.0'}<option value="0.0.0.0">0.0.0.0 (public)</option>{/if}</select></td>
								<td class="pr-2 pb-2"><input class="field w-28" type="number" step="1" min={range.min} max={range.max} bind:value={p.host_port} aria-label="Host port" /></td>
								<td class="pr-2 pb-2"><input class="field w-28" type="number" step="1" min="1" max="65535" bind:value={p.container_port} aria-label="Container port" /></td>
								<td class="pr-2 pb-2"><select class="field" bind:value={p.protocol} aria-label="Protocol"><option>tcp</option><option>udp</option></select></td>
								<td class="pb-2"><button type="button" class="btn btn-quiet btn-icon text-fail" aria-label="Remove port {p.host_port}" onclick={() => (ports = ports.filter((_, j) => j !== i))}><Icon name="trash" /></button></td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{:else}
			<p class="text-muted">No ports are published. Add one if your bot runs a web server, dashboard or health check.</p>
		{/if}
		<div><button type="button" class="btn" onclick={add}>Add port</button></div>
		<p class="text-small text-muted">Host ports must be between {range.min} and {range.max} and are unique across bots. Addresses other than 127.0.0.1 may be disabled by the administrator; put a reverse proxy in front of published ports.</p>
	</fieldset>

	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	<div class="sticky bottom-0 flex items-center gap-3 border-t border-rule-soft bg-paper/95 py-3 backdrop-blur-sm">
		<button class="btn btn-primary" disabled={!stopped || !dirty}>Save network settings</button>
		{#if dirty}<span class="text-small text-warn">Unsaved changes</span>{/if}
	</div>
</form>
