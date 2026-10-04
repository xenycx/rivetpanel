<script lang="ts">
	import { onMount } from 'svelte';
	import { ApiError, fetchRevision, putText } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot, onOpenFiles }: { bot: Bot; onOpenFiles: () => void } = $props();

	type Field = { key: string; label: string; help?: string; type: 'text' | 'number' | 'bool' | 'select'; options?: string[]; min?: number; max?: number };
	// The settings people change most, grouped like the game's own menus.
	const groups: { title: string; fields: Field[] }[] = [
		{
			title: 'General',
			fields: [
				{ key: 'motd', label: 'Message of the day', type: 'text', help: 'Shown under the server name in the multiplayer list. § colour codes work.' },
				{ key: 'max-players', label: 'Maximum players', type: 'number', min: 1, max: 1000 },
				{ key: 'online-mode', label: 'Online mode', type: 'bool', help: 'Check accounts with Mojang. Turn off only behind a proxy such as Velocity.' },
				{ key: 'white-list', label: 'Whitelist', type: 'bool', help: 'Only players on the whitelist can join (manage it on the Players tab).' },
				{ key: 'enforce-whitelist', label: 'Kick players not on the whitelist', type: 'bool' }
			]
		},
		{
			title: 'Gameplay',
			fields: [
				{ key: 'gamemode', label: 'Default game mode', type: 'select', options: ['survival', 'creative', 'adventure', 'spectator'] },
				{ key: 'force-gamemode', label: 'Force the game mode on join', type: 'bool' },
				{ key: 'difficulty', label: 'Difficulty', type: 'select', options: ['peaceful', 'easy', 'normal', 'hard'] },
				{ key: 'hardcore', label: 'Hardcore', type: 'bool' },
				{ key: 'pvp', label: 'Player versus player', type: 'bool' },
				{ key: 'allow-flight', label: 'Allow flight', type: 'bool', help: 'Needed by some mods and plugins; otherwise flying players are kicked.' },
				{ key: 'spawn-protection', label: 'Spawn protection radius', type: 'number', min: 0, max: 1000 }
			]
		},
		{
			title: 'World',
			fields: [
				{ key: 'level-name', label: 'World folder', type: 'text', help: 'Changing it starts (or loads) a different world.' },
				{ key: 'level-seed', label: 'Seed', type: 'text', help: 'Only used when a new world is generated.' },
				{ key: 'level-type', label: 'World type', type: 'text', help: 'For example minecraft:normal, minecraft:flat, minecraft:large_biomes.' },
				{ key: 'generate-structures', label: 'Generate structures', type: 'bool' },
				{ key: 'allow-nether', label: 'Allow the Nether', type: 'bool' }
			]
		},
		{
			title: 'Performance',
			fields: [
				{ key: 'view-distance', label: 'View distance (chunks)', type: 'number', min: 2, max: 32 },
				{ key: 'simulation-distance', label: 'Simulation distance (chunks)', type: 'number', min: 2, max: 32 },
				{ key: 'max-tick-time', label: 'Watchdog (ms, -1 = off)', type: 'number', min: -1 }
			]
		}
	];
	// Written by the panel before every start (the allocation decides them).
	const managed = ['server-port', 'query.port', 'server-ip'];

	let text = $state<string | null>(null);
	let etag = $state<string | null>(null);
	let values = $state<Record<string, string>>({});
	let base = $state('');
	let missing = $state(false);
	let error = $state('');
	let saving = $state(false);
	const canEdit = $derived(can(bot, Perm.files));
	const dirty = $derived(text !== null && JSON.stringify(values) !== base);
	const path = $derived(`/bots/${bot.id}/files/content?path=server.properties`);

	function parse(src: string): Record<string, string> {
		const out: Record<string, string> = {};
		for (const line of src.split('\n')) {
			const t = line.trimStart();
			if (!t || t.startsWith('#') || t.startsWith('!')) continue;
			const i = t.search(/[=:]/);
			if (i < 0) continue;
			out[t.slice(0, i).trim()] = t.slice(i + 1).trim().replace(/\\(.)/g, '$1');
		}
		return out;
	}
	const escape = (v: string) => v.replace(/\\/g, '\\\\').replace(/\r?\n/g, ' ');

	async function load() {
		try {
			const r = await fetchRevision(path);
			text = r.text;
			etag = r.etag;
			values = parse(r.text);
			base = JSON.stringify(values);
			missing = false;
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) {
				missing = true;
				text = '';
			} else error = e instanceof ApiError ? e.message : 'server.properties could not be read.';
		}
	}
	onMount(() => {
		load();
		return registerDirty({ label: 'Server properties', isDirty: () => dirty, save: async () => await save() });
	});

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		if (text === null) return false;
		saving = true;
		error = '';
		try {
			// Replace changed keys in place; keep comments, order and everything else.
			const pending = { ...values };
			const lines = text.split('\n').map((line) => {
				const t = line.trimStart();
				const i = t.search(/[=:]/);
				if (!t || t.startsWith('#') || t.startsWith('!') || i < 0) return line;
				const k = t.slice(0, i).trim();
				if (!(k in pending)) return line;
				const v = pending[k];
				delete pending[k];
				return `${k}=${escape(v)}`;
			});
			for (const [k, v] of Object.entries(pending)) if (v !== '') lines.splice(lines.length && lines[lines.length - 1] === '' ? lines.length - 1 : lines.length, 0, `${k}=${escape(v)}`);
			const next = lines.join('\n');
			etag = await putText(path, next, { ifMatch: etag });
			text = next;
			base = JSON.stringify(values);
			toast(bot.observed_state === 'running' ? 'Saved. Restart the server to apply the changes.' : 'Saved. The changes apply at the next start.');
			return true;
		} catch (err) {
			error = err instanceof ApiError && err.status === 412 ? 'The file changed since you opened it (the server rewrites it while running). Reload and try again.' : err instanceof ApiError ? err.message : 'Saving failed.';
			return false;
		} finally {
			saving = false;
		}
	}
	const boolOf = (k: string) => values[k] === 'true';
</script>

{#if text === null}
	{#if error}<Notice tone="fail">{error}</Notice>{:else}<Skeleton rows={6} label="Loading server.properties" />{/if}
{:else if missing}
	<Notice tone="info" title="No server.properties yet">The server creates it the first time it starts. Start the server once, then come back to change its settings here.</Notice>
{:else}
	<form class="grid max-w-3xl gap-5" onsubmit={save}>
		<p class="text-muted">These are the most-used settings from <code>server.properties</code>. Everything else in the file is kept; edit it directly in <button type="button" class="link" onclick={onOpenFiles}>Files</button>. The server reads the file when it starts.</p>
		<fieldset class="contents" disabled={!canEdit}>
			{#each groups as g (g.title)}
				<section class="card grid gap-4 p-5">
					<h2 class="text-section font-semibold">{g.title}</h2>
					<div class="grid gap-4 sm:grid-cols-2">
						{#each g.fields as f (f.key)}
							{#if f.type === 'bool'}
								<label class="flex items-start gap-2.5 sm:col-span-2">
									<input type="checkbox" class="mt-1" checked={boolOf(f.key)} onchange={(e) => (values[f.key] = (e.currentTarget as HTMLInputElement).checked ? 'true' : 'false')} />
									<span>{f.label} <code class="text-[11px] text-muted">{f.key}</code>{#if f.help}<span class="help">{f.help}</span>{/if}</span>
								</label>
							{:else}
								<label class="block {f.key === 'motd' ? 'sm:col-span-2' : ''}">
									<span class="label">{f.label} <code class="text-[11px] font-normal text-muted">{f.key}</code></span>
									{#if f.type === 'select'}
										<select class="field" bind:value={values[f.key]}>
											{#if values[f.key] && !f.options?.includes(values[f.key])}<option value={values[f.key]}>{values[f.key]}</option>{/if}
											{#each f.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
										</select>
									{:else if f.type === 'number'}
										<input class="field" type="number" min={f.min} max={f.max} bind:value={values[f.key]} />
									{:else}
										<input class="field" bind:value={values[f.key]} maxlength="300" />
									{/if}
									{#if f.help}<span class="help">{f.help}</span>{/if}
								</label>
							{/if}
						{/each}
					</div>
				</section>
			{/each}
		</fieldset>
		<p class="text-small text-muted">{managed.join(', ')} are set by the panel from the server's primary allocation before every start.</p>
		{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
		{#if canEdit}
			<div class="flex gap-2">
				<button class="btn btn-primary" disabled={!dirty || saving}>Save properties</button>
				<button type="button" class="btn" onclick={load} disabled={saving}>Reload</button>
			</div>
		{/if}
	</form>
{/if}
