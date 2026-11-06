<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { confirmDialog } from '$lib/ui/dialogs.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Switch from '$lib/components/ui/Switch.svelte';

	type P = {
		id: string; name: string; enabled: boolean; default: boolean; base_url: string; chat_path: string; models_path: string;
		default_model: string; context_size: number | null; max_output_tokens: number; temperature: number; timeout_ms: number;
		input_price_micros: number | null; output_price_micros: number | null; key_set: boolean;
	};
	type S = { search_enabled: boolean; fetch_enabled: boolean; base_url: string; key_count: number; results: number; language: string; categories: string; time_range: string; safe_search: number };
	// The form edits prices in USD per million tokens and the timeout in
	// seconds; the API stores micro-units per million tokens and milliseconds.
	type Form = {
		name: string; enabled: boolean; default: boolean; base_url: string; chat_path: string; models_path: string; default_model: string;
		context_size: number | null; max_output_tokens: number; temperature: number; timeout_s: number; input_price: number | null; output_price: number | null;
	};
	const preset = (): Form => ({ name: 'DeepSeek', enabled: true, default: true, base_url: 'https://api.deepseek.com', chat_path: '/chat/completions', models_path: '/models', default_model: 'deepseek-chat', context_size: null, max_output_tokens: 4096, temperature: 0.2, timeout_s: 120, input_price: null, output_price: null });

	let providers = $state<P[]>([]);
	let search = $state<S | null>(null);
	let error = $state('');
	let key = $state('');
	let searchKeys = $state('');
	let editing = $state<string | null>(null); // provider id, or '' for a new one
	let form = $state<Form>(preset());
	let models = $state<string[]>([]);
	let busy = $state('');
	let searchResult = $state('');
	let searchError = $state('');
	let savedSearch = $state('');

	// Only the input fields: the view also carries read-only markers
	// (key_count, key_set) that the strict API decoder rejects.
	const searchBody = (v: S) => ({ search_enabled: v.search_enabled, fetch_enabled: v.fetch_enabled, base_url: v.base_url.trim(), results: Number(v.results), language: v.language.trim(), categories: v.categories.trim(), time_range: v.time_range, safe_search: Number(v.safe_search) });
	// Test search uses the saved settings, so it waits for unsaved edits.
	const searchDirty = $derived(!!search && (JSON.stringify(searchBody(search)) !== savedSearch || !!searchKeys.trim()));
	function setSearch(v: S) { search = v; savedSearch = JSON.stringify(searchBody(v)); }

	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The AI settings request failed.');
	const micros = (usd: number | null) => (usd === null || (usd as unknown) === '' || Number.isNaN(Number(usd)) ? null : Math.round(Number(usd) * 1_000_000));
	const usd = (m: number | null) => (m === null ? null : m / 1_000_000);
	const num = (v: number | null) => (v === null || (v as unknown) === '' || Number.isNaN(Number(v)) ? null : Number(v));

	function toForm(p: P): Form {
		return { name: p.name, enabled: p.enabled, default: p.default, base_url: p.base_url, chat_path: p.chat_path, models_path: p.models_path, default_model: p.default_model, context_size: p.context_size, max_output_tokens: p.max_output_tokens, temperature: p.temperature, timeout_s: Math.round(p.timeout_ms / 1000), input_price: usd(p.input_price_micros), output_price: usd(p.output_price_micros) };
	}
	function body(f: Form) {
		return { name: f.name.trim(), enabled: f.enabled, default: f.default, base_url: f.base_url.trim(), chat_path: f.chat_path.trim(), models_path: f.models_path.trim(), default_model: f.default_model.trim(), context_size: num(f.context_size), max_output_tokens: Number(f.max_output_tokens), temperature: Number(f.temperature), timeout_ms: Math.round(Number(f.timeout_s) * 1000), input_price_micros: micros(f.input_price), output_price_micros: micros(f.output_price) };
	}

	async function load() {
		try {
			providers = (await api<{ providers: P[] }>('GET', '/admin/ai/providers')).providers;
			setSearch(await api<S>('GET', '/admin/ai/search'));
		} catch (e) { error = msg(e); }
	}
	function startNew() { editing = ''; form = { ...preset(), default: providers.length === 0 }; key = ''; models = []; error = ''; }
	function startEdit(p: P) { editing = p.id; form = toForm(p); key = ''; models = []; error = ''; }
	function stopEdit() { editing = null; key = ''; models = []; }

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (editing === null) return;
		busy = 'save'; error = '';
		try {
			const b: Record<string, unknown> = body(form);
			if (key.trim()) b.key = key.trim();
			if (editing === '') await api('POST', '/admin/ai/providers', b);
			else await api('PATCH', `/admin/ai/providers/${editing}`, b);
			toast(editing === '' ? 'AI provider added' : key.trim() ? 'AI provider and key saved' : 'AI provider saved', 'success');
			stopEdit();
			await load();
		} catch (x) { error = msg(x); } finally { busy = ''; }
	}
	async function setEnabled(p: P, enabled: boolean) {
		busy = p.id; error = '';
		try { await api('PATCH', `/admin/ai/providers/${p.id}`, { ...body(toForm(p)), enabled }); await load(); toast(enabled ? `${p.name} enabled` : `${p.name} disabled`, 'success'); }
		catch (e) { error = msg(e); } finally { busy = ''; }
	}
	async function remove(p: P) {
		const ok = await confirmDialog({ title: `Delete ${p.name}?`, body: 'Its encrypted key is removed. Conversations that used it keep their history but need another provider before they can run again.', confirmLabel: 'Delete provider', tone: 'danger' });
		if (!ok) return;
		busy = p.id; error = '';
		try { await api('DELETE', `/admin/ai/providers/${p.id}`); if (editing === p.id) stopEdit(); await load(); toast('AI provider deleted', 'success'); }
		catch (e) { error = msg(e); } finally { busy = ''; }
	}
	async function test(p: P) {
		busy = p.id; error = '';
		try {
			const r = await api<Record<string, boolean>>('POST', `/admin/ai/providers/${p.id}/test`);
			toast(r.tool_call ? `${p.name} supports streaming tools` : `${p.name} responded, but tool calling was not verified`, r.tool_call ? 'success' : 'fail');
		} catch (e) { error = msg(e); } finally { busy = ''; }
	}
	async function discover() {
		if (!editing) return;
		busy = 'models'; error = '';
		try {
			models = (await api<{ models: string[] }>('GET', `/admin/ai/providers/${editing}/models`)).models ?? [];
			toast(models.length ? `${models.length} models found` : 'The provider listed no models; enter an ID manually', models.length ? 'success' : 'info');
		} catch (e) { error = msg(e); } finally { busy = ''; }
	}

	async function saveSearch(e: SubmitEvent) {
		e.preventDefault();
		if (!search) return;
		busy = 'search'; searchError = ''; searchResult = '';
		try {
			const keys = searchKeys.trim() ? searchKeys.split('\n').map((x) => x.trim()).filter(Boolean) : undefined;
			setSearch(await api<S>('PUT', '/admin/ai/search', { ...searchBody(search), keys }));
			searchKeys = '';
			toast('Research settings saved', 'success');
		} catch (x) { searchError = msg(x); } finally { busy = ''; }
	}
	async function testSearch() {
		busy = 'search-test'; searchError = ''; searchResult = '';
		try {
			const r = await api<{ Answer?: string; Results?: { Title: string; URL: string }[] }>('POST', '/admin/ai/search/test');
			const n = r.Results?.length ?? 0;
			searchResult = n ? `${n} public result${n === 1 ? '' : 's'}, e.g. ${r.Results![0].Title || r.Results![0].URL}` : 'The search service answered with no public results.';
		} catch (e) { searchError = msg(e); } finally { busy = ''; }
	}
	onMount(load);

	// Starting points for the connection fields; everything stays editable.
	const presets = [
		{ id: 'deepseek', name: 'DeepSeek', base_url: 'https://api.deepseek.com', model: 'deepseek-chat' },
		{ id: 'openai', name: 'OpenAI', base_url: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
		{ id: 'openrouter', name: 'OpenRouter', base_url: 'https://openrouter.ai/api/v1', model: 'openai/gpt-4o-mini' },
		{ id: 'custom', name: '', base_url: '', model: '' }
	];
	let presetId = $state('');
	function applyPreset(id: string) {
		const p = presets.find((x) => x.id === id);
		if (!p) return;
		presetId = id;
		form.name = p.name || form.name;
		form.base_url = p.base_url;
		form.default_model = p.model;
	}
	const defaults = preset();
	let showTuning = $state(false);
	// How many optional settings differ from their defaults, shown on the closed section.
	const tuned = $derived(
		[
			form.chat_path !== defaults.chat_path, form.models_path !== defaults.models_path, form.context_size !== null && (form.context_size as unknown) !== '',
			Number(form.max_output_tokens) !== defaults.max_output_tokens, Number(form.temperature) !== defaults.temperature, Number(form.timeout_s) !== defaults.timeout_s,
			form.input_price !== null && (form.input_price as unknown) !== '', form.output_price !== null && (form.output_price as unknown) !== ''
		].filter(Boolean).length
	);
	// Open the optional section only when editing a provider that already uses it;
	// untrack so typing in it does not collapse it again.
	$effect(() => {
		const id = editing;
		if (id !== null) showTuning = id !== '' && untrack(() => tuned) > 0;
	});
	const defaultProvider = $derived(providers.find((p) => p.default && p.enabled) ?? providers.find((p) => p.enabled));
	const ready = $derived(!!defaultProvider?.key_set);
</script>

<div class="grid gap-4 xl:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]">
	<!-- Providers -->
	<section id="ai-providers" class="card min-w-0 p-5 sm:p-6" aria-labelledby="ai-prov-h">
		<div class="flex flex-wrap items-start gap-3">
			<div class="min-w-48 flex-1">
				<h3 id="ai-prov-h" class="flex flex-wrap items-center gap-2 text-title font-semibold">Providers
					{#if editing === null}<span class="pill" data-tone={ready ? 'run' : 'warn'}>{ready ? 'Assistant ready' : providers.length ? 'No usable provider' : 'Not set up'}</span>{/if}
				</h3>
				<p class="mt-1 text-small text-muted">Any OpenAI-compatible endpoint. Keys are encrypted and never shown again.</p>
			</div>
			{#if editing === null}<button type="button" class="btn btn-sm" onclick={startNew}><Icon name="plus" size={12} />Add provider</button>{/if}
		</div>
		{#if error}<p class="mt-3 text-small text-fail" role="alert">{error}</p>{/if}

		{#if editing === null}
			<ul class="mt-4 grid gap-2">
				{#each providers as p (p.id)}
					<li class="rounded-control border border-rule-soft p-3 {p.enabled ? '' : 'opacity-75'}">
						<div class="flex flex-wrap items-center gap-2">
							<strong>{p.name}</strong>
							<span class="pill" data-tone={p.enabled ? 'run' : undefined}>{p.enabled ? 'Enabled' : 'Disabled'}</span>
							{#if p.default}<span class="pill" data-tone="warn">Default</span>{/if}
							{#if !p.key_set}<span class="pill" data-tone="fail">Key missing</span>{/if}
						</div>
						<p class="mt-1 truncate font-mono text-[12px] text-muted" title={p.base_url}>{p.default_model} · {p.base_url.replace(/^https?:\/\//, '')}</p>
						<div class="mt-2 flex flex-wrap gap-1.5">
							<button type="button" class="btn btn-sm" disabled={!!busy || !p.key_set} onclick={() => test(p)}>{busy === p.id ? 'Working…' : 'Test'}</button>
							<button type="button" class="btn btn-sm" disabled={!!busy} onclick={() => startEdit(p)}><Icon name="pencil" size={12} />Edit</button>
							<button type="button" class="btn btn-sm" disabled={!!busy} onclick={() => setEnabled(p, !p.enabled)}>{p.enabled ? 'Disable' : 'Enable'}</button>
							<button type="button" class="btn btn-sm btn-quiet ml-auto text-fail" disabled={!!busy} onclick={() => remove(p)} aria-label="Delete {p.name}"><Icon name="trash" size={12} /></button>
						</div>
					</li>
				{:else}
					<li class="rounded-control border border-dashed border-rule p-4 text-small text-muted">
						<p class="font-medium text-ink">No provider yet</p>
						<p class="mt-0.5">The assistant stays unavailable until one is added. Hosting bots works either way.</p>
						<button type="button" class="btn btn-sm btn-primary mt-3" onclick={startNew}><Icon name="plus" size={12} />Add the first provider</button>
					</li>
				{/each}
			</ul>
		{:else}
			<form class="mt-4 grid gap-5" onsubmit={save}>
				<div class="flex items-center justify-between gap-2 border-b border-rule-soft pb-2">
					<h4 class="font-semibold">{editing === '' ? 'New provider' : `Edit ${form.name}`}</h4>
					<p class="text-small text-muted"><span class="req">*</span> required · the rest is <span class="opt !ml-0">Optional</span></p>
				</div>

				{#if editing === ''}
					<div>
						<span class="label">Start from</span>
						<div class="flex flex-wrap gap-1.5" role="group" aria-label="Provider presets">
							{#each presets as p (p.id)}
								<button type="button" class="rounded-pill border px-3 py-1 text-small {presetId === p.id ? 'border-action bg-action/10 font-medium' : 'border-rule-soft text-muted hover:text-ink'}" aria-pressed={presetId === p.id} onclick={() => applyPreset(p.id)}>{p.id === 'custom' ? 'Custom endpoint' : p.name}</button>
							{/each}
						</div>
					</div>
				{/if}

				<fieldset class="grid gap-3 sm:grid-cols-2">
					<legend class="eyebrow mb-2">Connection</legend>
					<label><span class="label">Name<span class="req" aria-hidden="true">*</span></span><input class="field" required maxlength="80" bind:value={form.name} placeholder="DeepSeek" /></label>
					<label><span class="label">Base API URL<span class="req" aria-hidden="true">*</span></span><input class="field font-mono" required bind:value={form.base_url} placeholder="https://api.deepseek.com" /></label>
					<label class="sm:col-span-2">
						<span class="label">API key{#if editing === ''}<span class="req" aria-hidden="true">*</span>{:else}<span class="opt">Optional · replace</span>{/if}</span>
						<input class="field font-mono" type="password" bind:value={key} autocomplete="new-password" required={editing === ''} placeholder={editing === '' ? 'Paste the key' : 'Leave blank to keep the current key'} />
						<span class="help">Sealed with the panel keyring; it is never shown again.</span>
					</label>
					<label class="sm:col-span-2">
						<span class="label">Default model<span class="req" aria-hidden="true">*</span></span>
						<div class="flex gap-2">
							<input class="field min-w-0 flex-1 font-mono" required bind:value={form.default_model} list="ai-provider-models" placeholder="deepseek-chat" />
							{#if editing}<button type="button" class="btn" disabled={!!busy} onclick={discover}>{busy === 'models' ? 'Loading…' : 'Discover models'}</button>{/if}
						</div>
						<datalist id="ai-provider-models">{#each models as m}<option value={m}></option>{/each}</datalist>
						<span class="help">{editing ? 'Discovery uses the saved key; you may also type any model ID.' : 'Save the provider first to discover models from its API.'}</span>
					</label>
				</fieldset>

				<div class="rounded-control border border-rule-soft">
					<button type="button" class="flex w-full items-center gap-2 px-3 py-2.5 text-left" aria-expanded={showTuning} onclick={() => (showTuning = !showTuning)}>
						<Icon name={showTuning ? 'chevronDown' : 'chevronRight'} size={14} class="text-muted" />
						<span class="font-medium">Tuning and pricing</span><span class="opt !ml-0">Optional</span>
						<span class="ml-auto text-small text-muted">{tuned ? `${tuned} changed from the defaults` : 'Defaults are fine for most providers'}</span>
					</button>
					{#if showTuning}
						<div class="grid gap-3 border-t border-rule-soft p-3 sm:grid-cols-2">
							<label><span class="label">Max output tokens<span class="opt">Optional</span></span><input class="field" type="number" min="64" max="131072" required bind:value={form.max_output_tokens} /><span class="help">Default {defaults.max_output_tokens}.</span></label>
							<label><span class="label">Temperature<span class="opt">Optional</span></span><input class="field" type="number" min="0" max="2" step="0.05" required bind:value={form.temperature} /><span class="help">Default {defaults.temperature}. Lower is steadier.</span></label>
							<label><span class="label">Timeout, seconds<span class="opt">Optional</span></span><input class="field" type="number" min="5" max="600" required bind:value={form.timeout_s} /><span class="help">Default {defaults.timeout_s}.</span></label>
							<label><span class="label">Context size, tokens<span class="opt">Optional</span></span><input class="field" type="number" min="1024" max="2000000" bind:value={form.context_size} placeholder="Provider default" /></label>
							<label><span class="label">Input price, USD per 1M tokens<span class="opt">Optional</span></span><input class="field" type="number" min="0" step="0.0001" bind:value={form.input_price} placeholder="Not tracked" /></label>
							<label><span class="label">Output price, USD per 1M tokens<span class="opt">Optional</span></span><input class="field" type="number" min="0" step="0.0001" bind:value={form.output_price} placeholder="Not tracked" /></label>
							<label><span class="label">Chat completions path<span class="opt">Optional</span></span><input class="field font-mono" required bind:value={form.chat_path} /></label>
							<label><span class="label">Models path<span class="opt">Optional</span></span><input class="field font-mono" required bind:value={form.models_path} /></label>
						</div>
					{/if}
				</div>

				<div class="grid gap-3 sm:grid-cols-2">
					<Switch bind:checked={form.enabled} label="Enabled">People can use this provider.</Switch>
					<Switch bind:checked={form.default} label="Default provider">Used for new conversations.</Switch>
				</div>
				<div class="flex justify-end gap-2 border-t border-rule-soft pt-3">
					<button type="button" class="btn" onclick={stopEdit}>Cancel</button>
					<button class="btn btn-primary" disabled={busy === 'save'}>{busy === 'save' ? 'Saving…' : editing === '' ? 'Add provider' : 'Save provider'}</button>
				</div>
			</form>
		{/if}
	</section>

	<!-- Web research -->
	{#if search}
		<section id="ai-research" class="card min-w-0 p-5 sm:p-6" aria-labelledby="ai-res-h">
			<div class="flex flex-wrap items-center gap-2">
				<h3 id="ai-res-h" class="text-title font-semibold">Web research</h3>
				<span class="opt !ml-0">Optional</span>
				<span class="pill ml-auto" data-tone={search.search_enabled ? 'run' : undefined}>{search.search_enabled ? 'On' : 'Off'}</span>
			</div>
			<p class="mt-1 text-small text-muted">Lets the assistant look things up while debugging. SearxNG or Risa compatible.</p>
			<form class="mt-4 grid gap-4" onsubmit={saveSearch}>
				<div class="grid gap-3">
					<Switch bind:checked={search.search_enabled} label="Search the web">Send search queries to the address below.</Switch>
					<Switch bind:checked={search.fetch_enabled} label="Read public pages">Fetch result pages. Private and local addresses are always refused.</Switch>
				</div>
				<fieldset class="grid gap-3 transition-opacity {search.search_enabled ? '' : 'pointer-events-none opacity-50'}" disabled={!search.search_enabled}>
					<label>
						<span class="label">Search base URL{#if search.search_enabled}<span class="req" aria-hidden="true">*</span>{/if}</span>
						<input class="field font-mono" bind:value={search.base_url} required={search.search_enabled} placeholder="https://search.example.com" />
						<span class="help">May be a private, self-hosted address. Credentials are sent only here.</span>
					</label>
					<div class="grid gap-3 sm:grid-cols-2">
						<label><span class="label">Results per query<span class="opt">Optional</span></span><input class="field" type="number" min="1" max="10" bind:value={search.results} /></label>
						<label><span class="label">Safe search<span class="opt">Optional</span></span><select class="field" bind:value={search.safe_search}><option value={0}>Off</option><option value={1}>Moderate</option><option value={2}>Strict</option></select></label>
						<label><span class="label">Language<span class="opt">Optional</span></span><input class="field" bind:value={search.language} placeholder="auto" /></label>
						<label><span class="label">Time range<span class="opt">Optional</span></span><select class="field" bind:value={search.time_range}><option value="">Any</option><option value="day">Day</option><option value="week">Week</option><option value="month">Month</option><option value="year">Year</option></select></label>
						<label class="sm:col-span-2"><span class="label">Categories<span class="opt">Optional</span></span><input class="field" bind:value={search.categories} placeholder="general, it" /></label>
						<label class="sm:col-span-2"><span class="label">API keys<span class="opt">Optional</span></span><textarea class="field min-h-16 font-mono" bind:value={searchKeys} placeholder={search.key_count ? `${search.key_count} encrypted key(s) set. Paste new ones, one per line, to replace them` : 'Only if the service needs a key. One per line'}></textarea></label>
					</div>
				</fieldset>
				{#if searchError}<p class="text-small text-fail" role="alert">{searchError}</p>{/if}
				{#if searchResult}<p class="text-small text-run" role="status">{searchResult}</p>{/if}
				{#if searchDirty}<p class="text-small text-muted">Unsaved changes. Save before testing; the test uses the saved settings.</p>{/if}
				<div class="flex flex-wrap justify-end gap-2 border-t border-rule-soft pt-3">
					<button type="button" class="btn" disabled={!!busy || searchDirty || !search.search_enabled} onclick={testSearch} title="Tests the saved settings">{busy === 'search-test' ? 'Testing…' : 'Test search'}</button>
					<button class="btn btn-primary" disabled={busy === 'search' || !searchDirty}>{busy === 'search' ? 'Saving…' : 'Save'}</button>
				</div>
			</form>
		</section>
	{/if}
</div>
