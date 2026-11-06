<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtTime } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { loadExtensions } from '$lib/extensions/store.svelte';
	import type { ExtCapability, ExtCapabilityArea, ExtensionView, ExtSetting, ExtVersion } from '$lib/extensions/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	const id = $derived(page.params.id ?? '');
	let v = $state<ExtensionView | null>(null);
	let error = $state('');
	let busy = $state(false);
	const builtinAdmin = $derived(session.user?.role === 'admin');

	// The version under review: ?version=…, else an available update, else
	// the newest stored version when nothing is installed.
	let reviewVersion = $state('');
	let grants = $state<Record<string, boolean>>({});
	let review = $state<ExtensionView | null>(null);

	async function load() {
		try {
			v = await api<ExtensionView>('GET', `/admin/extensions/${encodeURIComponent(id)}`);
			error = '';
			const asked = page.url.searchParams.get('version') ?? '';
			const target = asked && v.versions.some((x) => x.version === asked && !x.active) ? asked : !v.installed ? v.versions[0]?.version : (v.update_version ?? '');
			reviewVersion = target ?? '';
			review = v;
			grants = Object.fromEntries(v.capabilities.map((c) => [c.name, v!.installed ? c.granted || !c.optional : true]));
			initSettings();
		} catch (e) {
			error = e instanceof ApiError ? (e.status === 404 ? 'This extension is not installed or uploaded.' : e.message) : 'The extension could not be loaded.';
		}
	}
	$effect(() => {
		void id;
		load();
	});

	const reviewing = $derived(v && reviewVersion ? v.versions.find((x) => x.version === reviewVersion) : undefined);
	// The capabilities as the reviewed version declares them.
	$effect(() => {
		const ver = reviewVersion;
		if (!v || !ver) return;
		if (ver === v.version) {
			review = v;
			return;
		}
		api<ExtensionView>('GET', `/admin/extensions/${encodeURIComponent(id)}?version=${encodeURIComponent(ver)}`)
			.then((r) => {
				review = r;
				const granted = new Set(v!.capabilities.filter((c) => c.granted).map((c) => c.name));
				grants = Object.fromEntries(r.capabilities.map((c) => [c.name, !c.optional || !v!.installed || granted.has(c.name)]));
			})
			.catch(() => (review = null));
	});

	async function activate(version: string, caps: string[]) {
		busy = true;
		try {
			v = await api<ExtensionView>('POST', `/admin/extensions/${encodeURIComponent(id)}/activate`, { version, grants: caps });
			toast(`${v.name} ${version} is installed and on`, 'success');
			reviewVersion = '';
			goto(`/admin/extensions/${encodeURIComponent(id)}`, { replaceState: true, noScroll: true });
			await load();
			loadExtensions(true);
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The extension could not be installed.', 'fail');
		} finally {
			busy = false;
		}
	}
	function install() {
		const caps = Object.entries(grants)
			.filter(([, on]) => on)
			.map(([n]) => n);
		activate(reviewVersion, caps);
	}
	async function switchTo(x: ExtVersion) {
		if (!v) return;
		if (x.new_capabilities?.length) {
			reviewVersion = x.version;
			scrollTo({ top: 0, behavior: 'smooth' });
			return;
		}
		const ok = await confirmDialog({
			title: `Switch to ${x.version}?`,
			body: 'The extension keeps its settings and data. If this version stores data differently, it may not understand data written by the current one.',
			confirmLabel: `Use ${x.version}`
		});
		if (ok)
			activate(
				x.version,
				v.capabilities.filter((c) => c.granted).map((c) => c.name)
			);
	}

	async function setOn(on: boolean) {
		busy = true;
		try {
			v = await api<ExtensionView>('POST', `/admin/extensions/${encodeURIComponent(id)}/${on ? 'enable' : 'disable'}`);
			toast(on ? 'Extension turned on' : 'Extension turned off', 'success');
			loadExtensions(true);
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The change could not be saved.', 'fail');
			await load();
		} finally {
			busy = false;
		}
	}

	// Settings form: non-secret values are edited in place; secrets are
	// write-only (a new value replaces the stored one).
	let values = $state<Record<string, unknown>>({});
	let secrets = $state<Record<string, string>>({});
	function initSettings() {
		values = Object.fromEntries((v?.settings ?? []).filter((s) => s.type !== 'secret').map((s) => [s.key, s.value ?? s.default ?? (s.type === 'boolean' ? false : '')]));
		secrets = {};
	}
	async function saveSettings(e: SubmitEvent) {
		e.preventDefault();
		if (!v) return;
		const out: Record<string, unknown> = {};
		for (const s of v.settings) {
			if (s.type === 'secret') {
				if (secrets[s.key]) out[s.key] = secrets[s.key];
				continue;
			}
			const val = values[s.key];
			if (val === '' || val === undefined) {
				if (s.set) out[s.key] = null;
				continue;
			}
			out[s.key] = s.type === 'number' ? Number(val) : val;
		}
		busy = true;
		try {
			v = await api<ExtensionView>('PUT', `/admin/extensions/${encodeURIComponent(id)}/settings`, { values: out });
			initSettings();
			toast('Settings saved', 'success');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The settings could not be saved.', 'fail');
		} finally {
			busy = false;
		}
	}
	async function clearSecret(s: ExtSetting) {
		if (!(await confirmDialog({ title: `Clear ${s.label}?`, body: 'The stored value is deleted. Features that need it stop working until a new value is saved.', confirmLabel: 'Clear', tone: 'danger' }))) return;
		try {
			v = await api<ExtensionView>('PUT', `/admin/extensions/${encodeURIComponent(id)}/settings`, { values: { [s.key]: null } });
			toast('Cleared', 'success');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The setting could not be cleared.', 'fail');
		}
	}

	async function runJob(job: string) {
		busy = true;
		try {
			v = await api<ExtensionView>('POST', `/admin/extensions/${encodeURIComponent(id)}/jobs/${encodeURIComponent(job)}/run`);
			const j = v.jobs.find((x) => x.job === job);
			toast(j?.last_status === 'ok' ? 'Job finished' : `Job ${j?.last_status ?? 'ran'}: ${j?.last_error ?? ''}`, j?.last_status === 'ok' ? 'success' : 'warn');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The job could not run.', 'fail');
		} finally {
			busy = false;
		}
	}

	async function deleteVersion(x: ExtVersion) {
		if (!(await confirmDialog({ title: `Delete version ${x.version}?`, body: 'The stored bundle is removed; you can no longer switch to it without uploading it again.', confirmLabel: 'Delete version', tone: 'danger' }))) return;
		try {
			await api('DELETE', `/admin/extensions/${encodeURIComponent(id)}/versions/${encodeURIComponent(x.version)}`);
			await load();
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The version could not be deleted.', 'fail');
		}
	}

	async function uninstall() {
		if (!v) return;
		const ok = await confirmDialog({
			title: v.installed ? `Uninstall ${v.name}?` : `Remove the uploaded ${v.name}?`,
			body: v.installed
				? `Its settings, stored data, per-bot switches and consents are deleted. ${v.source === 'builtin' ? 'It stays available to install again because it ships with the panel.' : 'Uploaded versions are deleted too.'}`
				: 'The uploaded bundles are deleted.',
			confirmLabel: v.installed ? 'Uninstall' : 'Remove',
			tone: 'danger',
			typeToConfirm: v.installed ? v.id : undefined
		});
		if (!ok) return;
		try {
			await api('DELETE', `/admin/extensions/${encodeURIComponent(id)}`);
			toast(`${v.name} uninstalled`, 'success');
			loadExtensions(true);
			if (v.source === 'builtin') await load();
			else goto('/admin/extensions');
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The extension could not be uninstalled.', 'fail');
		}
	}

	// Permissions are grouped by what they touch, in this order.
	const areas: { id: ExtCapabilityArea; title: string }[] = [
		{ id: 'bots', title: 'Bots' },
		{ id: 'people', title: 'People' },
		{ id: 'internet', title: 'Internet' },
		{ id: 'own', title: 'Its own data' }
	];
	const inArea = (caps: ExtCapability[], a: ExtCapabilityArea) => caps.filter((c) => c.area === a);
	// The hosts the reviewed version may reach with what is ticked now.
	const reach = $derived([...new Set((review?.capabilities ?? []).filter((c) => c.name === 'http.fetch' && grants[c.name]).flatMap((c) => c.hosts ?? []))]);
	// Discord's read-only API reached through the panel (bot.discord_api): the
	// panel makes those calls, but they still leave the server, so they count.
	const viaPanel = $derived(!!review?.capabilities.some((c) => c.name === 'bot.discord_api' && grants[c.name]));
	const stateLabel: Record<string, string> = { enabled: 'On', disabled: 'Off', failed: 'Stopped', available: 'Not installed' };
	const publicURL = (path: string) => `${location.origin}/api/v1/ext-public/${id}${path}`;
	const newCaps = (c: ExtCapability) => !!reviewing?.new_capabilities?.includes(c.name);
	async function copy(text: string) {
		try {
			await navigator.clipboard.writeText(text);
			toast('Copied', 'success');
		} catch {
			toast('Copy failed; select the text instead', 'fail');
		}
	}
</script>

{#snippet capList(caps: ExtCapability[], editable: boolean)}
	<div class="mt-4 grid gap-5">
		{#each areas as a (a.id)}
			{@const group = inArea(caps, a.id)}
			{#if group.length}
				<div>
					<p class="text-small font-medium text-muted">{a.title}</p>
					<ul class="mt-1.5 list-card">
						{#each group as c (c.name)}
							<li class="flex items-start gap-3 px-4 py-3">
								{#if editable}
									<input id="cap-{c.name}" type="checkbox" class="mt-0.5" bind:checked={grants[c.name]} disabled={!c.optional || !builtinAdmin} aria-describedby="cap-{c.name}-guard" />
								{:else}
									<Icon name={c.granted ? 'check' : 'x'} size={15} class="mt-0.5 shrink-0 {c.granted ? 'text-run' : 'text-muted'}" />
								{/if}
								<div class="min-w-0 flex-1">
									<p class="flex flex-wrap items-center gap-x-2 gap-y-1">
										{#if editable}<label for="cap-{c.name}" class="font-medium">{c.label}</label>{:else}<span class="font-medium {c.granted ? '' : 'text-muted'}">{c.label}</span>{/if}
										{#if c.optional}<span class="pill">{editable ? 'Optional' : c.granted ? 'Optional, on' : 'Optional, off'}</span>{/if}
										{#if editable && newCaps(c)}<span class="pill" data-tone="warn">New in this version</span>{/if}
									</p>
									{#if c.reason}<p class="mt-0.5 text-small">{c.reason}</p>{/if}
									{#if c.hosts?.length}<p class="mt-0.5 font-mono text-small text-muted">{c.hosts.join(', ')}</p>{/if}
									{#if c.guard}<p id="cap-{c.name}-guard" class="mt-1 flex items-start gap-1.5 text-small text-muted"><Icon name="lock" size={12} class="mt-[3px] shrink-0" />{c.guard}</p>{/if}
								</div>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
		{/each}
	</div>
{/snippet}

<svelte:head><title>{v?.name ?? 'Extension'} · Extensions · RivetPanel</title></svelte:head>

<a class="link text-small" href="/admin/extensions"><Icon name="chevronLeft" size={12} class="mr-0.5 inline align-[-1px]" />Extensions</a>

{#if error}
	<Notice tone="fail" class="mt-4">{error}</Notice>
{:else if !v}
	<div class="mt-4"><Skeleton rows={6} label="Loading extension" /></div>
{:else}
	<header class="mt-2 flex flex-wrap items-start gap-4">
		<div class="min-w-48 flex-1">
			<div class="flex flex-wrap items-center gap-2">
				<h2 class="text-section">{v.name}</h2>
				<span class="pill" data-tone={v.state === 'enabled' ? 'run' : v.state === 'failed' ? 'fail' : undefined}>{stateLabel[v.state]}</span>
				{#if v.source === 'dev'}<span class="pill" data-tone="warn">Unsigned</span>{/if}
			</div>
			<p class="mt-0.5 text-small text-muted">{v.publisher} · version {v.version} · <code>{v.id}</code>{#if v.homepage}{' '}· <a class="link" href={v.homepage} target="_blank" rel="noopener noreferrer">Homepage</a>{/if}</p>
			<p class="mt-2 max-w-3xl">{v.description}</p>
		</div>
		{#if v.installed && v.state !== 'failed'}
			<div class="shrink-0"><Switch checked={v.state === 'enabled'} disabled={busy} label="Turned on" onchange={() => setOn(v!.state !== 'enabled')} /></div>
		{/if}
	</header>

	{#if v.state === 'failed'}
		<Notice tone="fail" class="mt-4" title="Stopped">
			{v.last_error || 'The extension stopped.'}
			{#snippet action()}<button class="btn btn-sm" disabled={busy} onclick={() => setOn(true)}>Try again</button>{/snippet}
		</Notice>
	{/if}

	{#if reviewing}
		<section class="mt-6 overflow-hidden rounded-card border border-rule bg-panel" aria-labelledby="review-title">
			<div class="px-5 pt-5 pb-4">
				<h3 id="review-title" class="text-section font-semibold">{v.installed ? `Review version ${reviewing.version}` : 'Review and install'}</h3>
				<p class="mt-1 max-w-3xl text-small text-muted">Everything below is enforced by the panel itself, whatever the extension's code tries to do.</p>
				{#if !reviewing.compatible}<Notice tone="fail" class="mt-3">This version does not support this panel version.</Notice>{/if}
			</div>
			<!-- How it is contained: facts about this exact bundle, not promises from its author. -->
			<dl class="grid border-y border-rule-soft bg-paper/40 sm:grid-cols-2 xl:grid-cols-4">
				<div class="contain">
					<dt>Origin</dt>
					<dd class="flex items-center gap-1.5">
						{#if reviewing.source === 'dev'}<Icon name="alert" size={14} class="shrink-0 text-warn" />Unsigned{:else}<Icon name="shield" size={14} class="shrink-0 text-run" />{reviewing.source === 'builtin' ? 'Ships with this panel' : `Signed by ${reviewing.publisher_key_name || 'a trusted key'}`}{/if}
					</dd>
					<p>{reviewing.source === 'dev' ? 'Development panels only.' : reviewing.source === 'builtin' ? 'Part of the panel binary you installed.' : 'Every file is checked against the signature before it runs.'} <span class="font-mono" title="SHA-256 {reviewing.sha256}">{reviewing.sha256.slice(0, 12)}</span></p>
				</div>
				<div class="contain">
					<dt>Sandbox</dt>
					<dd>WebAssembly, {review?.memory_mb ?? v.memory_mb} MiB a copy</dd>
					<p>No files, processes or sockets. At most 4 copies run at once; each call ends after {(review?.timeout_ms ?? v.timeout_ms) / 1000} s.</p>
				</div>
				<div class="contain">
					<dt>Internet</dt>
					<dd class="break-words">{reach.length || viaPanel ? [...new Set([...reach, ...(viaPanel ? ['discord.com'] : [])])].join(', ') : 'No access'}</dd>
					<p>{reach.length ? 'HTTPS to these hosts only, nothing private or local.' : viaPanel ? "Discord's read-only API, called by the panel for it." : 'It cannot connect anywhere.'}</p>
				</div>
				<div class="contain">
					<dt>Removal</dt>
					<dd>Leaves nothing behind</dd>
					<p>Uninstalling deletes its data, settings and grants. Bots keep running.</p>
				</div>
			</dl>
			<div class="px-5 pt-5 pb-5">
				<h4 class="font-medium">What it can do</h4>
				<p class="mt-0.5 text-small text-muted">Granted once here, checked by the panel on every use.{#if v.contributes.bot_enablement}{' '}It touches a bot only after someone who manages that bot's variables turns it on for that bot.{/if}</p>
				{@render capList(review?.capabilities ?? [], true)}
				{#if builtinAdmin}
					<div class="mt-6 flex flex-wrap gap-2">
						<button class="btn btn-primary" disabled={busy || !reviewing.compatible} onclick={install}>{v.installed ? `Switch to ${reviewing.version}` : 'Install and turn on'}</button>
						{#if v.installed}<button class="btn btn-quiet" onclick={() => (reviewVersion = '')}>Not now</button>{/if}
					</div>
				{:else}
					<p class="mt-5 text-small text-muted">Only built-in administrators install extensions and grant permissions.</p>
				{/if}
			</div>
		</section>
	{:else if v.update_version && builtinAdmin}
		<Notice tone="info" class="mt-4" title="Version {v.update_version} is available">
			{#snippet action()}<button class="btn btn-sm" onclick={() => (reviewVersion = v!.update_version!)}>Review update</button>{/snippet}
		</Notice>
	{/if}

	{#if v.installed}
		{#if v.settings.length}
			<SettingsSection title="Settings" description="Secret values are stored encrypted and are write-only: the extension never reads them; the panel attaches them only to requests to the hosts listed.">
				<form class="grid max-w-xl gap-4" onsubmit={saveSettings}>
					{#each v.settings as s (s.key)}
						<div class="grid gap-1">
							{#if s.type === 'boolean'}
								<Switch label={s.label} checked={values[s.key] === true} onchange={() => (values[s.key] = values[s.key] !== true)}>{s.description}</Switch>
							{:else}
								<label class="font-medium" for="set-{s.key}">{s.label}{#if s.required}<span class="text-muted"> (required)</span>{/if}</label>
								{#if s.type === 'enum'}
									<select id="set-{s.key}" class="field" bind:value={values[s.key]}>
										<option value="">Not set</option>
										{#each s.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
									</select>
								{:else if s.type === 'secret'}
									<div class="flex gap-2">
										<input id="set-{s.key}" class="field flex-1" type="password" autocomplete="off" placeholder={s.set ? 'Saved; type to replace' : 'Not set'} bind:value={secrets[s.key]} />
										{#if s.set}<button type="button" class="btn btn-quiet" onclick={() => clearSecret(s)}>Clear</button>{/if}
									</div>
								{:else}
									<input id="set-{s.key}" class="field" type={s.type === 'number' ? 'number' : 'text'} bind:value={values[s.key]} spellcheck="false" />
								{/if}
								{#if s.description}<p class="text-small text-muted">{s.description}{#if s.inject_hosts?.length}{' '}Sent only to {s.inject_hosts.join(', ')}.{/if}</p>{/if}
							{/if}
						</div>
					{/each}
					<div><button class="btn btn-primary" disabled={busy}>Save settings</button></div>
				</form>
			</SettingsSection>
		{/if}

		{#if v.routes.some((r) => r.auth === 'public')}
			<SettingsSection title="Public endpoints" description="Addresses other services call. The panel checks every request's signature before the extension sees it.">
				<ul class="grid min-w-0 grid-cols-1 gap-2">
					{#each v.routes.filter((r) => r.auth === 'public') as r (r.id)}
						<li class="flex min-w-0 items-center gap-2">
							<code class="min-w-0 flex-1 truncate rounded-control bg-paper-2 px-2 py-1 text-small" title={publicURL(r.path)}>{publicURL(r.path)}</code>
							<button class="btn btn-sm" onclick={() => copy(publicURL(r.path))}><Icon name="copy" size={13} />Copy</button>
						</li>
					{/each}
				</ul>
			</SettingsSection>
		{/if}

		{#if v.jobs.length}
			<SettingsSection title="Scheduled jobs">
				<ul class="list-card">
					{#each v.jobs as j (j.job)}
						<li class="flex flex-wrap items-center gap-3 px-4 py-3">
							<div class="min-w-48 flex-1">
								<p class="font-medium"><code>{j.job}</code> {#if j.last_status}<span class="pill" data-tone={j.last_status === 'ok' ? 'run' : j.last_status === 'running' ? undefined : 'fail'}>{j.last_status}</span>{/if}</p>
								<p class="text-small text-muted">{j.last_run_ms ? `Last ran ${fmtAgo(j.last_run_ms)}` : 'Not run yet'} · next {fmtTime(j.next_run_ms)}</p>
								{#if j.last_error}<p class="text-small text-fail">{j.last_error}</p>{/if}
							</div>
							<button class="btn btn-sm" disabled={busy || v.state !== 'enabled'} onclick={() => runJob(j.job)}>Run now</button>
						</li>
					{/each}
				</ul>
			</SettingsSection>
		{/if}

		<SettingsSection title="Permissions" description="Approved at installation and checked on every use. A version that needs more asks again before it runs.">
			{@render capList(v.capabilities, false)}
			<p class="mt-4 text-small text-muted">Runs sandboxed with at most {v.memory_mb} MiB of memory per running copy (at most 4 at once) and {v.timeout_ms / 1000} s per call.{#if v.bots}{' '}Turned on for {v.bots} bot{v.bots === 1 ? '' : 's'}.{/if}</p>
		</SettingsSection>
	{/if}

	<SettingsSection title="Versions" description="The three newest uploads are kept, so you can switch back without uploading again.">
		<ul class="list-card">
			{#each v.versions as x (x.version)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-48 flex-1">
						<p class="font-medium">{x.version} {#if x.active}<span class="pill" data-tone="run">Active</span>{/if}{#if !x.compatible}<span class="pill" data-tone="fail">Incompatible</span>{/if}</p>
						<p class="text-small text-muted">{x.source === 'builtin' ? 'Ships with the panel' : x.source === 'dev' ? 'Unsigned' : `Signed by ${x.publisher_key_name || x.key_id}`} · {fmtTime(x.created_at_ms)}{#if x.new_capabilities?.length && !x.active}{' '}· asks for {x.new_capabilities.length} more capabilit{x.new_capabilities.length === 1 ? 'y' : 'ies'}{/if}</p>
					</div>
					{#if builtinAdmin && !x.active}
						{#if v.installed}<button class="btn btn-sm" disabled={busy || !x.compatible} onclick={() => switchTo(x)}>Switch</button>{:else}<button class="btn btn-sm" onclick={() => (reviewVersion = x.version)}>Review</button>{/if}
						{#if x.source !== 'builtin'}<button class="btn btn-sm btn-quiet" onclick={() => deleteVersion(x)}>Delete</button>{/if}
					{/if}
				</li>
			{/each}
		</ul>
	</SettingsSection>

	{#if builtinAdmin && (v.installed || v.source !== 'builtin')}
		<SettingsSection title={v.installed ? 'Uninstall' : 'Remove'} description="Deletes everything the extension stored. Bots keep running.">
			<button class="btn btn-danger" onclick={uninstall}>{v.installed ? 'Uninstall extension' : 'Remove uploaded bundle'}</button>
		</SettingsSection>
	{/if}
{/if}

<style>
	/* One fact in the containment band: a quiet label, the fact, a line of detail. */
	.contain {
		display: grid;
		align-content: start;
		gap: 0.125rem;
		padding: 0.875rem 1.25rem;
		font-size: var(--text-small);
		line-height: var(--text-small--line-height);
		color: var(--color-muted);
	}
	.contain + .contain {
		border-top: 1px solid var(--color-rule-soft);
	}
	@media (min-width: 40rem) {
		.contain + .contain {
			border-top: 0;
		}
		.contain:nth-child(even) {
			border-left: 1px solid var(--color-rule-soft);
		}
		.contain:nth-child(n + 3) {
			border-top: 1px solid var(--color-rule-soft);
		}
	}
	@media (min-width: 80rem) {
		.contain:nth-child(n + 2) {
			border-left: 1px solid var(--color-rule-soft);
		}
		.contain:nth-child(n + 3) {
			border-top: 0;
		}
	}
	.contain dt {
		font-size: var(--text-label);
		font-weight: 500;
	}
	.contain dd {
		font-size: var(--text-body);
		line-height: var(--text-body--line-height);
		font-weight: 500;
		color: var(--color-ink);
	}
</style>
