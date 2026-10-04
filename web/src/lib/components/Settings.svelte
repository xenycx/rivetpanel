<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes, MiB } from '$lib/api/client';
	import type { Bot, Limits, RuntimeInfo } from '$lib/api/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import LogoEditor from '$lib/components/LogoEditor.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notifications from '$lib/components/Notifications.svelte';
	import { session } from '$lib/session.svelte';
	import { creatable, loadWorkspaces, workspaceName } from '$lib/workspaces.svelte';

	let { bot, stopped, onSaved }: { bot: Bot; stopped: boolean; onSaved: (b: Bot) => void } = $props();
	const isGame = $derived(bot.kind === 'game');
	const noun = $derived(isGame ? 'server' : 'bot');

	// Form state starts from the bot once; saving replaces the bot and resets it.
	const initial = () => ({ name: bot.name, memoryMiB: Math.round(bot.memory_bytes / MiB), cpus: bot.nano_cpus / 1e9, pids: bot.pids_limit });
	let form = $state(initial());
	let base = $state(initial());
	let limits = $state<Limits | null>(null);
	let rt = $state<RuntimeInfo | null>(null);
	let error = $state('');
	let saving = $state(false);
	// svelte-ignore state_referenced_locally
	let tagText = $state(bot.tags.join(', '));
	let tagError = $state('');
	// svelte-ignore state_referenced_locally
	let workspaceId = $state(bot.workspace_id);
	$effect(() => {
		loadWorkspaces();
	});

	let logoBusy = $state(false);
	async function logo(method: 'PUT' | 'DELETE' | 'POST', body?: unknown) {
		logoBusy = true;
		try {
			const path = method === 'POST' ? `/bots/${bot.id}/logo/discord` : `/bots/${bot.id}/logo`;
			const b = await api<Bot>(method, path, body);
			onSaved({ ...bot, logo_url: b.logo_url, custom_logo: b.custom_logo, discord_avatar_url: b.discord_avatar_url, discord_username: b.discord_username });
			toast(method === 'POST' ? `Got the avatar of ${b.discord_username || 'the bot'} from Discord` : method === 'PUT' ? 'Logo saved' : 'Custom logo removed', 'success');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The logo could not be changed.', 'fail');
		} finally {
			logoBusy = false;
		}
	}

	async function moveWorkspace(e: SubmitEvent) {
		e.preventDefault();
		try {
			const b = await api<Bot>('PUT', `/bots/${bot.id}/workspace`, { workspace_id: workspaceId });
			onSaved({ ...bot, workspace_id: b.workspace_id });
			await loadWorkspaces();
			toast(`Moved to ${workspaceName(b.workspace_id) || 'the workspace'}`, 'success');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : `The ${noun} could not be moved.`, 'fail');
		}
	}

	async function saveTags(e: SubmitEvent) {
		e.preventDefault();
		tagError = '';
		try {
			const r = await api<{ tags: string[] }>('PUT', `/bots/${bot.id}/tags`, { tags: tagText.split(/[,\s]+/).filter(Boolean) });
			tagText = r.tags.join(', ');
			onSaved({ ...bot, tags: r.tags });
			toast(r.tags.length ? 'Tags saved' : 'Tags removed');
		} catch (err) {
			tagError = err instanceof ApiError ? err.message : 'The tags could not be saved.';
		}
	}

	onMount(() => {
		api<{ runtimes: RuntimeInfo[]; limits: Limits }>('GET', '/runtimes')
			.then((r) => {
				limits = r.limits;
				rt = r.runtimes.find((x) => x.id === bot.runtime) ?? null;
			})
			.catch(() => {});
		return registerDirty({ label: bot.kind === 'game' ? 'Server settings' : 'Bot settings', isDirty: () => dirty, save: async () => await save() });
	});

	const dirty = $derived(JSON.stringify(form) !== JSON.stringify(base));
	const minMem = $derived(Math.max(limits?.min_memory_bytes ?? 0, rt?.min_memory_bytes ?? 0));

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		error = '';
		saving = true;
		try {
			const b = await api<Bot>('PATCH', `/bots/${bot.id}`, {
				name: form.name,
				memory_bytes: Math.round(form.memoryMiB * MiB),
				nano_cpus: Math.round(form.cpus * 1e9),
				pids_limit: form.pids
			});
			onSaved(b);
			base = { ...form };
			toast('Settings saved');
			return true;
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The settings could not be saved.';
			return false;
		} finally {
			saving = false;
		}
	}

	async function remove() {
		const ok = await confirmDialog({
			title: `Delete ${bot.name}?`,
			body: isGame
				? 'The container, the world and every other file of the server, its backups and history are removed permanently, and its ports go back to the pool. This cannot be undone.'
				: 'The container, every file in the workspace, its environment variables, backups and history are removed permanently. This cannot be undone.',
			confirmLabel: `Delete ${noun} permanently`,
			tone: 'danger',
			typeToConfirm: bot.name
		});
		if (!ok) return;
		try {
			await api('DELETE', `/bots/${bot.id}`);
			toast(`Deleted ${bot.name}`);
			await goto(isGame ? '/servers' : '/dashboard');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : `The ${noun} could not be deleted.`, 'fail');
		}
	}
</script>

<SettingsSection title="Name and resources" description="The host enforces these limits. A {noun} that uses more memory than its limit is stopped and restarted according to its restart policy. Changes need a stopped {noun}.">
	<form onsubmit={save}>
		<fieldset class="grid min-w-0 gap-5" disabled={!stopped}>
			<label class="block">
				<span class="label">Name</span>
				<input class="field" required maxlength="64" bind:value={form.name} />
			</label>
			<div class="grid gap-4 sm:grid-cols-3">
				<label class="block">
					<span class="label">Memory</span>
					<span class="flex items-center gap-2"><input class="field" type="number" step="any" min="1" bind:value={form.memoryMiB} /><span class="text-muted">MiB</span></span>
					{#if limits}<span class="help">{fmtBytes(minMem)} to {fmtBytes(limits.max_memory_bytes)}</span>{/if}
				</label>
				<label class="block">
					<span class="label">CPU</span>
					<span class="flex items-center gap-2"><input class="field" type="number" step="any" min="0.01" bind:value={form.cpus} /><span class="text-muted">cores</span></span>
					{#if limits}<span class="help">{limits.min_nano_cpus / 1e9} to {limits.max_nano_cpus / 1e9} cores</span>{/if}
				</label>
				<label class="block">
					<span class="label">Processes</span>
					<input class="field" type="number" step="1" min="1" max="4096" bind:value={form.pids} />
					<span class="help">1 to 4096 at once</span>
				</label>
			</div>
			{#if rt?.has_build && !isGame}<p class="text-small text-muted">Builds run separately with up to {fmtBytes(Math.max(rt.build_memory_bytes, form.memoryMiB * MiB))}.</p>{/if}
		</fieldset>
		{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
		<div class="mt-5 flex flex-wrap items-center gap-3">
			<button class="btn btn-primary" disabled={!stopped || !dirty || saving}>Save settings</button>
			{#if dirty}<button type="button" class="btn btn-quiet" onclick={() => (form = { ...base })}>Discard changes</button><span class="text-small text-warn">Unsaved changes</span>{/if}
		</div>
	</form>
</SettingsSection>

{#if session.features.health}<Notifications {bot} />{/if}

{#if isGame}
	<SettingsSection title="Logo" description="Shown in the server list, the sidebar and the server's header. Without a custom logo the server type's icon is used.">
		<LogoEditor src={bot.logo_url} custom={bot.custom_logo} fallbackLabel={bot.name.slice(0, 2).toUpperCase()} busy={logoBusy}
			onUpload={(image) => logo('PUT', { image })} onRemove={() => logo('DELETE')} />
	</SettingsSection>
{:else}
	<SettingsSection title="Logo" description="Shown in the bot list, the sidebar and on the bot's public page. Without a custom logo the bot's Discord avatar is used: it arrives through the telemetry SDK, or you can get it now with the bot's token.">
		<LogoEditor src={bot.logo_url} custom={bot.custom_logo} fallbackLabel={bot.name.slice(0, 2).toUpperCase()} busy={logoBusy}
			onUpload={(image) => logo('PUT', { image })} onRemove={() => logo('DELETE')}>
			<button type="button" class="btn btn-sm" disabled={logoBusy} onclick={() => logo('POST')} title="Uses the bot token stored in this bot's variables"><Icon name="discord" size={14} />Get avatar from Discord</button>
		</LogoEditor>
		{#if bot.discord_username}<p class="mt-2 text-small text-muted">Discord user: {bot.discord_username}</p>{/if}
	</SettingsSection>
{/if}

<SettingsSection title="Tags" description={isGame ? 'Group servers on the Servers page, for example by community, game mode or environment. Everyone with access sees them.' : 'Group bots on the Bots page, for example by team, server or environment. Everyone with access sees them.'}>
	<form class="flex flex-wrap items-start gap-2" onsubmit={saveTags}>
		<label class="block min-w-0 flex-1 basis-64">
			<span class="sr-only">Tags</span>
			<input class="field" bind:value={tagText} placeholder="music, production" aria-invalid={tagError ? 'true' : undefined} aria-describedby="tags-help" />
			<span id="tags-help" class="help {tagError ? 'text-fail!' : ''}">{tagError || 'Separate with commas. Up to 8 lowercase words.'}</span>
		</label>
		<button class="btn">Save tags</button>
	</form>
</SettingsSection>

{#if (bot.permissions & 16) !== 0 && creatable().length > 1}
	<SettingsSection title="Workspace" description="Everyone in the workspace can see this {noun} and act on it according to their role. Moving it changes who has access; per-{noun} sharing stays.">
		<form class="flex flex-wrap items-end gap-2" onsubmit={moveWorkspace}>
			<label class="block min-w-0 flex-1 basis-64"><span class="sr-only">Workspace</span>
				<select class="field" bind:value={workspaceId}>{#each creatable() as w (w.id)}<option value={w.id}>{w.personal ? 'Personal' : w.name}</option>{/each}</select>
			</label>
			<button class="btn" disabled={workspaceId === bot.workspace_id}>Move {noun}</button>
		</form>
	</SettingsSection>
{/if}

{#if !bot.shared}
	<SettingsSection title="Delete this {noun}" description={isGame ? 'Removes the container, the world and all other files, backups and history, and releases its ports.' : 'Removes the container, all files, environment variables, backups and history.'}>
		<div class="flex flex-wrap items-center justify-between gap-3 rounded-tile border border-fail/30 bg-fail/5 px-4 py-3">
			<p class="min-w-0 flex-1 basis-64 text-small">This cannot be undone. Create and download a backup first if you might need it.</p>
			<button class="btn btn-danger" onclick={remove}>Delete {noun}…</button>
		</div>
	</SettingsSection>
{/if}
