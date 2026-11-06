<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError, upload } from '$lib/api/client';
	import { fmtTime } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import type { ExtensionView, ExtPublisher } from '$lib/extensions/types';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';

	let list = $state<ExtensionView[] | null>(null);
	let publishers = $state<ExtPublisher[]>([]);
	let safeMode = $state(false);
	let unsignedAllowed = $state(false);
	let error = $state('');
	let uploading = $state(false);
	let pubName = $state('');
	let pubKey = $state('');
	let addingKey = $state(false);
	const builtinAdmin = $derived(session.user?.role === 'admin');

	async function load() {
		try {
			const [r, p] = await Promise.all([
				api<{ extensions: ExtensionView[]; safe_mode: boolean; unsigned_allowed: boolean }>('GET', '/admin/extensions'),
				api<{ publishers: ExtPublisher[] }>('GET', '/admin/extension-publishers')
			]);
			list = r.extensions;
			safeMode = r.safe_mode;
			unsignedAllowed = r.unsigned_allowed;
			publishers = p.publishers;
			error = '';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'Extensions could not be loaded.';
		}
	}
	onMount(load);

	async function onUpload(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file) return;
		uploading = true;
		try {
			const v = (await (await upload('/admin/extensions/upload', file)).json()) as ExtensionView;
			toast(`${v.name} ${v.version} uploaded. Review it before installing.`, 'success');
			goto(`/admin/extensions/${encodeURIComponent(v.id)}?version=${encodeURIComponent(v.version)}`);
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The bundle could not be uploaded.', 'fail');
		} finally {
			uploading = false;
		}
	}

	async function addKey(e: SubmitEvent) {
		e.preventDefault();
		addingKey = true;
		try {
			await api('POST', '/admin/extension-publishers', { name: pubName, public_key: pubKey.trim() });
			pubName = pubKey = '';
			toast('Publisher key trusted', 'success');
			await load();
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The key could not be added.', 'fail');
		} finally {
			addingKey = false;
		}
	}

	async function revoke(p: ExtPublisher) {
		if (!(await confirmDialog({ title: `Revoke ${p.name}?`, body: 'Bundles signed with this key can no longer be installed, and installed extensions running a version it signed stop at once. A revoked key cannot be trusted again.', confirmLabel: 'Revoke key', tone: 'danger', details: [['Key', p.key_id]] }))) return;
		try {
			await api('DELETE', `/admin/extension-publishers/${p.key_id}`);
			toast('Key revoked', 'success');
			await load();
		} catch (err) {
			toast(err instanceof ApiError ? err.message : 'The key could not be revoked.', 'fail');
		}
	}

	const stateTone: Record<string, string | undefined> = { enabled: 'run', failed: 'fail', disabled: undefined, available: undefined };
	const stateLabel: Record<string, string> = { enabled: 'On', disabled: 'Off', failed: 'Stopped', available: 'Not installed' };
	const sourceLabel = (v: ExtensionView) => (v.source === 'builtin' ? 'Built in' : v.source === 'dev' ? 'Unsigned' : 'Signed upload');
	const highRisk = (v: ExtensionView) => v.capabilities.filter((c) => c.risk === 'high').length;
</script>

<svelte:head><title>Extensions · RivetPanel</title></svelte:head>

<div class="flex flex-wrap items-start justify-between gap-3">
	<div class="max-w-3xl">
		<h2 class="text-section">Extensions</h2>
		<p class="mt-1 text-muted">Extensions add pages, bot tabs, overview cards and background jobs. Each runs sandboxed in WebAssembly and can only use the capabilities you grant it; custom pages run in an isolated frame with no access to the panel.</p>
	</div>
	{#if builtinAdmin}
		<label class="btn cursor-pointer focus-within:outline-2 focus-within:outline-action" title="Upload a signed .rpx bundle">
			<Icon name="upload" size={14} />{uploading ? 'Uploading…' : 'Upload bundle'}
			<input type="file" accept=".rpx,application/zip" class="sr-only" disabled={uploading} onchange={onUpload} />
		</label>
	{/if}
</div>

{#if error}<Notice tone="fail" class="mt-4" live>{error}</Notice>{/if}
{#if safeMode}<Notice tone="warn" class="mt-4" title="Safe mode">The panel started with <code>RIVET_EXTENSIONS_SAFE_MODE=1</code>: no extension code runs and no extension appears in the interface until it restarts without it.</Notice>{/if}
{#if unsignedAllowed}<p class="mt-3 text-small text-warn">Development mode: unsigned bundles can be installed. Production panels accept only signed ones.</p>{/if}

{#if !list && !error}
	<div class="mt-5"><Skeleton rows={4} label="Loading extensions" /></div>
{:else if list && !list.length}
	<div class="mt-5">
		<EmptyState title="No extensions yet">
			This build ships no built-in extensions (build with <code>make extensions</code>). Upload a signed bundle from a publisher you trust.
		</EmptyState>
	</div>
{:else if list}
	<ul class="list-card mt-5">
		{#each list as v (v.id)}
			<li>
				<a class="flex flex-col gap-2 px-4 py-3.5 hover:bg-paper-2/50 sm:flex-row sm:items-center sm:gap-6" href="/admin/extensions/{encodeURIComponent(v.id)}">
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-2">
							<h3 class="font-medium">{v.name}</h3>
							<span class="pill" data-tone={stateTone[v.state]}>{stateLabel[v.state]}</span>
							{#if v.update_version}<span class="pill" data-tone="warn">Update {v.update_version}</span>{/if}
						</div>
						<p class="mt-0.5 line-clamp-2 text-small text-muted">{v.description}</p>
						{#if v.state === 'failed' && v.last_error}<p class="mt-1 text-small text-fail">{v.last_error}</p>{/if}
					</div>
					<div class="shrink-0 text-small text-muted sm:text-right">
						<p>{v.publisher} · {v.version}</p>
						<p>{sourceLabel(v)}{#if highRisk(v)} · <span class="text-warn">{highRisk(v)} sensitive capabilit{highRisk(v) === 1 ? 'y' : 'ies'}</span>{/if}</p>
					</div>
				</a>
			</li>
		{/each}
	</ul>
{/if}

<section class="mt-10" aria-labelledby="publishers">
	<h2 id="publishers" class="text-title font-semibold">Trusted publisher keys</h2>
	<p class="mt-1 max-w-3xl text-small text-muted">Uploaded bundles must be signed by one of these Ed25519 keys (create one with <code>rivet-ext keygen</code>). Built-in extensions ship inside the panel and need no key.</p>
	{#if publishers.length}
		<ul class="list-card mt-3">
			{#each publishers as p (p.key_id)}
				<li class="flex flex-wrap items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<p class="font-medium">{p.name} {#if p.revoked_at_ms}<span class="pill" data-tone="fail">Revoked</span>{/if}</p>
						<p class="truncate font-mono text-small text-muted" title={p.public_key}>{p.key_id} · added {fmtTime(p.created_at_ms)}</p>
					</div>
					{#if builtinAdmin && !p.revoked_at_ms}<button class="btn btn-sm btn-danger" onclick={() => revoke(p)}>Revoke</button>{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="mt-3 text-small text-muted">No keys are trusted yet.</p>
	{/if}
	{#if builtinAdmin}
		<form class="mt-4 grid max-w-3xl gap-3 sm:grid-cols-[12rem_minmax(0,1fr)_auto] sm:items-end" onsubmit={addKey}>
			<label class="grid gap-1 text-small"><span class="font-medium">Publisher name</span><input class="field" bind:value={pubName} required maxlength="80" placeholder="Acme Bots" /></label>
			<label class="grid gap-1 text-small"><span class="font-medium">Public key</span><input class="field font-mono" bind:value={pubKey} required placeholder="ed25519:…" spellcheck="false" /></label>
			<button class="btn" disabled={addingKey}>Trust key</button>
		</form>
	{/if}
</section>
