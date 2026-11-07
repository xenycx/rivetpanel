<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { joinArgs, splitArgs } from '$lib/args';
	import type { Bot, Volume } from '$lib/api/types';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	// A container app's configuration: its image (changeable while it runs:
	// the container is replaced), its command, and the folders it may write.
	let { bot, stopped, onSaved }: { bot: Bot; stopped: boolean; onSaved: (b: Bot) => void } = $props();

	type Policy = { registries: string[]; max_image_bytes: number; enabled: boolean };
	const shortImage = (ref: string) => ref.replace(/^docker\.io\/library\//, '').replace(/^docker\.io\//, '');
	const fromBot = (b: Bot) => ({
		image: shortImage(b.image_ref),
		command: joinArgs(b.argv),
		entrypoint: joinArgs(b.entrypoint),
		policy: b.restart_policy,
		user: b.container?.user ?? '',
		writable: (b.container?.writable ?? []).join('\n'),
		volumes: (b.container?.volumes ?? []).map((v) => ({ ...v }))
	});
	const start = (() => fromBot(bot))();
	let image = $state(start.image);
	let command = $state(start.command);
	let entrypoint = $state(start.entrypoint);
	let policy = $state(start.policy);
	let user = $state(start.user);
	let writable = $state(start.writable);
	let volumes = $state<Volume[]>(start.volumes);
	let info = $state<Policy | null>(null);
	let error = $state('');
	let busy = $state(false);
	let pulling = $state(false);

	const snapshot = () => JSON.stringify([image, command, entrypoint, policy, user, writable, volumes]);
	let base = $state(snapshot());
	const dirty = $derived(snapshot() !== base);
	let syncedFrom = JSON.stringify(start);
	$effect.pre(() => {
		const s = fromBot(bot);
		const key = JSON.stringify(s);
		untrack(() => {
			if (key === syncedFrom) return;
			syncedFrom = key;
			if (dirty) return;
			({ image, command, entrypoint, policy, user, writable } = s);
			volumes = s.volumes;
			base = snapshot();
		});
	});
	const startupChanged = $derived(command !== joinArgs(bot.argv) || entrypoint !== joinArgs(bot.entrypoint) || policy !== bot.restart_policy);

	onMount(() => {
		api<Policy>('GET', '/containers/policy').then((p) => (info = p)).catch(() => {});
		return registerDirty({ label: 'Configuration', isDirty: () => dirty, save: async () => await save() });
	});

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		error = '';
		busy = true;
		try {
			let b = await api<Bot>('PUT', `/bots/${bot.id}/container`, {
				image,
				user,
				writable: writable.split('\n').map((s) => s.trim()).filter(Boolean),
				volumes: volumes.filter((v) => v.name.trim() || v.path.trim()).map((v) => ({ name: v.name.trim(), path: v.path.trim() }))
			});
			if (startupChanged) {
				b = await api<Bot>('PATCH', `/bots/${bot.id}`, { argv: splitArgs(command), entrypoint: splitArgs(entrypoint), restart_policy: policy });
			}
			onSaved(b);
			base = snapshot();
			toast(bot.desired_state === 'running' ? 'Saved. The container is replaced with the new settings.' : 'Configuration saved');
			return true;
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The configuration could not be saved.';
			return false;
		} finally {
			busy = false;
		}
	}
	async function pull() {
		pulling = true;
		try {
			onSaved(await api<Bot>('POST', `/bots/${bot.id}/container/pull`));
			toast(bot.desired_state === 'running' ? 'Pulling the image again and restarting' : 'The image is pulled again at the next start');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The pull could not be requested.', 'fail');
		} finally {
			pulling = false;
		}
	}
</script>

<form onsubmit={save}>
	<SettingsSection title="Image" description="The image this app runs. Changing it replaces the container, also while it runs. A tag such as :latest is pulled once per host; pull again to pick up a newer image.">
		<div class="grid gap-3">
			<label class="block">
				<span class="label">Image</span>
				<input class="field font-mono text-[13px]" bind:value={image} spellcheck="false" autocomplete="off" placeholder="nginx:1.27 or ghcr.io/owner/app:v1" required />
				{#if info}
					<span class="help">{info.registries.length ? `Allowed registries: ${info.registries.join(', ')}.` : 'No registry is allowed on this panel.'}{info.max_image_bytes ? ` Images up to ${Math.round(info.max_image_bytes / 2 ** 20)} MiB.` : ''}</span>
				{/if}
			</label>
			<div><button type="button" class="btn btn-sm" onclick={pull} disabled={pulling}><Icon name="download" size={13} />{pulling ? 'Requesting…' : 'Pull again'}</button></div>
		</div>
	</SettingsSection>

	<SettingsSection title="Command" description="Leave both empty to run the image's own entrypoint and command. Changes need a stopped app.">
		<div class="grid gap-5">
			<label class="block">
				<span class="label">Command <span class="font-normal text-muted">(optional)</span></span>
				<input class="field font-mono text-[13px]" bind:value={command} disabled={!stopped} spellcheck="false" placeholder="the image's command" />
				<span class="help">Arguments for the entrypoint, such as <code class="font-mono">redis-server --appendonly yes</code>. Quote arguments containing spaces; no shell runs them.</span>
			</label>
			<label class="block">
				<span class="label">Entrypoint <span class="font-normal text-muted">(optional)</span></span>
				<input class="field font-mono text-[13px]" bind:value={entrypoint} disabled={!stopped} spellcheck="false" placeholder="the image's entrypoint" />
			</label>
			<label class="block">
				<span class="label">When it exits</span>
				<select class="field" bind:value={policy} disabled={!stopped}>
					<option value="on_failure">Restart it after a crash (non-zero exit)</option>
					<option value="never">Never restart it automatically</option>
				</select>
			</label>
		</div>
	</SettingsSection>

	<SettingsSection title="Files and user" description="The root filesystem is read-only. Volumes keep their files across restarts and appear under Files → volumes; writable paths are small in-memory folders that start empty.">
		<div class="grid gap-5">
			<fieldset class="min-w-0">
				<legend class="label">Volumes</legend>
				<div class="grid gap-2">
					{#each volumes as v, i (i)}
						<div class="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)_auto] items-center gap-2">
							<input class="field font-mono text-[13px]" bind:value={v.name} placeholder="data" aria-label="Volume {i + 1} name" spellcheck="false" />
							<input class="field font-mono text-[13px]" bind:value={v.path} placeholder="/var/lib/app" aria-label="Volume {i + 1} path in the container" spellcheck="false" />
							<button type="button" class="btn btn-quiet btn-icon btn-sm" onclick={() => volumes.splice(i, 1)} aria-label="Remove volume {v.name || i + 1}"><Icon name="trash" size={13} /></button>
						</div>
					{/each}
					<div><button type="button" class="btn btn-sm btn-quiet text-action" onclick={() => volumes.push({ name: '', path: '' })}><Icon name="plus" size={13} />Add a volume</button></div>
				</div>
				<span class="help">Name, then the folder inside the container. Removing a volume keeps its files.</span>
			</fieldset>
			<label class="block">
				<span class="label">Writable paths</span>
				<textarea class="field min-h-20 font-mono text-[13px]" bind:value={writable} spellcheck="false" placeholder="/var/cache/nginx&#10;/var/run"></textarea>
				<span class="help">One folder per line, for images that write caches, sockets or PID files. <code class="font-mono">/tmp</code> is always writable.</span>
			</label>
			<label class="block">
				<span class="label">Run as <span class="font-normal text-muted">(optional)</span></span>
				<input class="field font-mono text-[13px]" bind:value={user} spellcheck="false" placeholder="the panel's container user" />
				<span class="help">A numeric <code class="font-mono">uid:gid</code> such as <code class="font-mono">1000:1000</code>, never root. Images that must start as root need their unprivileged variant (for example <code class="font-mono">nginxinc/nginx-unprivileged</code>).</span>
			</label>
		</div>
	</SettingsSection>

	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	<div class="sticky bottom-0 z-10 flex items-center gap-3 border-t border-rule-soft bg-paper/95 py-3 backdrop-blur-sm">
		<button class="btn btn-primary" disabled={busy || !dirty || (startupChanged && !stopped)}>{busy ? 'Saving…' : 'Save configuration'}</button>
		{#if startupChanged && !stopped}<span class="text-small text-warn">Stop the app to change its command</span>{:else if dirty}<span class="text-small text-warn">Unsaved changes</span>{/if}
	</div>
</form>
