<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { fmtAgo, fmtTime } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { loadExtensions } from '$lib/extensions/store.svelte';
	import type { ExtCapability, ExtensionView, ExtSetting, ExtVersion } from '$lib/extensions/types';
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

	const riskTone: Record<string, string | undefined> = { high: 'fail', medium: 'warn', low: undefined };
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

<svelte:head><title>{v?.name ?? 'Extension'} · Extensions · RivetPanel</title></svelte:head>

<a class="link text-small" href="/admin/extensions"><Icon name="chevronLeft" size={12} class="mr-0.5 inline align-[-1px]" />Extensions</a>

{#if error}
	<Notice tone="fail" class="mt-4">{error}</Notice>
{:else if !v}
	<div class="mt-4"><Skeleton rows={6} label="Loading extension" /></div>
{:else}
	<header class="mt-2 flex flex-wrap items-start gap-4">
		<div class="min-w-0 flex-1">
			<div class="flex flex-wrap items-center gap-2">
				<h2 class="text-section">{v.name}</h2>
				<span class="pill" data-tone={v.state === 'enabled' ? 'run' : v.state === 'failed' ? 'fail' : undefined}>{stateLabel[v.state]}</span>
				{#if v.source === 'dev'}<span class="pill" data-tone="warn">Unsigned</span>{/if}
			</div>
			<p class="mt-0.5 text-small text-muted">{v.publisher} · version {v.version} · <code>{v.id}</code>{#if v.homepage} · <a class="link" href={v.homepage} target="_blank" rel="noopener noreferrer">Homepage</a>{/if}</p>
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
		<section class="mt-6 rounded-card border border-rule bg-panel p-5" aria-labelledby="review-title">
			<h3 id="review-title" class="text-title font-semibold">{v.installed ? `Review version ${reviewing.version}` : 'Review and install'}</h3>
			<p class="mt-1 text-small text-muted">
				{reviewing.source === 'builtin' ? 'Ships with this panel.' : reviewing.source === 'dev' ? 'Unsigned development bundle.' : `Signed by ${reviewing.publisher_key_name || 'key'} ${reviewing.key_id}.`}
				SHA-256 <code class="break-all">{reviewing.sha256.slice(0, 16)}…</code>
			</p>
			{#if !reviewing.compatible}<Notice tone="fail" class="mt-3">This version does not support this panel version.</Notice>{/if}
			<h4 class="mt-4 font-medium">It asks for</h4>
			<ul class="mt-2 space-y-2.5">
				{#each review?.capabilities ?? [] as c (c.name)}
					<li class="flex items-start gap-3">
						<input id="cap-{c.name}" type="checkbox" class="mt-1" bind:checked={grants[c.name]} disabled={!c.optional || !builtinAdmin} />
						<label for="cap-{c.name}" class="min-w-0">
							<span class="font-medium">{c.label}</span>
							{#if c.risk !== 'low'}<span class="pill ml-1" data-tone={riskTone[c.risk]}>{c.risk === 'high' ? 'Sensitive' : 'Review'}</span>{/if}
							{#if c.optional}<span class="pill ml-1">Optional</span>{/if}
							{#if newCaps(c)}<span class="pill ml-1" data-tone="warn">New in this version</span>{/if}
							<span class="block text-small text-muted">{c.reason}{#if c.hosts?.length} Hosts: {c.hosts.join(', ')}.{/if}</span>
						</label>
					</li>
				{/each}
			</ul>
			{#if v.contributes.bot_enablement}<p class="mt-3 text-small text-muted"><strong class="text-ink">Per bot:</strong> it only touches a bot after someone who manages that bot's variables turns it on for it.</p>{/if}
			{#if builtinAdmin}
				<div class="mt-5 flex flex-wrap gap-2">
					<button class="btn btn-primary" disabled={busy || !reviewing.compatible} onclick={install}>{v.installed ? `Switch to ${reviewing.version}` : 'Install and turn on'}</button>
					{#if v.installed}<button class="btn btn-quiet" onclick={() => (reviewVersion = '')}>Not now</button>{/if}
				</div>
			{:else}
				<p class="mt-4 text-small text-muted">Only built-in administrators install extensions and grant capabilities.</p>
			{/if}
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
								{#if s.description}<p class="text-small text-muted">{s.description}{#if s.inject_hosts?.length} Sent only to {s.inject_hosts.join(', ')}.{/if}</p>{/if}
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
							<div class="min-w-0 flex-1">
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

		<SettingsSection title="Granted capabilities" description="Approved at installation. A version that needs more asks again before it runs.">
			<ul class="grid gap-1.5">
				{#each v.capabilities as c (c.name)}
					<li class="flex items-start gap-2 text-small">
						<Icon name={c.granted ? 'check' : 'x'} size={14} class="mt-0.5 shrink-0 {c.granted ? 'text-run' : 'text-muted'}" />
						<span><span class="font-medium">{c.label}</span>{#if c.hosts?.length} <span class="text-muted">({c.hosts.join(', ')})</span>{/if}</span>
					</li>
				{/each}
			</ul>
			<p class="mt-3 text-small text-muted">Runs with at most {v.memory_mb} MiB of memory and {v.timeout_ms / 1000} s per call.{#if v.bots} Turned on for {v.bots} bot{v.bots === 1 ? '' : 's'}.{/if}</p>
		</SettingsSection>
	{/if}

	<SettingsSection title="Versions" description="The three newest uploads are kept, so you can switch back without uploading again.">
		<ul class="list-card">
			{#each v.versions as x (x.version)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<p class="font-medium">{x.version} {#if x.active}<span class="pill" data-tone="run">Active</span>{/if}{#if !x.compatible}<span class="pill" data-tone="fail">Incompatible</span>{/if}</p>
						<p class="text-small text-muted">{x.source === 'builtin' ? 'Ships with the panel' : x.source === 'dev' ? 'Unsigned' : `Signed by ${x.publisher_key_name || x.key_id}`} · {fmtTime(x.created_at_ms)}{#if x.new_capabilities?.length && !x.active} · asks for {x.new_capabilities.length} more capabilit{x.new_capabilities.length === 1 ? 'y' : 'ies'}{/if}</p>
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
