<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import type { GameDetail, GameJVM, JVMPreset, Version } from '$lib/api/games';
	import { checkJVMArgs, checkVariable } from '$lib/api/games';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { registerDirty } from '$lib/ui/guard.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let { bot, stopped, onSaved }: { bot: Bot; stopped: boolean; onSaved: (b: Bot) => void } = $props();

	let detail = $state<GameDetail | null>(null);
	const isJava = $derived(!!detail?.spec.images.some((i) => i.java));
	let values = $state<Record<string, string>>({});
	let base = $state('');
	let versions = $state<Record<string, Version[] | 'error'>>({});
	let image = $state('');
	let error = $state('');
	let busy = $state(false);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');
	const canEdit = $derived(can(bot, Perm.env));
	const admin = $derived(can(bot, Perm.admin));

	// JVM arguments (Java server types): saved separately from the settings,
	// editable while the server runs, applied at the next start.
	let jvmArgs = $state('');
	let jvmError = $state('');
	let jvmBusy = $state(false);
	const jvm = $derived(detail?.jvm);
	const normArgs = (s: string) => s.trim().split(/\s+/).filter(Boolean).join(' ');
	const jvmDirty = $derived(!!jvm?.supported && normArgs(jvmArgs) !== jvm.args);
	const jvmProblem = $derived(jvm?.supported ? checkJVMArgs(normArgs(jvmArgs), jvm.max_length) : '');
	// Saved options apply to a container of a later generation; the running
	// one predates them until Start/Restart advances the generation.
	const jvmPending = $derived(
		!!jvm?.supported && jvm.set && bot.observed_state === 'running' && bot.observed_generation <= jvm.saved_generation
	);
	const activePreset = $derived(jvm?.presets.find((p) => p.args === normArgs(jvmArgs)));
	const presetTooNew = (p: JVMPreset) => !!(jvm && p.min_java && jvm.java && jvm.java < p.min_java);

	async function saveJVM(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		if (!jvm?.supported || jvmProblem) return false;
		jvmBusy = true;
		jvmError = '';
		try {
			const r = await api<{ bot: Bot; jvm: GameJVM }>('PUT', `/bots/${bot.id}/game/jvm-args`, { args: normArgs(jvmArgs) });
			onSaved(r.bot);
			if (detail) detail.jvm = r.jvm;
			jvmArgs = r.jvm.args;
			toast(stopped ? 'JVM arguments saved. They apply on the next start.' : 'JVM arguments saved. Restart the server to apply them.');
			return true;
		} catch (err) {
			jvmError = msg(err);
			return false;
		} finally {
			jvmBusy = false;
		}
	}

	async function restartNow() {
		jvmBusy = true;
		try {
			onSaved(await api<Bot>('POST', `/bots/${bot.id}/restart`));
			toast('Restarting with the new JVM arguments.');
		} catch (err) {
			jvmError = msg(err);
		} finally {
			jvmBusy = false;
		}
	}

	async function load() {
		try {
			detail = await api<GameDetail>('GET', `/bots/${bot.id}/game`);
			jvmArgs = detail.jvm?.args ?? '';
			values = Object.fromEntries(detail.variables.map((v) => [v.env, v.value]));
			base = JSON.stringify(values);
			image = bot.image_choice ?? '';
			for (const v of detail.variables) {
				if (v.versions && !versions[v.env]) {
					api<{ versions: Version[] }>('GET', `/blueprints/${detail.blueprint.slug}/versions/${v.env}`)
						.then((r) => (versions[v.env] = r.versions))
						.catch(() => (versions[v.env] = 'error'));
				}
			}
		} catch (e) {
			error = msg(e);
		}
	}
	onMount(() => {
		load();
		const a = registerDirty({ label: 'Server settings', isDirty: () => dirty, save: async () => await save() });
		const b = registerDirty({ label: 'JVM arguments', isDirty: () => jvmDirty, save: async () => await saveJVM() });
		return () => {
			a();
			b();
		};
	});

	const dirty = $derived(!!detail && JSON.stringify(values) !== base);
	const problems = $derived.by(() => {
		const p: Record<string, string> = {};
		for (const v of detail?.variables ?? []) {
			if (!v.editable) continue;
			const e = checkVariable(v, values[v.env] ?? '');
			if (e) p[v.env] = e;
		}
		return p;
	});
	const reinstallOnSave = $derived(!!detail && detail.variables.some((v) => v.reinstall && values[v.env] !== v.value));

	async function save(e?: SubmitEvent): Promise<boolean> {
		e?.preventDefault();
		if (!detail || Object.keys(problems).length) return false;
		const changed: Record<string, string> = {};
		for (const v of detail.variables) if (v.editable && values[v.env] !== v.value) changed[v.env] = values[v.env];
		if (!Object.keys(changed).length) return true;
		busy = true;
		error = '';
		try {
			const b = await api<Bot>('PUT', `/bots/${bot.id}/game/variables`, { variables: changed });
			onSaved(b);
			toast(reinstallOnSave ? 'Saved. The new version is installed on the next start; your worlds and settings stay.' : 'Settings saved. They apply on the next start.');
			await load();
			return true;
		} catch (err) {
			error = msg(err);
			return false;
		} finally {
			busy = false;
		}
	}

	async function saveImage() {
		busy = true;
		try {
			const b = await api<Bot>('PUT', `/bots/${bot.id}/game/image`, { image });
			onSaved(b);
			const im = detail?.spec.images.find((i) => i.label === image);
			if (detail?.jvm && im?.java) detail.jvm.java = im.java;
			toast(image ? `The server now uses ${image}.` : 'The Java version is chosen automatically at the next installation.');
		} catch (err) {
			error = msg(err);
		} finally {
			busy = false;
		}
	}

	async function reinstall() {
		const ok = await confirmDialog({
			title: `Reinstall ${bot.name}?`,
			body: 'The server software is downloaded and installed again on the next start. Worlds, plugins, mods and settings in the server files are kept, but the installer may replace the server jar and its libraries.',
			confirmLabel: 'Reinstall on next start'
		});
		if (!ok) return;
		busy = true;
		try {
			onSaved(await api<Bot>('POST', `/bots/${bot.id}/game/reinstall`));
			toast('The server is reinstalled the next time it starts.');
		} catch (err) {
			error = msg(err);
		} finally {
			busy = false;
		}
	}

	async function upgrade() {
		busy = true;
		try {
			onSaved(await api<Bot>('POST', `/bots/${bot.id}/game/upgrade`));
			toast('The server now uses the newest server-type definition.');
			await load();
		} catch (err) {
			error = msg(err);
		} finally {
			busy = false;
		}
	}
</script>

{#if !detail}
	{#if error}<Notice tone="fail">{error}</Notice>{:else}<Skeleton rows={4} label="Loading server settings" />{/if}
{:else}
	<div class="grid max-w-3xl grid-cols-[minmax(0,1fr)] gap-6">
		<section class="card grid grid-cols-[minmax(0,1fr)] gap-3 p-5">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div>
					<p class="eyebrow">Server type</p>
					<p class="mt-1 text-title font-semibold">{detail.blueprint.name} <span class="text-small font-normal text-muted">· {detail.blueprint.category} · revision {bot.blueprint_revision}</span></p>
					<p class="mt-1 text-small text-muted">{detail.spec.description}</p>
				</div>
				<span class="pill" data-tone={bot.install_state === 'installed' ? 'run' : bot.install_state === 'failed' ? 'fail' : 'warn'}>
					{bot.install_state === 'installed' ? 'Installed' : bot.install_state === 'failed' ? 'Installation failed' : bot.install_state === 'installing' ? 'Installing' : 'Installs on next start'}
				</span>
			</div>
			{#if detail.update_available && admin}
				<Notice tone="info" title="A newer definition of this server type is available">
					It may add settings or change how the server starts. Your files are not changed.
					{#snippet action()}<button class="btn btn-sm" disabled={!stopped || busy} onclick={upgrade}>Update to revision {detail?.blueprint.current_revision}</button>{/snippet}
				</Notice>
			{/if}
			<div>
				<p class="eyebrow">Startup command</p>
				<code class="mt-1 block overflow-x-auto rounded-control bg-paper-2 px-3 py-2 font-mono text-small whitespace-pre">{detail.startup}</code>
				<p class="help mt-1">Set by the server type. <code>$&#123;SERVER_MEMORY&#125;</code> is {detail.spec.resources.heap_percent ?? 85}% of the memory limit; <code>$&#123;SERVER_PORT&#125;</code> is the primary allocation.</p>
			</div>
		</section>

		<form class="card grid gap-4 p-5" onsubmit={save}>
			<h2 class="text-section font-semibold">Settings</h2>
			<fieldset class="grid gap-4" disabled={!stopped || !canEdit}>
				{#each detail.variables as v (v.env)}
					{@const list = versions[v.env]}
					<label class="block">
						<span class="label">{v.name} <code class="ml-1 text-[11px] font-normal text-muted">{v.env}</code></span>
						{#if !v.editable}
							<input class="field font-mono" value={v.value} readonly />
						{:else if v.versions && Array.isArray(list)}
							<select class="field" bind:value={values[v.env]}>
								<option value="latest">Latest stable{list[0] ? ` (${list[0].id})` : ''}</option>
								{#if values[v.env] !== 'latest' && !list.some((x) => x.id === values[v.env])}<option value={values[v.env]}>{values[v.env]}</option>{/if}
								{#each list as x (x.id)}<option value={x.id}>{x.id}</option>{/each}
							</select>
						{:else if v.rules.type === 'enum'}
							<select class="field" bind:value={values[v.env]}>{#each v.rules.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}</select>
						{:else if v.rules.type === 'bool'}
							<select class="field" bind:value={values[v.env]}><option value="true">Yes</option><option value="false">No</option></select>
						{:else}
							<input class="field font-mono" bind:value={values[v.env]} aria-invalid={problems[v.env] ? 'true' : undefined} />
						{/if}
						<span class="help {problems[v.env] ? 'text-fail' : ''}">{problems[v.env] ?? v.description ?? ''}{v.reinstall && !problems[v.env] ? ' Changing it reinstalls the server software; worlds and settings stay.' : ''}</span>
					</label>
				{/each}
			</fieldset>
			{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
			{#if canEdit}
				<div class="flex flex-wrap items-center gap-2">
					<button class="btn btn-primary" disabled={!stopped || !dirty || busy || Object.keys(problems).length > 0}>Save settings</button>
					{#if reinstallOnSave}<span class="text-small text-warn">Saving reinstalls the server software on the next start.</span>{/if}
				</div>
			{/if}
		</form>

		{#if jvm?.supported}
			<form class="card grid gap-4 p-5" onsubmit={saveJVM} aria-labelledby="jvm-title">
				<div class="flex flex-wrap items-start justify-between gap-3">
					<div>
						<h2 id="jvm-title" class="text-section font-semibold">JVM arguments</h2>
						<p class="mt-1 text-small text-muted">
							Extra options passed to <code>java</code> as <code>$&#123;SERVER_JVM_ARGS&#125;</code>, after the heap size. The heap
							(<code>-Xmx{jvm.heap_mib}M</code>) follows the memory limit and cannot be set here.
						</p>
					</div>
					<div class="flex flex-wrap items-center gap-2">
						<span class="pill" title="The Java version of the server's image">{jvm.java ? `Java ${jvm.java}` : 'Java chosen at installation'}</span>
						{#if jvmPending}<span class="pill" data-tone="warn">Restart to apply</span>{/if}
					</div>
				</div>
				<fieldset class="grid gap-3" disabled={!canEdit || jvmBusy}>
					<div class="flex flex-wrap items-center gap-2" role="group" aria-label="Presets">
						<span class="text-small text-muted">Presets</span>
						{#each jvm.presets as p (p.id)}
							<button
								type="button"
								class="btn btn-sm"
								aria-pressed={activePreset?.id === p.id}
								title={p.note ?? ''}
								onclick={() => {
									jvmArgs = p.args;
									jvmError = '';
								}}>{p.name}</button
							>
						{/each}
					</div>
					{#if activePreset?.note}<p class="help -mt-1">{activePreset.note}</p>{/if}
					{#if activePreset && presetTooNew(activePreset)}
						<Notice tone="warn">{activePreset.name} needs Java {activePreset.min_java} or newer; this server uses Java {jvm.java}. Choose a newer Java image below, or the server will not start.</Notice>
					{:else if activePreset?.min_java && !jvm.java && activePreset.min_java > 8}
						<Notice tone="info">{activePreset.name} needs Java {activePreset.min_java} or newer. The Java version is chosen at installation; choose a Java image below if you need to be sure.</Notice>
					{/if}
					<label class="block">
						<span class="label">Options <code class="ml-1 text-[11px] font-normal text-muted">SERVER_JVM_ARGS</code></span>
						<textarea
							class="field min-h-24 font-mono text-small"
							rows="4"
							spellcheck="false"
							autocomplete="off"
							bind:value={jvmArgs}
							oninput={() => (jvmError = '')}
							placeholder="None"
							aria-invalid={jvmProblem || jvmError ? 'true' : undefined}
							aria-describedby="jvm-help"
						></textarea>
						<span id="jvm-help" class="help {jvmProblem ? 'text-fail' : ''}"
							>{jvmProblem ||
								`Separate options with spaces. Allowed: -XX:…, -X…, -D…, --add-opens=…, --add-modules=…, --enable-preview and -javaagent: with a jar in the server's files.${jvm.default ? ` Server type default: ${jvm.default}.` : ''}`}</span
						>
					</label>
				</fieldset>
				{#if jvmError}<Notice tone="fail" live>{jvmError}</Notice>{/if}
				{#if canEdit}
					<div class="flex flex-wrap items-center gap-2">
						<button class="btn btn-primary" disabled={!jvmDirty || jvmBusy || !!jvmProblem}>Save JVM arguments</button>
						{#if jvmPending}
							<span class="text-small text-warn">The server is running with its previous JVM arguments.</span>
							{#if can(bot, Perm.power)}<button type="button" class="btn btn-sm" disabled={jvmBusy} onclick={restartNow}><Icon name="restart" size={13} />Restart now</button>{/if}
						{:else}
							<span class="text-small text-muted">Applies the next time the server starts.</span>
						{/if}
					</div>
				{/if}
			</form>
		{:else if isJava && detail.update_available && admin}
			<Notice tone="info" title="JVM arguments">Update this server to the newest server-type definition (above) to set extra JVM arguments such as Aikar's flags.</Notice>
		{/if}

		{#if admin}
			<section class="card grid gap-4 p-5">
				<h2 class="text-section font-semibold">{isJava ? 'Java and installation' : 'Image and installation'}</h2>
				<label class="block max-w-md">
					<span class="label">{isJava ? 'Java image' : 'Image'}</span>
					<div class="flex gap-2">
						<select class="field" bind:value={image} disabled={!stopped}>
							<option value="">Automatic{bot.image_choice ? '' : ' (decided at installation)'}</option>
							{#each detail.spec.images as im (im.label)}<option value={im.label}>{im.label}</option>{/each}
						</select>
						<button class="btn" disabled={!stopped || busy || image === (bot.image_choice ?? '')} onclick={saveImage}>Apply</button>
					</div>
					<span class="help">{isJava ? 'Automatic picks the Java version the Minecraft version needs. Choose one yourself if a mod needs a specific Java.' : 'Automatic uses the server type\'s first image.'}{detail.spec.install?.steamcmd ? ' Reinstall downloads the current Steam build again with SteamCMD.' : ''}</span>
				</label>
				<div class="flex flex-wrap items-center gap-3">
					<button class="btn" disabled={!stopped || busy} onclick={reinstall}><Icon name="restart" size={14} />Reinstall</button>
					<span class="text-small text-muted">Downloads and installs the server software again on the next start. Worlds and settings stay.</span>
				</div>
			</section>
		{/if}
	</div>
{/if}
